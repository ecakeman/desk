package run

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"desk/internal/ctxmgr"
	"desk/internal/event"
	"desk/internal/ids"
	"desk/internal/prompt"
	"desk/internal/testdb"
	"desk/internal/worker"
)

// captureWorker 记录 context.replace / tool.result 的协议顺序。
type captureWorker struct {
	mu       sync.Mutex
	replaces [][]map[string]any
	results  []worker.In
}

func (w *captureWorker) Handle(in worker.In, _ func(worker.Out) error) (*worker.Out, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch in.T {
	case "context.replace":
		copied := append([]map[string]any{}, in.Messages...)
		w.replaces = append(w.replaces, copied)
		return &worker.Out{T: "context.replaced"}, nil
	case "tool.result", "tool.denied":
		w.results = append(w.results, in)
		return &worker.Out{T: "turn.finish", Text: "ok"}, nil
	default:
		return &worker.Out{T: "turn.finish", Text: "ok"}, nil
	}
}

func (w *captureWorker) Done(string) {}

func TestRebuildPendingToolBoundaryDefersDynamicSuffix(t *testing.T) {
	w := &captureWorker{}
	work := t.TempDir()
	runService, db := contractEnv(t, w, work)

	// 极小窗口迫使 eviction → Rebuild；PendingTool 使 Window 以 tool_calls 收尾。
	okJSON := []byte(`{"summary":"保留工具推进状态足够长","facts":[{"key":"step","value":"read","status":"active","confidence":0.9,"source_event_seqs":[1]}],"open_items":[],"decisions":["continue"]}`)
	contextManager := ctxmgr.New(runService.Events, runService.Index, ctxmgr.Settings{
		WindowTokens:        40,
		TotalTokens:         200_000,
		EvictedBufferTokens: 200_000,
		SmallTriggerTok:     1,
		LargeSmallCount:     99,
		PromptsDir:          filepath.Join(repoRoot(t), "prompts"),
	})
	contextManager.Compactor = &ctxmgr.StubCompactor{Raw: okJSON}
	runService.Context = contextManager

	sessionID := testdb.InsertSession(t, db)
	runID := ids.New()
	if _, err := db.Exec(`INSERT INTO runs(id,session_id,status,workspace_dir) VALUES($1,$2,'running',$3)`, runID, sessionID, work); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runService.Events.Append(context.Background(), tx, runID, event.TypeMessageUser, map[string]string{
			"text": strings.Repeat("boundary-pad-", 8) + ids.New(),
		}); err != nil {
			t.Fatal(err)
		}
		_ = tx.Commit()
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runService.Events.Append(context.Background(), tx, runID, event.TypeToolRequested, map[string]any{
		"id": "call-boundary", "name": "fs.read", "args": map[string]any{"path": "a.md"},
	}); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()

	snapshot, err := prompt.Load(runService.PromptsDir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runService.ask(context.Background(), runID, sessionID, work, snapshot, worker.In{
		T: "tool.result", ID: "call-boundary", Phase: "review", OK: true,
		Data: json.RawMessage(`{"ok":true,"text":"hi"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(w.replaces) != 1 {
		t.Fatalf("context.replace calls=%d want 1 (Rebuild)", len(w.replaces))
	}
	replace := w.replaces[0]
	if !endsWithPendingToolCall(replace) {
		t.Fatalf("replace must end with pending tool_calls, last=%#v", lastMsg(replace))
	}
	for _, msg := range replace {
		content, _ := msg["content"].(string)
		if strings.Contains(content, "[CONTEXT: MEMORY]") || strings.Contains(content, "[CONTEXT: SKILL]") {
			t.Fatalf("dynamic suffix leaked into replace: %q", content)
		}
	}
	if len(w.results) != 1 {
		t.Fatalf("tool.result calls=%d", len(w.results))
	}
	result := w.results[0]
	if result.SkipRuntime {
		t.Fatal("tool.result must keep runtime for post-tool injection")
	}
	for _, msg := range result.Messages {
		if hasToolCalls(msg) {
			t.Fatal("deferred must not contain assistant tool_calls")
		}
		if role, _ := msg["role"].(string); role == "tool" {
			t.Fatal("deferred must not contain tool role; Worker appends tool first")
		}
	}

	// Provider 顺序不变量：assistant(tool_calls) → tool → deferred → runtime
	provider := append([]map[string]any{}, replace...)
	provider = append(provider, map[string]any{
		"role": "tool", "tool_call_id": "call-boundary", "content": `{"ok":true}`,
	})
	provider = append(provider, result.Messages...)
	if result.Runtime != "" {
		provider = append(provider, map[string]any{"role": "user", "content": result.Runtime})
	}
	asst, tool := -1, -1
	for i, msg := range provider {
		role, _ := msg["role"].(string)
		if role == "assistant" && hasToolCalls(msg) {
			asst = i
		}
		if role == "tool" && tool < 0 {
			tool = i
		}
	}
	if asst < 0 || tool != asst+1 {
		t.Fatalf("provider order broken: assistant@%d tool@%d len=%d", asst, tool, len(provider))
	}
}

func lastMsg(messages []map[string]any) map[string]any {
	if len(messages) == 0 {
		return nil
	}
	return messages[len(messages)-1]
}
