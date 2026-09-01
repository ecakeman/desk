package memory

import (
	"encoding/json"
	"testing"

	"desk/internal/event"
)

func TestExtractSkillRevisedText(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"path": "memory/skills/tx.md", "diff_head": "abcd1234",
		"text": "事务与事件必须同一提交",
	})
	got, ok := extract(event.TypeSkillRevised, raw)
	if !ok || got != "事务与事件必须同一提交" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
}

func TestTokenizeHanAndSymbols(t *testing.T) {
	got := tokenize("事务 path_escaped")
	want := map[string]bool{"事": true, "务": true, "path": true, "escaped": true}
	if len(got) != 4 {
		t.Fatalf("toks=%v", got)
	}
	for _, tok := range got {
		if !want[tok] {
			t.Fatalf("unexpected %q in %v", tok, got)
		}
	}
}

func TestBM25RanksExactTermOverLongNoise(t *testing.T) {
	docs := []Hit{
		{RunID: "a", Seq: 1, Text: "the the the the the the the the"},
		{RunID: "b", Seq: 1, Text: "fs.write path_escaped jail"},
	}
	out := bm25Rank(docs, "path_escaped")
	if len(out) == 0 || out[0].RunID != "b" {
		t.Fatalf("%+v", out)
	}
}

func TestBM25RanksChinesePhrase(t *testing.T) {
	docs := []Hit{
		{RunID: "noise", Seq: 1, Text: "hello world dashboard session"},
		{RunID: "gold", Seq: 2, Text: "事务和事件必须同一提交"},
	}
	out := bm25Rank(docs, "事务和事件")
	if len(out) == 0 || out[0].RunID != "gold" {
		t.Fatalf("%+v", out)
	}
}

func TestRRFMergeDedup(t *testing.T) {
	a := []Hit{{RunID: "r", Seq: 1, Kind: "k", Text: "lex", Score: 1}}
	b := []Hit{{RunID: "r", Seq: 1, Kind: "k", Text: "vec", Score: 0.9}, {RunID: "r", Seq: 2, Kind: "k", Text: "other", Score: 0.1}}
	out := rrfMerge(a, b, 8)
	if len(out) != 2 {
		t.Fatalf("len %d", len(out))
	}
	if out[0].RunID != "r" || out[0].Seq != 1 {
		t.Fatalf("first %+v", out[0])
	}
	if out[0].Text != "lex" {
		t.Fatalf("keep first-seen text %q", out[0].Text)
	}
}
