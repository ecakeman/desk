package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"desk/internal/event"
	"desk/internal/memory"
	"desk/internal/plugin"
	"desk/internal/task"
	"desk/internal/testdb"
	"desk/internal/worker"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func buildFSPlugin(t *testing.T) {
	t.Helper()
	root := repoRoot(t)
	cmd := exec.Command("go", "build", "-o", filepath.Join(root, "plugins/fs/fs"), "./plugins/fs")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fs: %s %v", out, err)
	}
}

func contractEnv(t *testing.T, w worker.Worker, work string) (*Service, *sql.DB) {
	t.Helper()
	buildFSPlugin(t)
	if work == "" {
		work = t.TempDir()
	}
	db := testdb.Open(t)
	root := repoRoot(t)
	reg, err := plugin.Load(filepath.Join(root, "plugins"), work)
	if err != nil {
		t.Fatal(err)
	}
	ev := event.NewStore(db)
	idx := memory.New(db)
	ev.OnInsert = idx.IndexTx
	reg.Put(pingPlugin{})
	reg.Put(memory.NewHost(idx))
	reg.Put(task.NewHost(db, ev))
	runService := NewService(db, ev)
	runService.Plugins = reg
	runService.Worker = w
	runService.Index = idx
	runService.PromptsDir = filepath.Join(root, "prompts")
	return runService, db
}

type recWorker struct {
	mu   sync.Mutex
	asks []worker.In
	fn   func(worker.In) *worker.Out
}

func (w *recWorker) Handle(in worker.In, emit func(worker.Out) error) (*worker.Out, error) {
	w.mu.Lock()
	w.asks = append(w.asks, in)
	w.mu.Unlock()
	if in.T == "context.replace" {
		return &worker.Out{T: "context.replaced"}, nil
	}
	cached := 3
	if in.Model == "pro" {
		cached = 9
	}
	if emit != nil {
		_ = emit(worker.Out{
			T:            "model.usage",
			InputTokens:  20,
			OutputTokens: 4,
			CachedTokens: cached,
		})
	}
	if w.fn == nil {
		return &worker.Out{T: "turn.finish", Text: "ok"}, nil
	}
	out := w.fn(in)
	if out == nil {
		return &worker.Out{T: "turn.finish", Text: "ok"}, nil
	}
	return out, nil
}

func (w *recWorker) snapshot() []worker.In {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]worker.In, len(w.asks))
	copy(out, w.asks)
	return out
}

func (w *recWorker) Done(string) {}

func lastUser(in worker.In) string {
	for i := len(in.Messages) - 1; i >= 0; i-- {
		role, _ := in.Messages[i]["role"].(string)
		if role != "user" {
			continue
		}
		s, _ := in.Messages[i]["content"].(string)
		return s
	}
	return ""
}

