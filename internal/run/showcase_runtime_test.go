package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"desk/internal/event"
	"desk/internal/testdb"
	"desk/internal/worker"
)

func TestRuntimeContractShowcase(t *testing.T) {
	work := t.TempDir()
	w := &recWorker{fn: showcaseScript}
	runService, db := contractEnv(t, w, work)
	sessionID := testdb.InsertSession(t, db)

	r1 := postWait(t, runService, db, sessionID, "baseline bookmark-lab", work, StatusCompleted)
	assertEventConsistency(t, db, r1)
	if !hasType(loadEvents(t, db, r1), event.TypeTaskUpdated) {
		t.Fatal("run 1 must create a task")
	}

	r2 := postWait(t, runService, db, sessionID, "change read-later", work, StatusWaitingApproval)
	if err := runService.Decide(context.Background(), r2, requestedSeq(t, db, r2), true); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, r2, StatusCompleted)
	assertEventConsistency(t, db, r2)
	b, err := os.ReadFile(filepath.Join(work, "notes.md"))
	if err != nil || !strings.Contains(string(b), "read-later") {
		t.Fatalf("notes.md=%q err=%v", b, err)
	}

	r3 := postWait(t, runService, db, sessionID, "history kebab-case", work, StatusCompleted)
	assertEventConsistency(t, db, r3)
	if !hasType(loadEvents(t, db, r3), event.TypeToolCompleted) {
		t.Fatal("run 3 must complete memory.search")
	}
	if !showcaseMemoryHit(t, loadEvents(t, db, r3), "bookmark-lab") {
		t.Fatal("run 3 must retrieve prior session facts")
	}

	r4 := postWait(t, runService, db, sessionID, "closeout", work, StatusCompleted)
	assertEventConsistency(t, db, r4)

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM runs WHERE session_id=$1`, sessionID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("runs=%d want 4", n)
	}
	for _, id := range []string{r1, r2, r3, r4} {
		var st string
		if err := db.QueryRow(`SELECT status FROM runs WHERE id=$1`, id).Scan(&st); err != nil {
			t.Fatal(err)
		}
		if !Terminal(st) {
			t.Fatalf("run %s status=%s", id, st)
		}
	}

	msgs, err := runService.Events.Messages(context.Background(), sessionID, r4)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) < 2 {
		t.Fatalf("session STM too small: %d", len(msgs))
	}
	hits := showcaseHitCount(t, loadEvents(t, db, r3))
	consistent := true
	for _, id := range []string{r1, r2, r3, r4} {
		evs := loadEvents(t, db, id)
		proj, err := projectRunStatus(evs)
		if err != nil || proj != runStatus(t, db, id) {
			consistent = false
		}
	}
	taskN := countType(loadEvents(t, db, r1), event.TypeTaskUpdated)
	report(t, "4-run showcase",
		"runs", "4/4",
		"terminal_runs", "4/4",
		"session_continuity", fmt.Sprintf("%t", len(msgs) >= 2),
		"task_continuity", fmt.Sprintf("%t", taskN > 0),
		"memory_hits", fmt.Sprintf("%d", hits),
		"event_consistency", fmt.Sprintf("%t", consistent),
		"approval", "allow+write",
	)
}

func showcaseScript(in worker.In) *worker.Out {
	text := lastUser(in)
	switch {
	case in.T == "turn.start" && strings.Contains(text, "baseline"):
		return &worker.Out{
			T: "tool.request", ID: "t1", Name: "task.update",
			Args: map[string]any{"title": "bookmark-lab baseline", "status": "open"},
		}
	case in.T == "turn.start" && strings.Contains(text, "change"):
		return &worker.Out{
			T: "tool.request", ID: "w1", Name: "fs.write",
			Args: map[string]any{"path": "notes.md", "content": "read-later flag"},
		}
	case in.T == "turn.start" && strings.Contains(text, "history"):
		return &worker.Out{
			T: "tool.request", ID: "m1", Name: "memory.search",
			Args: map[string]any{"query": "bookmark-lab"},
		}
	default:
		return &worker.Out{T: "turn.finish", Text: "ok"}
	}
}

func showcaseMemoryHit(t *testing.T, events []event.Event, needle string) bool {
	t.Helper()
	for _, e := range events {
		if e.Type != event.TypeToolCompleted {
			continue
		}
		var p struct {
			Name string          `json:"name"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p.Name != "memory.search" {
			continue
		}
		return strings.Contains(string(p.Data), needle)
	}
	return false
}

func showcaseHitCount(t *testing.T, events []event.Event) int {
	t.Helper()
	for _, e := range events {
		if e.Type != event.TypeToolCompleted {
			continue
		}
		var p struct {
			Name string          `json:"name"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p.Name != "memory.search" {
			continue
		}
		var body struct {
			Hits []json.RawMessage `json:"hits"`
		}
		if err := json.Unmarshal(p.Data, &body); err != nil {
			t.Fatal(err)
		}
		return len(body.Hits)
	}
	return 0
}
