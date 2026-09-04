package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"desk/internal/event"
	"desk/internal/plugin"
	"desk/internal/testdb"
	"desk/internal/worker"
)

func TestRuntimeContractLifecycle(t *testing.T) {
	w := &recWorker{fn: func(in worker.In) *worker.Out {
		return &worker.Out{T: "turn.finish", Text: "done"}
	}}
	work := t.TempDir()
	runService, db := contractEnv(t, w, work)
	sessionID := testdb.InsertSession(t, db)
	runID := postWait(t, runService, db, sessionID, "hello", work, StatusCompleted)
	events := loadEvents(t, db, runID)
	if !hasType(events, event.TypeRunCreated) || !hasType(events, event.TypeMessageUser) {
		t.Fatalf("missing start events: %v", typesOf(events))
	}
	if !hasType(events, event.TypeRunCompleted) {
		t.Fatalf("missing run.completed: %v", typesOf(events))
	}
	if hasType(events, event.TypeToolRequested) {
		t.Fatalf("unexpected tool: %v", typesOf(events))
	}
	assertEventConsistency(t, db, runID)
	asks := w.snapshot()
	if len(asks) == 0 || asks[0].Phase != "plan" || asks[0].Model != "pro" {
		t.Fatalf("first ask=%+v", asks)
	}
	report(t, "run lifecycle",
		"initial_status", StatusRunning,
		"events_count", fmt.Sprintf("%d", len(events)),
		"final_status", runStatus(t, db, runID),
		"terminal_event", terminalEvent(events),
	)
}

func TestRuntimeContractToolLifecycle(t *testing.T) {
	w := &recWorker{fn: func(in worker.In) *worker.Out {
		if in.T == "turn.start" {
			return &worker.Out{T: "tool.request", ID: "c1", Name: "ping.ok", Args: map[string]any{}}
		}
		return &worker.Out{T: "turn.finish", Text: "used ping"}
	}}
	work := t.TempDir()
	runService, db := contractEnv(t, w, work)
	sessionID := testdb.InsertSession(t, db)
	runID := postWait(t, runService, db, sessionID, "ping", work, StatusCompleted)
	events := loadEvents(t, db, runID)
	if !hasType(events, event.TypeToolRequested) || !hasType(events, event.TypeToolStarted) || !hasType(events, event.TypeToolCompleted) {
		t.Fatalf("incomplete tool chain: %v", typesOf(events))
	}
	if hasType(events, event.TypeToolDenied) || hasType(events, event.TypeToolFailed) {
		t.Fatalf("unexpected deny/fail: %v", typesOf(events))
	}
	req, started, done := firstSeq(events, event.TypeToolRequested), firstSeq(events, event.TypeToolStarted), firstSeq(events, event.TypeToolCompleted)
	if !(req < started && started < done) {
		t.Fatalf("order requested=%d started=%d completed=%d", req, started, done)
	}
	assertEventConsistency(t, db, runID)
	report(t, "tool lifecycle",
		"tool_requested", fmt.Sprintf("%d", countType(events, event.TypeToolRequested)),
		"tool_started", fmt.Sprintf("%d", countType(events, event.TypeToolStarted)),
		"tool_completed", fmt.Sprintf("%d", countType(events, event.TypeToolCompleted)),
		"seq_order", fmt.Sprintf("%d < %d < %d", req, started, done),
		"final_status", runStatus(t, db, runID),
	)
}

type boomPlugin struct{}

func (boomPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name: "boom",
		Risk: "read",
		Ops:  []plugin.OpSpec{{Name: "now", Description: "fail", Parameters: json.RawMessage(`{}`)}},
	}
}

func (boomPlugin) Exec(context.Context, string, map[string]any) (json.RawMessage, error) {
	return nil, os.ErrNotExist
}

