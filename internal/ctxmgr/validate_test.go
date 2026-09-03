package ctxmgr

import (
	"encoding/json"
	"strings"
	"testing"

	"desk/internal/event"
)

func TestValidateResultAcceptsSignal(t *testing.T) {
	raw := []byte(`{"summary":"用户要写 STATUS.md 并完成收集","facts":[{"key":"file","value":"STATUS.md","status":"active","confidence":0.9,"source_refs":[{"run_id":"r1","seq":1}]}],"open_items":["write file"],"decisions":[]}`)
	got, err := ValidateResult(raw, []SourceRef{{RunID: "r1", Seq: 1}}, 200, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary == "" || len(got.Facts) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestValidateResultRejectsJSON(t *testing.T) {
	if _, err := ValidateResult([]byte(`not json`), []SourceRef{{RunID: "r", Seq: 1}}, 100, 0); err == nil {
		t.Fatal("expected json error")
	}
}

func TestValidateResultRejectsEmpty(t *testing.T) {
	raw := []byte(`{"summary":"Everything important is preserved.","facts":[],"open_items":[],"decisions":[]}`)
	if _, err := ValidateResult(raw, []SourceRef{{RunID: "r", Seq: 1}}, 100, 0); err == nil {
		t.Fatal("expected empty")
	}
}

func TestProvenanceRunAndSeq(t *testing.T) {
	ok := []byte(`{"summary":"保留了任务状态与文件约束足够长","facts":[{"key":"x","value":"y","status":"active","confidence":1,"source_refs":[{"run_id":"run-a","seq":12}]}],"open_items":[],"decisions":[]}`)
	allowed := []SourceRef{{RunID: "run-a", Seq: 12}, {RunID: "run-b", Seq: 12}}
	if _, err := ValidateResult(ok, allowed, 200, 0); err != nil {
		t.Fatal(err)
	}
	wrongRun := []byte(`{"summary":"保留了任务状态与文件约束足够长","facts":[{"key":"x","value":"y","status":"active","confidence":1,"source_refs":[{"run_id":"run-b","seq":12}]}],"open_items":[],"decisions":[]}`)
	if _, err := ValidateResult(wrongRun, []SourceRef{{RunID: "run-a", Seq: 12}}, 200, 0); err == nil {
		t.Fatal("wrong run_id")
	}
	unknownSeq := []byte(`{"summary":"保留了任务状态与文件约束足够长","facts":[{"key":"x","value":"y","status":"active","confidence":1,"source_refs":[{"run_id":"run-a","seq":99}]}],"open_items":[],"decisions":[]}`)
	if _, err := ValidateResult(unknownSeq, []SourceRef{{RunID: "run-a", Seq: 12}}, 200, 0); err == nil {
		t.Fatal("unknown seq")
	}
	unknownRun := []byte(`{"summary":"保留了任务状态与文件约束足够长","facts":[{"key":"x","value":"y","status":"active","confidence":1,"source_refs":[{"run_id":"nope","seq":12}]}],"open_items":[],"decisions":[]}`)
	if _, err := ValidateResult(unknownRun, []SourceRef{{RunID: "run-a", Seq: 12}}, 200, 0); err == nil {
		t.Fatal("unknown run")
	}
	legacy := []byte(`{"summary":"保留了任务状态与文件约束足够长","facts":[{"key":"x","value":"y","status":"active","confidence":1,"source_event_seqs":[12]}],"open_items":[],"decisions":[]}`)
	if _, err := ValidateResult(legacy, []SourceRef{{RunID: "run-a", Seq: 12}}, 200, 0); err != nil {
		t.Fatal("legacy same-run seq", err)
	}
	legacyCross := []byte(`{"summary":"保留了任务状态与文件约束足够长","facts":[{"key":"x","value":"y","status":"active","confidence":1,"source_event_seqs":[12]}],"open_items":[],"decisions":[]}`)
	if _, err := ValidateResult(legacyCross, []SourceRef{{RunID: "run-a", Seq: 12}, {RunID: "run-b", Seq: 1}}, 200, 0); err == nil {
		t.Fatal("legacy seq across runs")
	}
}

func TestValidateResultRejectsOversized(t *testing.T) {
	sum := make([]byte, 0, 80)
	for i := 0; i < 40; i++ {
		sum = append(sum, []byte("状态")...)
	}
	raw, _ := json.Marshal(map[string]any{
		"summary": string(sum), "facts": []any{}, "open_items": []any{}, "decisions": []any{},
	})
	if _, err := ValidateResult(raw, []SourceRef{{RunID: "r", Seq: 1}}, 10, 0); err == nil {
		t.Fatal("expected oversized")
	}
}

func TestSplitUnitsKeepsToolAtomic(t *testing.T) {
	asst := windowItem{Ref: SourceRef{RunID: "r", Seq: 2}, Msg: map[string]any{"role": "assistant", "tool_calls": []any{}}}
	res := windowItem{Ref: SourceRef{RunID: "r", Seq: 3}, Msg: map[string]any{"role": "tool", "content": "ok"}}
	user := contextUnit{Kind: "normal", Items: []windowItem{{Ref: SourceRef{RunID: "r", Seq: 1}, Msg: map[string]any{"role": "user", "content": "012345678901234567890123456789"}}}}
	tool := contextUnit{Kind: "tool", Items: []windowItem{asst, res}}
	budget := tool.tokens()
	kept, evicted := splitUnits([]contextUnit{user, tool}, budget)
	if len(kept) != 1 || kept[0].Kind != "tool" || len(kept[0].Items) != 2 {
		t.Fatalf("kept=%+v evicted=%+v", kept, evicted)
	}
	if evicted[0].Kind != "normal" {
		t.Fatal("should evict user unit")
	}
}

func TestSplitUnitsPendingAndOversized(t *testing.T) {
	huge := contextUnit{Kind: "tool", Items: []windowItem{
		{Msg: map[string]any{"role": "assistant", "content": stringsRepeat("a", 200)}},
		{Msg: map[string]any{"role": "tool", "content": stringsRepeat("b", 200)}},
	}}
	kept, evicted := splitUnits([]contextUnit{huge}, 10)
	if len(kept) != 0 || len(evicted) != 1 || len(evicted[0].Items) != 2 {
		t.Fatalf("oversized non-pending tool must evict whole unit kept=%d evicted=%d", len(kept), len(evicted))
	}
	pend := contextUnit{Kind: "tool", Pending: true, Items: []windowItem{{Msg: map[string]any{"role": "assistant", "content": "call"}}}}
	older := contextUnit{Kind: "normal", Items: []windowItem{{Msg: map[string]any{"role": "user", "content": stringsRepeat("z", 80)}}}}
	kept, evicted = splitUnits([]contextUnit{older, pend}, pend.tokens())
	if len(kept) == 0 || !kept[len(kept)-1].Pending {
		t.Fatal("pending dropped")
	}
	if len(evicted) != 1 {
		t.Fatalf("evicted=%d", len(evicted))
	}
}

func TestBuildUnitsToolPairPendingHistoricalMulti(t *testing.T) {
	run := "run-now"
	old := "run-old"
	events := []event.Event{
		{RunID: old, Seq: 9, Type: event.TypeToolCompleted, Payload: []byte(`{"id":"h1","name":"ping","data":{"ok":true}}`)},
		{RunID: run, Seq: 1, Type: event.TypeMessageUser, Payload: []byte(`{"text":"hi"}`)},
		{RunID: run, Seq: 2, Type: event.TypeToolRequested, Payload: []byte(`{"id":"t1","name":"ping","args":{}}`)},
		{RunID: run, Seq: 3, Type: event.TypeToolCompleted, Payload: []byte(`{"id":"t1","name":"ping","data":{}}`)},
		{RunID: run, Seq: 4, Type: event.TypeToolRequested, Payload: []byte(`{"id":"t2","name":"ping","args":{}}`)},
		{RunID: run, Seq: 5, Type: event.TypeToolCompleted, Payload: []byte(`{"id":"t2","name":"ping","data":{"n":2}}`)},
		{RunID: run, Seq: 6, Type: event.TypeToolRequested, Payload: []byte(`{"id":"pend","name":"ping","args":{}}`)},
	}
	units := parseLayers(events, run, "pend").window
	if len(units) < 4 {
		t.Fatalf("units=%d", len(units))
	}
	if units[0].Kind != "normal" || !strings.Contains(fmtString(units[0].Items[0].Msg["content"]), "[event tool.completed") {
		t.Fatalf("historical %+v", units[0])
	}
	var tools, pending int
	for _, u := range units {
		if u.Kind == "tool" {
			tools++
			if u.Pending {
				pending++
				if len(u.Items) != 1 {
					t.Fatal("pending must be assistant only")
				}
			} else if len(u.Items) != 2 {
				t.Fatalf("tool pair items=%d", len(u.Items))
			}
		}
	}
	if tools != 3 || pending != 1 {
		t.Fatalf("tools=%d pending=%d", tools, pending)
	}
	budget := units[len(units)-2].tokens() + units[len(units)-1].tokens()
	kept, evicted := splitUnits(units, budget)
	for _, u := range kept {
		if u.Kind == "tool" && !u.Pending && len(u.Items) != 2 {
			t.Fatal("split cut a tool pair")
		}
	}
	if len(evicted) == 0 {
		t.Fatal("expected older eviction")
	}
}

func TestHistoricalToolIsNormalUnit(t *testing.T) {
	u := contextUnit{Kind: "normal", Items: []windowItem{{
		Msg: map[string]any{"role": "user", "content": "[event tool.completed r:1]\n{}"},
	}}}
	if u.Kind != "normal" || len(u.Items) != 1 {
		t.Fatal("historical")
	}
}

func stringsRepeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
