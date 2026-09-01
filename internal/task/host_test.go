package task

import (
	"context"
	"strings"
	"testing"

	"desk/internal/plugin"
)

func TestHostRejectsBadSkillRef(t *testing.T) {
	h := NewHost(nil, nil)
	ctx := plugin.WithPhase(plugin.WithRunID(context.Background(), "run-1"), "review")
	_, err := h.Exec(ctx, "update", map[string]any{
		"title":     "index events",
		"status":    "done",
		"skill_ref": "notes.md@not-a-hash",
	})
	if err == nil || !strings.Contains(err.Error(), "bad_skill_ref") {
		t.Fatalf("got %v", err)
	}
}
