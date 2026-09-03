package ctxmgr

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"desk/internal/event"
	"desk/internal/ids"
)

func countType(t *testing.T, ev *event.Store, runID, typ string) int {
	t.Helper()
	var n int
	if err := ev.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`, runID, typ).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func windowContents(asm Assembly) []string {
	var out []string
	for _, m := range asm.Layers.Window {
		out = append(out, fmtString(m["content"]))
	}
	return out
}

func TestInvariantFinalEstimateLeqTotal(t *testing.T) {
	m, ev, sess, runID := testMgr(t, 100000, &StubCompactor{Err: context.Canceled})
	m.Settings.TotalTokens = 80
	m.Settings.SmallTriggerTok = 1_000_000
	for i := 0; i < 10; i++ {
		appendUser(t, ev, runID, strings.Repeat("inv1-", 10)+ids.New())
	}
	asm, err := m.Prepare(context.Background(), PrepareIn{SessionID: sess, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	got := EstimateLLMInput("", nil, asm.Messages, "")
	if got > m.Settings.TotalTokens && asm.Applied.OverBudget != "pending_tool" {
		t.Fatalf("est %d > total %d", got, m.Settings.TotalTokens)
	}
}

func TestInvariantEvictedNeverResurrects(t *testing.T) {
	m, ev, sess, runID := testMgr(t, 100000, &StubCompactor{Err: context.Canceled})
	m.Settings.TotalTokens = 60
	m.Settings.SmallTriggerTok = 1_000_000
	var first string
	for i := 0; i < 8; i++ {
		text := "mark-" + ids.New() + strings.Repeat(" x", 8)
		if i == 0 {
			first = text
		}
		appendUser(t, ev, runID, text)
	}
	a1, err := m.Prepare(context.Background(), PrepareIn{SessionID: sess, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	if countType(t, ev, runID, event.TypeContextEvicted) < 1 {
		t.Fatal("expected durable eviction")
	}
	in1 := false
	for _, c := range windowContents(a1) {
		if strings.Contains(c, first) {
			in1 = true
		}
	}
	if in1 {
		t.Fatal("oldest should have left window")
	}
	appendUser(t, ev, runID, "newer-only")
	a2, err := m.Prepare(context.Background(), PrepareIn{SessionID: sess, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range windowContents(a2) {
		if strings.Contains(c, first) {
			t.Fatal("evicted history resurrected")
		}
	}
}

func TestInvariantSmallFailNoRetryUntilNewEvict(t *testing.T) {
	fail := &StubCompactor{Raw: []byte(`not-json`)}
	m, ev, sess, runID := testMgr(t, 20, fail)
	for i := 0; i < 8; i++ {
		appendUser(t, ev, runID, strings.Repeat("retry-pad-", 8)+ids.New())
	}
	if _, err := m.Prepare(context.Background(), PrepareIn{SessionID: sess, RunID: runID}); err != nil {
		t.Fatal(err)
	}
	n1 := fail.N
	if n1 < 1 {
		t.Fatal("expected compact attempt")
	}
	if countType(t, ev, runID, event.TypeContextSmallCompact) != 0 {
		t.Fatal("forged compact")
	}
	if countType(t, ev, runID, event.TypeContextCompactFailed) < 1 {
		t.Fatal("missing compact_failed")
	}
	if _, err := m.Prepare(context.Background(), PrepareIn{SessionID: sess, RunID: runID}); err != nil {
		t.Fatal(err)
	}
	if fail.N != n1 {
		t.Fatalf("retried compact without new eviction N %d -> %d", n1, fail.N)
	}
	okJSON := []byte(`{"summary":"保留了任务状态与文件约束足够长","facts":[{"key":"k","value":"v","status":"active","confidence":0.9,"source_event_seqs":[1]}],"open_items":[],"decisions":[]}`)
	fail.Raw = okJSON
	fail.Err = nil
	appendUser(t, ev, runID, strings.Repeat("new-evict-", 10)+ids.New())
	if _, err := m.Prepare(context.Background(), PrepareIn{SessionID: sess, RunID: runID}); err != nil {
		t.Fatal(err)
	}
	if fail.N <= n1 {
		t.Fatal("expected retry after new eviction")
	}
	if countType(t, ev, runID, event.TypeContextSmallCompact) < 1 {
		t.Fatal("expected small after retry")
	}
}

func TestInvariantActiveLargeUniqueAndSmallsAfter(t *testing.T) {
	smallJSON := func(seq int) []byte {
		return []byte(`{"summary":"小压缩保留当前书签任务状态","facts":[{"key":"k","value":"v","status":"active","confidence":0.8,"source_event_seqs":[` + itoa(seq) + `]}],"open_items":["x"],"decisions":["d"]}`)
	}
	stub := &StubCompactor{Raw: smallJSON(1)}
	m, ev, sess, runID := testMgr(t, 15, stub)
	m.Settings.LargeSmallCount = 2
	m.Settings.SmallTriggerTok = 1
	for i := 0; i < 12; i++ {
		appendUser(t, ev, runID, strings.Repeat("large-payload-", 10)+ids.New())
	}
	if _, err := m.Prepare(context.Background(), PrepareIn{SessionID: sess, RunID: runID}); err != nil {
		t.Fatal(err)
	}
	stub.Raw = smallJSON(2)
	for i := 0; i < 12; i++ {
		appendUser(t, ev, runID, strings.Repeat("large-payload-b-", 10)+ids.New())
	}
	asm, err := m.Prepare(context.Background(), PrepareIn{SessionID: sess, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	if len(asm.Layers.Large) > 1 {
		t.Fatalf("active large %d", len(asm.Layers.Large))
	}
	events, err := ev.ListBySession(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	layers := parseLayers(events, runID, "")
	if layers.large != nil {
		for _, s := range layers.smalls {
			if s.Seq <= layers.large.Seq && s.RunID == layers.large.RunID {
				t.Fatalf("small seq %d not after large %d", s.Seq, layers.large.Seq)
			}
		}
	}
}

func TestInvariantFactProvenanceAllowed(t *testing.T) {
	raw := []byte(`{"summary":"保留了任务状态与文件约束足够长","facts":[{"key":"x","value":"y","status":"active","confidence":1,"source_refs":[{"run_id":"r","seq":1}]}],"open_items":[],"decisions":[]}`)
	if _, err := ValidateResult(raw, []SourceRef{{RunID: "r", Seq: 1}}, 200, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateResult(raw, []SourceRef{{RunID: "other", Seq: 1}}, 200, 0); err == nil {
		t.Fatal("unknown source")
	}
}

func TestInvariantNormalCallPrefixStable(t *testing.T) {
	m, ev, sess, runID := testMgr(t, 100000, &StubCompactor{Err: context.Canceled})
	appendUser(t, ev, runID, "prefix-user")
	a1, err := m.Prepare(context.Background(), PrepareIn{SessionID: sess, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	a2, err := m.Prepare(context.Background(), PrepareIn{
		SessionID: sess, RunID: runID,
		FrozenHits: []RetrievalHit{{RunID: runID, Seq: 1, Kind: "message.user", Text: "tail"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if a2.Rebuild {
		t.Fatal("tail-only change must not rebuild")
	}
	n := len(a1.Layers.Window) + len(a1.Layers.Large) + len(a1.Layers.Smalls) + len(a1.Layers.Facts)
	for i := 0; i < n; i++ {
		b1, _ := json.Marshal(a1.Messages[i])
		b2, _ := json.Marshal(a2.Messages[i])
		if string(b1) != string(b2) {
			t.Fatalf("prefix %d", i)
		}
	}
}

func TestInvariantReconstructSkipsCompactLLM(t *testing.T) {
	stub := &StubCompactor{Err: context.Canceled}
	m, ev, sess, runID := testMgr(t, 100000, stub)
	appendUser(t, ev, runID, "inspect-skip-llm")
	asm, err := m.Prepare(context.Background(), PrepareIn{SessionID: sess, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	n := stub.N
	ctx := context.Background()
	tx, err := ev.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ev.Append(ctx, tx, runID, event.TypeContextApplied, asm.Applied); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()
	m.Forget(runID)
	if _, src, ok := m.Inspect(ctx, sess, runID); !ok || src != "reconstructable" {
		t.Fatalf("src=%s ok=%v", src, ok)
	}
	if stub.N != n {
		t.Fatalf("reconstruct invoked compact N %d -> %d", n, stub.N)
	}
}

func TestWindowBudgetUsesRemainingWhenTotalSmaller(t *testing.T) {
	m, ev, sess, runID := testMgr(t, 4000, &StubCompactor{Err: context.Canceled})
	m.Settings.TotalTokens = 50
	m.Settings.SmallTriggerTok = 1_000_000
	for i := 0; i < 6; i++ {
		appendUser(t, ev, runID, strings.Repeat("win-", 12)+ids.New())
	}
	asm, err := m.Prepare(context.Background(), PrepareIn{SessionID: sess, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	if asm.Applied.WindowBudget > m.Settings.TotalTokens {
		t.Fatalf("window_budget %d > total", asm.Applied.WindowBudget)
	}
	if asm.Applied.WindowEstimate > asm.Applied.WindowBudget && asm.Applied.OverBudget == "" {
		t.Fatalf("window est %d > budget %d", asm.Applied.WindowEstimate, asm.Applied.WindowBudget)
	}
}

func TestPendingToolMayExceedTotal(t *testing.T) {
	huge := contextUnit{Kind: "tool", Pending: true, Items: []windowItem{
		{Msg: map[string]any{"role": "assistant", "content": strings.Repeat("p", 200)}},
	}}
	kept, evicted := splitUnits([]contextUnit{huge}, 10)
	if len(kept) != 1 || !kept[0].Pending || len(evicted) != 0 {
		t.Fatalf("pending must stay kept=%d evicted=%d", len(kept), len(evicted))
	}
}