func loadEvents(t *testing.T, db *sql.DB, runID string) []event.Event {
	t.Helper()
	rows, err := db.Query(`SELECT run_id, seq, type, payload FROM events WHERE run_id=$1 ORDER BY seq`, runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []event.Event
	for rows.Next() {
		var e event.Event
		if err := rows.Scan(&e.RunID, &e.Seq, &e.Type, &e.Payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func payloadID(raw json.RawMessage) string {
	var p struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &p)
	return p.ID
}

func payloadHash(raw json.RawMessage) string {
	var p struct {
		Hash string `json:"hash"`
	}
	_ = json.Unmarshal(raw, &p)
	return p.Hash
}

func typesOf(events []event.Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Type
	}
	return out
}

func hasType(events []event.Event, typ string) bool {
	for _, e := range events {
		if e.Type == typ {
			return true
		}
	}
	return false
}

func countType(events []event.Event, typ string) int {
	n := 0
	for _, e := range events {
		if e.Type == typ {
			n++
		}
	}
	return n
}

// projectRunStatus 只根据事件解释 Run 状态；与 runs.status 应对齐。
func projectRunStatus(events []event.Event) (string, error) {
	requested := map[string]bool{}
	started := map[string]bool{}
	openAsk := map[string]bool{}
	terminal := ""
	for i, e := range events {
		if e.Seq != i+1 {
			return "", fmt.Errorf("seq not 1..n: index=%d seq=%d", i, e.Seq)
		}
		if terminal != "" {
			return "", fmt.Errorf("event %s after terminal %s", e.Type, terminal)
		}
		id := payloadID(e.Payload)
		switch e.Type {
		case event.TypeRunCompleted:
			terminal = StatusCompleted
		case event.TypeRunFailed:
			terminal = StatusFailed
		case event.TypeRunInterrupted:
			terminal = StatusInterrupted
		case event.TypeToolRequested:
			if id == "" {
				return "", fmt.Errorf("tool.requested missing id at seq=%d", e.Seq)
			}
			requested[id] = true
			openAsk[id] = true
		case event.TypeToolStarted:
			if !requested[id] {
				return "", fmt.Errorf("tool.started without requested id=%s", id)
			}
			started[id] = true
			delete(openAsk, id)
		case event.TypeToolCompleted:
			if !started[id] {
				return "", fmt.Errorf("tool.completed without started id=%s", id)
			}
			delete(openAsk, id)
		case event.TypeToolDenied, event.TypeToolFailed:
			if !requested[id] {
				return "", fmt.Errorf("%s without requested id=%s", e.Type, id)
			}
			delete(openAsk, id)
		}
	}
	if terminal != "" {
		return terminal, nil
	}
	if len(openAsk) > 0 {
		return StatusWaitingApproval, nil
	}
	return StatusRunning, nil
}

func assertEventConsistency(t *testing.T, db *sql.DB, runID string) {
	t.Helper()
	events := loadEvents(t, db, runID)
	if len(events) == 0 {
		t.Fatal("no events")
	}
	seen := map[int]bool{}
	for i, e := range events {
		if seen[e.Seq] {
			t.Fatalf("duplicate seq %d", e.Seq)
		}
		seen[e.Seq] = true
		if e.Seq != i+1 {
			t.Fatalf("seq gap: %+v", typesOf(events))
		}
	}
	projected, err := projectRunStatus(events)
	if err != nil {
		t.Fatalf("project: %v events=%v", err, typesOf(events))
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM runs WHERE id=$1`, runID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if projected != status {
		t.Fatalf("projected %s != runs.status %s events=%v", projected, status, typesOf(events))
	}
}

func copyPrompts(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	src := filepath.Join(repoRoot(t), "prompts")
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	return dst
}

func report(t *testing.T, name string, pairs ...string) {
	t.Helper()
	if len(pairs)%2 != 0 {
		t.Fatalf("report %s: odd kv", name)
	}
	fmt.Printf("::evidence::[PASS] %s\n", name)
	for i := 0; i < len(pairs); i += 2 {
		fmt.Printf("::evidence::       %s: %s\n", pairs[i], pairs[i+1])
	}
}

func runStatus(t *testing.T, db *sql.DB, runID string) string {
	t.Helper()
	var st string
	if err := db.QueryRow(`SELECT status FROM runs WHERE id=$1`, runID).Scan(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

func firstSeq(events []event.Event, typ string) int {
	for _, e := range events {
		if e.Type == typ {
			return e.Seq
		}
	}
	return 0
}

func terminalEvent(events []event.Event) string {
	for i := len(events) - 1; i >= 0; i-- {
		switch events[i].Type {
		case event.TypeRunCompleted, event.TypeRunFailed, event.TypeRunInterrupted:
			return events[i].Type
		}
	}
	return ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func postWait(t *testing.T, runService *Service, db *sql.DB, sessionID, text, work, want string) string {
	t.Helper()
	runID, err := runService.PostUserMessage(context.Background(), sessionID, text, work)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runID, want)
	return runID
}
