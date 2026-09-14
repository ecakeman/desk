package ctxmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"desk/internal/event"
	"desk/internal/ids"
)

func logMeasurement(t *testing.T, scenario string, payload map[string]any) {
	t.Helper()
	payload["scenario"] = scenario
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("::measurement::%s\n", b)
	t.Logf("::measurement::%s", b)
}

func refsKey(refs []SourceRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, fmt.Sprintf("%s#%d", r.RunID, r.Seq))
	}
	return out
}

func loadEvictedPayloads(t *testing.T, ev *event.Store, runID string) []SourceRef {
	t.Helper()
	events, err := ev.ListAfter(context.Background(), runID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []SourceRef
	seen := map[string]bool{}
	for _, e := range events {
		if e.Type != event.TypeContextEvicted {
			continue
		}
		var p EvictPayload
		if json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		for _, r := range p.BasedOn {
			k := fmt.Sprintf("%s#%d", r.RunID, r.Seq)
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, r)
		}
	}
	return out
}

func loadSmallBasedOn(t *testing.T, ev *event.Store, runID string) []SourceRef {
	t.Helper()
	events, err := ev.ListAfter(context.Background(), runID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []SourceRef
	for _, e := range events {
		if e.Type != event.TypeContextSmallCompact {
			continue
		}
		var p CompactPayload
		if json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		out = append(out, p.BasedOn...)
	}
	return out
}

func countType(t *testing.T, ev *event.Store, runID, typ string) int {
	t.Helper()
	var n int
	if err := ev.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`, runID, typ).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func windowContents(contextAssembly Assembly) []string {
	var out []string
	for _, m := range contextAssembly.Layers.Window {
		out = append(out, fmtString(m["content"]))
	}
	return out
}

func TestInvariantFinalEstimateLeqTotal(t *testing.T) {
	m, ev, sessionID, runID := testMgr(t, 100000, &StubCompactor{Err: context.Canceled})
	m.Settings.TotalTokens = 80
	m.Settings.SmallTriggerTok = 1_000_000
	for i := 0; i < 10; i++ {
		appendUser(t, ev, runID, strings.Repeat("inv1-", 10)+ids.New())
	}
	contextAssembly, err := m.Prepare(context.Background(), PrepareIn{SessionID: sessionID, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	got := EstimateLLMInput("", nil, contextAssembly.Messages, "")
	if got > m.Settings.TotalTokens && contextAssembly.Applied.OverBudget != "pending_tool" {
		t.Fatalf("est %d > total %d", got, m.Settings.TotalTokens)
	}
}

func TestInvariantEvictedNeverResurrects(t *testing.T) {
	m, ev, sessionID, runID := testMgr(t, 100000, &StubCompactor{Err: context.Canceled})
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
	a1, err := m.Prepare(context.Background(), PrepareIn{SessionID: sessionID, RunID: runID})
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
	a2, err := m.Prepare(context.Background(), PrepareIn{SessionID: sessionID, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range windowContents(a2) {
		if strings.Contains(c, first) {
			t.Fatal("evicted history resurrected")
		}
	}
}

func TestEvictedBufferVisibleBeforeSmallCompact(t *testing.T) {
	m, ev, sessionID, runID := testMgr(t, 40, &StubCompactor{Err: context.Canceled})
	m.Settings.TotalTokens = 100000
	m.Settings.EvictedBufferTokens = 100000
	m.Settings.SmallTriggerTok = 1_000_000
	var first string
	for i := 0; i < 8; i++ {
		text := "buf-" + ids.New() + strings.Repeat(" y", 8)
		if i == 0 {
			first = text
		}
		appendUser(t, ev, runID, text)
	}
	contextAssembly, err := m.Prepare(context.Background(), PrepareIn{SessionID: sessionID, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	if countType(t, ev, runID, event.TypeContextEvicted) < 1 {
		t.Fatal("expected durable eviction")
	}
	if countType(t, ev, runID, event.TypeContextSmallCompact) != 0 {
		t.Fatal("small compact should not run")
	}
	inWindow := false
	for _, c := range windowContents(contextAssembly) {
		if strings.Contains(c, first) {
			inWindow = true
		}
	}
	if inWindow {
		t.Fatal("oldest should have left window")
	}
	inBuffer := false
	inMessages := false
	for _, msg := range contextAssembly.Layers.Evicted {
		if strings.Contains(fmtString(msg["content"]), first) {
			inBuffer = true
		}
	}
	for _, msg := range contextAssembly.Messages {
		if strings.Contains(fmtString(msg["content"]), first) {
			inMessages = true
		}
	}
	if !inBuffer || !inMessages {
		t.Fatalf("evicted buffer missing oldest buffer=%v messages=%v", inBuffer, inMessages)
	}
	got := EstimateLLMInput("", nil, contextAssembly.Messages, "")
	if got > m.Settings.TotalTokens && contextAssembly.Applied.OverBudget != "pending_tool" {
		t.Fatalf("buffer must count in total est %d > %d", got, m.Settings.TotalTokens)
	}
	evicted := loadEvictedPayloads(t, ev, runID)
	logMeasurement(t, "eviction_before_small_compact", map[string]any{
		"WindowTokens":        m.Settings.WindowTokens,
		"TotalTokens":         m.Settings.TotalTokens,
		"EvictedBufferTokens": m.Settings.EvictedBufferTokens,
		"SmallTriggerTok":     m.Settings.SmallTriggerTok,
		"estimated_tokens":    got,
		"evicted_refs":        refsKey(evicted),
		"pending_evicted":     refsKey(contextAssembly.Applied.PendingEvicted),
		"buffer_in_assembly":  inMessages,
		"small_compact_count": countType(t, ev, runID, event.TypeContextSmallCompact),
	})
}

func TestEvictedBufferRespectsIndependentCap(t *testing.T) {
	m, ev, sessionID, runID := testMgr(t, 40, &StubCompactor{Err: context.Canceled})
	m.Settings.TotalTokens = 100000
	m.Settings.EvictedBufferTokens = 8
	m.Settings.SmallTriggerTok = 1_000_000
	first := "cap-oldest-" + strings.Repeat("z", 40)
	appendUser(t, ev, runID, first)
	for i := 0; i < 6; i++ {
		appendUser(t, ev, runID, strings.Repeat("cap-new-", 8)+ids.New())
	}
	contextAssembly, err := m.Prepare(context.Background(), PrepareIn{SessionID: sessionID, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	if countType(t, ev, runID, event.TypeContextEvicted) < 1 {
		t.Fatal("expected eviction")
	}
	droppedOldest := true
	for _, msg := range contextAssembly.Layers.Evicted {
		if strings.Contains(fmtString(msg["content"]), "cap-oldest-") {
			droppedOldest = false
			t.Fatal("independent cap should drop oldest evicted from buffer")
		}
	}
	got := EstimateLLMInput("", nil, contextAssembly.Messages, "")
	logMeasurement(t, "evicted_buffer_independent_cap", map[string]any{
		"WindowTokens":        m.Settings.WindowTokens,
		"TotalTokens":         m.Settings.TotalTokens,
		"EvictedBufferTokens": m.Settings.EvictedBufferTokens,
		"estimated_tokens":    got,
		"evicted_refs":        refsKey(loadEvictedPayloads(t, ev, runID)),
		"dropped_oldest":      droppedOldest,
		"buffer_msgs":         len(contextAssembly.Layers.Evicted),
	})
}

func TestInvariantSmallFailNoRetryUntilNewEvict(t *testing.T) {
	fail := &StubCompactor{Raw: []byte(`not-json`)}
	m, ev, sessionID, runID := testMgr(t, 20, fail)
	for i := 0; i < 8; i++ {
		appendUser(t, ev, runID, strings.Repeat("retry-pad-", 8)+ids.New())
	}
	if _, err := m.Prepare(context.Background(), PrepareIn{SessionID: sessionID, RunID: runID}); err != nil {
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
	if _, err := m.Prepare(context.Background(), PrepareIn{SessionID: sessionID, RunID: runID}); err != nil {
		t.Fatal(err)
	}
	if fail.N != n1 {
		t.Fatalf("retried compact without new eviction N %d -> %d", n1, fail.N)
	}
	okJSON := []byte(`{"summary":"保留了任务状态与文件约束足够长","facts":[{"key":"k","value":"v","status":"active","confidence":0.9,"source_event_seqs":[1]}],"open_items":[],"decisions":[]}`)
	fail.Raw = okJSON
	fail.Err = nil
	appendUser(t, ev, runID, strings.Repeat("new-evict-", 10)+ids.New())
	if _, err := m.Prepare(context.Background(), PrepareIn{SessionID: sessionID, RunID: runID}); err != nil {
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
	m, ev, sessionID, runID := testMgr(t, 15, stub)
	m.Settings.LargeSmallCount = 2
	m.Settings.SmallTriggerTok = 1
	for i := 0; i < 12; i++ {
		appendUser(t, ev, runID, strings.Repeat("large-payload-", 10)+ids.New())
	}
	if _, err := m.Prepare(context.Background(), PrepareIn{SessionID: sessionID, RunID: runID}); err != nil {
		t.Fatal(err)
	}
	stub.Raw = smallJSON(2)
	for i := 0; i < 12; i++ {
		appendUser(t, ev, runID, strings.Repeat("large-payload-b-", 10)+ids.New())
	}
	contextAssembly, err := m.Prepare(context.Background(), PrepareIn{SessionID: sessionID, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	if len(contextAssembly.Layers.Large) > 1 {
		t.Fatalf("active large %d", len(contextAssembly.Layers.Large))
	}
	events, err := ev.ListBySession(context.Background(), sessionID)
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
	m, ev, sessionID, runID := testMgr(t, 100000, &StubCompactor{Err: context.Canceled})
	appendUser(t, ev, runID, "prefix-user")
	a1, err := m.Prepare(context.Background(), PrepareIn{SessionID: sessionID, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	a2, err := m.Prepare(context.Background(), PrepareIn{
		SessionID: sessionID, RunID: runID,
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
	m, ev, sessionID, runID := testMgr(t, 100000, stub)
	appendUser(t, ev, runID, "inspect-skip-llm")
	contextAssembly, err := m.Prepare(context.Background(), PrepareIn{SessionID: sessionID, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	n := stub.N
	ctx := context.Background()
	tx, err := ev.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ev.Append(ctx, tx, runID, event.TypeContextApplied, contextAssembly.Applied); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()
	m.Forget(runID)
	if _, src, ok := m.Inspect(ctx, sessionID, runID); !ok || src != "reconstructable" {
		t.Fatalf("src=%s ok=%v", src, ok)
	}
	if stub.N != n {
		t.Fatalf("reconstruct invoked compact N %d -> %d", n, stub.N)
	}
}

func TestWindowBudgetUsesRemainingWhenTotalSmaller(t *testing.T) {
	m, ev, sessionID, runID := testMgr(t, 4000, &StubCompactor{Err: context.Canceled})
	m.Settings.TotalTokens = 50
	m.Settings.SmallTriggerTok = 1_000_000
	for i := 0; i < 6; i++ {
		appendUser(t, ev, runID, strings.Repeat("win-", 12)+ids.New())
	}
	contextAssembly, err := m.Prepare(context.Background(), PrepareIn{SessionID: sessionID, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	if contextAssembly.Applied.WindowBudget > m.Settings.TotalTokens {
		t.Fatalf("window_budget %d > total", contextAssembly.Applied.WindowBudget)
	}
	if contextAssembly.Applied.WindowEstimate > contextAssembly.Applied.WindowBudget && contextAssembly.Applied.OverBudget == "" {
		t.Fatalf("window est %d > budget %d", contextAssembly.Applied.WindowEstimate, contextAssembly.Applied.WindowBudget)
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

func TestEvictedBufferRefLineageAndSmallAbsorb(t *testing.T) {
	ok := []byte(`{"summary":"会话要维护书签规则与状态文件足够长","facts":[{"key":"goal","value":"write STATUS","status":"active","confidence":0.9,"source_event_seqs":[1]}],"open_items":["STATUS.md"],"decisions":[]}`)
	stub := &StubCompactor{Raw: ok}
	m, ev, sessionID, runID := testMgr(t, 20, stub)
	m.Settings.TotalTokens = 200000
	m.Settings.EvictedBufferTokens = 200000
	m.Settings.SmallTriggerTok = 1
	for i := 0; i < 8; i++ {
		appendUser(t, ev, runID, strings.Repeat("lineage-payload-", 8)+ids.New())
	}
	a1, err := m.Prepare(context.Background(), PrepareIn{SessionID: sessionID, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	evicted := loadEvictedPayloads(t, ev, runID)
	if len(evicted) < 1 {
		t.Fatal("expected evicted refs")
	}
	pending := a1.Applied.PendingEvicted
	if countType(t, ev, runID, event.TypeContextSmallCompact) < 1 && len(pending) == 0 {
		t.Fatal("expected pending evicted or small compact")
	}
	based := loadSmallBasedOn(t, ev, runID)
	if len(based) < 1 {
		t.Fatal("small compact must record based_on refs")
	}
	got := EstimateLLMInput("", nil, a1.Messages, "")
	absorbed := map[string]bool{}
	for _, r := range based {
		absorbed[fmt.Sprintf("%s#%d", r.RunID, r.Seq)] = true
	}
	overlap := 0
	for _, r := range evicted {
		if absorbed[fmt.Sprintf("%s#%d", r.RunID, r.Seq)] {
			overlap++
		}
	}
	if overlap < 1 {
		t.Fatalf("small based_on did not absorb any evicted ref evicted=%v based=%v", refsKey(evicted), refsKey(based))
	}
	a2, err := m.Prepare(context.Background(), PrepareIn{SessionID: sessionID, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	logMeasurement(t, "evicted_ref_lineage_small_absorb", map[string]any{
		"WindowTokens":        m.Settings.WindowTokens,
		"TotalTokens":         m.Settings.TotalTokens,
		"EvictedBufferTokens": m.Settings.EvictedBufferTokens,
		"SmallTriggerTok":     m.Settings.SmallTriggerTok,
		"estimated_tokens":    got,
		"evicted_refs":        refsKey(evicted),
		"buffer_refs":         refsKey(a1.Applied.PendingEvicted),
		"small_compact_refs":  refsKey(based),
		"absorbed_overlap":    overlap,
		"prepare2_pending":    refsKey(a2.Applied.PendingEvicted),
		"prepare2_smalls":     countType(t, ev, runID, event.TypeContextSmallCompact),
	})
}

func TestEvictedPrepareStableNoDuplicateAppend(t *testing.T) {
	m, ev, sessionID, runID := testMgr(t, 40, &StubCompactor{Err: context.Canceled})
	m.Settings.TotalTokens = 100000
	m.Settings.EvictedBufferTokens = 100000
	m.Settings.SmallTriggerTok = 1_000_000
	for i := 0; i < 8; i++ {
		appendUser(t, ev, runID, strings.Repeat("dup-pad-", 8)+ids.New())
	}
	a1, err := m.Prepare(context.Background(), PrepareIn{SessionID: sessionID, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	n1 := countType(t, ev, runID, event.TypeContextEvicted)
	p1 := refsKey(a1.Applied.PendingEvicted)
	a2, err := m.Prepare(context.Background(), PrepareIn{SessionID: sessionID, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	n2 := countType(t, ev, runID, event.TypeContextEvicted)
	p2 := refsKey(a2.Applied.PendingEvicted)
	if n2 != n1 {
		t.Fatalf("second prepare rewrote eviction %d -> %d", n1, n2)
	}
	if len(p2) != len(p1) {
		t.Fatalf("pending refs changed %v -> %v", p1, p2)
	}
	logMeasurement(t, "prepare_stable_no_duplicate_evict", map[string]any{
		"evicted_event_count": n1,
		"pending_refs":        p1,
		"prepare2_pending":    p2,
		"estimated_tokens":    EstimateLLMInput("", nil, a2.Messages, ""),
	})
}

func TestEvictedCompactFailDoesNotResurrect(t *testing.T) {
	fail := &StubCompactor{Raw: []byte(`not-json`)}
	m, ev, sessionID, runID := testMgr(t, 20, fail)
	var first string
	for i := 0; i < 8; i++ {
		text := "fail-mark-" + ids.New() + strings.Repeat(" z", 8)
		if i == 0 {
			first = text
		}
		appendUser(t, ev, runID, text)
	}
	a1, err := m.Prepare(context.Background(), PrepareIn{SessionID: sessionID, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	if countType(t, ev, runID, event.TypeContextSmallCompact) != 0 {
		t.Fatal("compact must not write on invalid json")
	}
	for _, c := range windowContents(a1) {
		if strings.Contains(c, first) {
			t.Fatal("compact fail resurrected oldest into window")
		}
	}
	a2, err := m.Prepare(context.Background(), PrepareIn{SessionID: sessionID, RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range windowContents(a2) {
		if strings.Contains(c, first) {
			t.Fatal("second prepare resurrected evicted text")
		}
	}
	logMeasurement(t, "compact_fail_no_resurrect", map[string]any{
		"small_compact_count": countType(t, ev, runID, event.TypeContextSmallCompact),
		"compact_failed":      countType(t, ev, runID, event.TypeContextCompactFailed),
		"evicted_refs":        refsKey(loadEvictedPayloads(t, ev, runID)),
		"compact_attempts":    fail.N,
	})
}
