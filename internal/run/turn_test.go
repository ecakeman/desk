package run

import (
	"testing"

	"desk/internal/worker"
)

func TestValidateTurnFinishAllowsFreeText(t *testing.T) {
	if err := validateTurnFinish(&worker.Out{T: "turn.finish", Text: "普通回答，不是 JSON {"}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateTurnFinishRejectsMixedTool(t *testing.T) {
	if err := validateTurnFinish(&worker.Out{T: "turn.finish", Text: "x", Name: "fs.write"}); err == nil {
		t.Fatal("expected mixed tool")
	}
	if err := validateTurnFinish(&worker.Out{T: "turn.finish", ID: "1"}); err == nil {
		t.Fatal("expected mixed id")
	}
	if err := validateTurnFinish(&worker.Out{T: "tool.request", Text: "x"}); err == nil {
		t.Fatal("expected bad t")
	}
	if err := validateTurnFinish(nil); err == nil {
		t.Fatal("expected missing")
	}
}