func TestRuntimeContractToolFailure(t *testing.T) {
	w := &recWorker{fn: func(in worker.In) *worker.Out {
		if in.T == "turn.start" {
			return &worker.Out{T: "tool.request", ID: "b1", Name: "boom.now", Args: map[string]any{}}
		}
		if in.T == "tool.result" && !in.OK {
			return &worker.Out{T: "turn.fail", Error: "tool_failed"}
		}
		return &worker.Out{T: "turn.finish", Text: "should-not"}
	}}
	work := t.TempDir()
	runService, db := contractEnv(t, w, work)
	runService.Plugins.Put(boomPlugin{})
	sessionID := testdb.InsertSession(t, db)
	runID := postWait(t, runService, db, sessionID, "explode", work, StatusFailed)
	events := loadEvents(t, db, runID)
	if !hasType(events, event.TypeToolRequested) || !hasType(events, event.TypeToolStarted) || !hasType(events, event.TypeToolFailed) {
		t.Fatalf("incomplete fail chain: %v", typesOf(events))
	}
	if hasType(events, event.TypeToolCompleted) || hasType(events, event.TypeRunCompleted) {
		t.Fatalf("must not complete: %v", typesOf(events))
	}
	if !hasType(events, event.TypeRunFailed) {
		t.Fatalf("missing run.failed: %v", typesOf(events))
	}
	assertEventConsistency(t, db, runID)
	entries, _ := os.ReadDir(work)
	report(t, "tool failure",
		"tool_requested", fmt.Sprintf("%d", countType(events, event.TypeToolRequested)),
		"tool_started", fmt.Sprintf("%d", countType(events, event.TypeToolStarted)),
		"tool_failed", fmt.Sprintf("%d", countType(events, event.TypeToolFailed)),
		"tool_completed", fmt.Sprintf("%d", countType(events, event.TypeToolCompleted)),
		"final_status", runStatus(t, db, runID),
		"side_effect_count", fmt.Sprintf("%d", len(entries)),
	)
}

func TestRuntimeContractApprovalReject(t *testing.T) {
	work := t.TempDir()
	w := &recWorker{fn: func(in worker.In) *worker.Out {
		if in.T == "turn.start" {
			return &worker.Out{
				T: "tool.request", ID: "w1", Name: "fs.write",
				Args: map[string]any{"path": "secret.txt", "content": "nope"},
			}
		}
		return &worker.Out{T: "turn.finish", Text: "denied"}
	}}
	runService, db := contractEnv(t, w, work)
	sessionID := testdb.InsertSession(t, db)
	runID := postWait(t, runService, db, sessionID, "write", work, StatusWaitingApproval)
	assertEventConsistency(t, db, runID)
	seq := requestedSeq(t, db, runID)
	if err := runService.Decide(context.Background(), runID, seq, false); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runID, StatusCompleted)
	if _, err := os.Stat(filepath.Join(work, "secret.txt")); !os.IsNotExist(err) {
		t.Fatal("write must not happen after reject")
	}
	events := loadEvents(t, db, runID)
	if !hasType(events, event.TypeToolDenied) {
		t.Fatal("missing tool.denied")
	}
	if hasType(events, event.TypeToolStarted) || hasType(events, event.TypeToolCompleted) {
		t.Fatalf("forged execution: %v", typesOf(events))
	}
	assertEventConsistency(t, db, runID)
	report(t, "approval reject",
		"request_seq", fmt.Sprintf("%d", seq),
		"waiting_status", StatusWaitingApproval,
		"decision", "reject",
		"tool_started", fmt.Sprintf("%d", countType(events, event.TypeToolStarted)),
		"tool_completed", fmt.Sprintf("%d", countType(events, event.TypeToolCompleted)),
		"side_effect", fmt.Sprintf("%t", fileExists(filepath.Join(work, "secret.txt"))),
		"final_status", runStatus(t, db, runID),
	)
}

func TestRuntimeContractApprovalAllow(t *testing.T) {
	work := t.TempDir()
	w := &recWorker{fn: func(in worker.In) *worker.Out {
		if in.T == "turn.start" {
			return &worker.Out{
				T: "tool.request", ID: "w1", Name: "fs.write",
				Args: map[string]any{"path": "ok.txt", "content": "yes"},
			}
		}
		return &worker.Out{T: "turn.finish", Text: "wrote"}
	}}
	runService, db := contractEnv(t, w, work)
	sessionID := testdb.InsertSession(t, db)
	runID := postWait(t, runService, db, sessionID, "write", work, StatusWaitingApproval)
	seq := requestedSeq(t, db, runID)
	if err := runService.Decide(context.Background(), runID, seq, true); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runID, StatusCompleted)
	b, err := os.ReadFile(filepath.Join(work, "ok.txt"))
	if err != nil || string(b) != "yes" {
		t.Fatalf("file=%s err=%v", b, err)
	}
	events := loadEvents(t, db, runID)
	if !hasType(events, event.TypeToolStarted) || !hasType(events, event.TypeToolCompleted) {
		t.Fatalf("missing execution events: %v", typesOf(events))
	}
	assertEventConsistency(t, db, runID)
	report(t, "approval allow",
		"request_seq", fmt.Sprintf("%d", seq),
		"decision", "allow",
		"tool_started", fmt.Sprintf("%d", countType(events, event.TypeToolStarted)),
		"tool_completed", fmt.Sprintf("%d", countType(events, event.TypeToolCompleted)),
		"side_effect", fmt.Sprintf("%t", string(b) == "yes"),
		"final_status", runStatus(t, db, runID),
	)
}

