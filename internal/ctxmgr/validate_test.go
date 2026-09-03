package ctxmgr

import (
	"encoding/json"
	"testing"
)

func TestValidateResultAcceptsSignal(t *testing.T) {
	raw := []byte(`{"summary":"用户要写 STATUS.md 并完成收集","facts":[{"key":"file","value":"STATUS.md","status":"active","confidence":0.9,"source_event_seqs":[1]}],"open_items":["write file"],"decisions":[]}`)
	got, err := ValidateResult(raw, map[int]bool{1: true}, 200)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary == "" || len(got.Facts) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestValidateResultRejectsJSON(t *testing.T) {
	if _, err := ValidateResult([]byte(`not json`), map[int]bool{1: true}, 100); err == nil {
		t.Fatal("expected json error")
	}
}

func TestValidateResultRejectsEmpty(t *testing.T) {
	raw := []byte(`{"summary":"Everything important is preserved.","facts":[],"open_items":[],"decisions":[]}`)
	if _, err := ValidateResult(raw, map[int]bool{1: true}, 100); err == nil {
		t.Fatal("expected empty")
	}
}

func TestValidateResultRejectsProvenance(t *testing.T) {
	raw := []byte(`{"summary":"保留了任务状态与文件约束","facts":[{"key":"x","value":"y","status":"active","confidence":1,"source_event_seqs":[999]}],"open_items":[],"decisions":[]}`)
	if _, err := ValidateResult(raw, map[int]bool{1: true}, 100); err == nil {
		t.Fatal("expected provenance")
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
	if _, err := ValidateResult(raw, map[int]bool{1: true}, 10); err == nil {
		t.Fatal("expected oversized")
	}
}

func TestSplitWindowEvictsOldest(t *testing.T) {
	var items []windowItem
	for i := 0; i < 5; i++ {
		items = append(items, windowItem{
			Ref: SourceRef{RunID: "r", Seq: i + 1},
			Msg: map[string]any{"role": "user", "content": "0123456789"},
		})
	}
	budget := EstimateTokens("user\n0123456789") * 2
	kept, evicted := splitWindow(items, budget)
	if len(evicted) == 0 || len(kept) == 0 {
		t.Fatalf("kept=%d evicted=%d", len(kept), len(evicted))
	}
	if evicted[0].Ref.Seq != 1 {
		t.Fatalf("oldest first: %+v", evicted[0])
	}
	under, none := splitWindow(items[:1], 100000)
	if len(none) != 0 || len(under) != 1 {
		t.Fatalf("under limit")
	}
}