func TestRuntimeContractCancel(t *testing.T) {
	work := t.TempDir()
	w := &recWorker{fn: func(in worker.In) *worker.Out {
		if in.T == "turn.start" {
			return &worker.Out{
				T: "tool.request", ID: "w1", Name: "fs.write",
				Args: map[string]any{"path": "late.txt", "content": "late"},
			}
		}
		return &worker.Out{T: "turn.finish", Text: "should-not"}
	}}
	runService, db := contractEnv(t, w, work)
	sessionID := testdb.InsertSession(t, db)
	runID := postWait(t, runService, db, sessionID, "write", work, StatusWaitingApproval)
	before := loadEvents(t, db, runID)
	cancelFrom := runStatus(t, db, runID)
	maxBefore := 0
	if len(before) > 0 {
		maxBefore = before[len(before)-1].Seq
	}
	if err := runService.Cancel(runID); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runID, StatusInterrupted)
	if err := runService.Cancel(runID); err != ErrNotWaiting {
		t.Fatalf("duplicate cancel: %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "late.txt")); !os.IsNotExist(err) {
		t.Fatal("cancel must not write")
	}
	events := loadEvents(t, db, runID)
	if hasType(events, event.TypeRunCompleted) || hasType(events, event.TypeToolCompleted) {
		t.Fatalf("illegal complete after cancel: %v", typesOf(events))
	}
	if !hasType(events, event.TypeRunInterrupted) {
		t.Fatal("missing run.interrupted")
	}
	assertEventConsistency(t, db, runID)
	after := 0
	for _, e := range events {
		if e.Seq > maxBefore {
			after++
		}
	}
	report(t, "cancellation",
		"initial_status", cancelFrom,
		"cancel_event", event.TypeRunInterrupted,
		"final_status", runStatus(t, db, runID),
		"events_after_cancel", fmt.Sprintf("%d", after),
		"side_effects_after_cancel", fmt.Sprintf("%t", fileExists(filepath.Join(work, "late.txt"))),
	)
}

func TestRuntimeContractEventConsistency(t *testing.T) {
	work := t.TempDir()
	runService, db := contractEnv(t, &recWorker{}, work)
	sessionID := testdb.InsertSession(t, db)

	completed := postWait(t, runService, db, sessionID, "plain", work, StatusCompleted)
	assertEventConsistency(t, db, completed)

	runService.Worker = &recWorker{fn: func(in worker.In) *worker.Out {
		if in.T == "turn.start" {
			return &worker.Out{
				T: "tool.request", ID: "w1", Name: "fs.write",
				Args: map[string]any{"path": "wait.txt", "content": "x"},
			}
		}
		return &worker.Out{T: "turn.finish", Text: "ok"}
	}}
	waiting := postWait(t, runService, db, sessionID, "wait", work, StatusWaitingApproval)
	assertEventConsistency(t, db, waiting)

	runService.Worker = crashStub{}
	failed := postWait(t, runService, db, sessionID, "crash", work, StatusFailed)
	assertEventConsistency(t, db, failed)

	runService.Worker = sleepStub{}
	runID, err := runService.PostUserMessage(context.Background(), sessionID, "sleep", work)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runID, StatusRunning)
	if err := runService.Cancel(runID); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runID, StatusInterrupted)
	assertEventConsistency(t, db, runID)

	ids := []string{completed, waiting, failed, runID}
	matched := 0
	eventCount := 0
	maxSeq := 0
	unique, ordered := true, true
	for _, id := range ids {
		evs := loadEvents(t, db, id)
		eventCount += len(evs)
		seen := map[int]bool{}
		for i, e := range evs {
			if e.Seq > maxSeq {
				maxSeq = e.Seq
			}
			if seen[e.Seq] {
				unique = false
			}
			seen[e.Seq] = true
			if e.Seq != i+1 {
				ordered = false
			}
		}
		proj, err := projectRunStatus(evs)
		if err != nil || proj != runStatus(t, db, id) {
			continue
		}
		matched++
	}
	if matched != 4 || !unique || !ordered {
		t.Fatalf("consistency matched=%d unique=%v ordered=%v", matched, unique, ordered)
	}
	report(t, "event consistency",
		"event_count", fmt.Sprintf("%d", eventCount),
		"max_seq", fmt.Sprintf("%d", maxSeq),
		"stored_status==projected", fmt.Sprintf("%d/4", matched),
		"seq_unique", fmt.Sprintf("%t", unique),
		"ordering_valid", fmt.Sprintf("%t", ordered),
	)
}
