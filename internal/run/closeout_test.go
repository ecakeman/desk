package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"desk/internal/event"
	"desk/internal/ids"
	"desk/internal/testdb"
	"desk/internal/worker"
)

func TestFailDoesNotMoveCompleted(t *testing.T) {
	runService, db, _ := askEnv(t)
	ctx := context.Background()
	sessionID, runID := testdb.InsertSession(t, db), ids.New()
	if _, err := db.Exec(
		`INSERT INTO runs (id, session_id, status, workspace_dir) VALUES ($1,$2,$3,'')`,
		runID, sessionID, StatusCompleted,
	); err != nil {
		t.Fatal(err)
	}
	if err := runService.Fail(ctx, runID, "late"); err != nil {
		t.Fatal(err)
	}
	var st string
	if err := db.QueryRow(`SELECT status FROM runs WHERE id=$1`, runID).Scan(&st); err != nil {
		t.Fatal(err)
	}
	if st != StatusCompleted {
		t.Fatalf("status=%s", st)
	}
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`,
		runID, event.TypeRunFailed,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("failed events=%d", n)
	}
}

func TestRecoverWaitingApprovalIsInterruptedAndDecideFails(t *testing.T) {
	runService, db, _ := askEnv(t)
	ctx := context.Background()
	sessionID, runID := testdb.InsertSession(t, db), ids.New()
	if _, err := db.Exec(
		`INSERT INTO runs (id, session_id, status, workspace_dir) VALUES ($1,$2,$3,'')`,
		runID, sessionID, StatusWaitingApproval,
	); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	seq, err := runService.Events.Append(ctx, tx, runID, event.TypeToolRequested, map[string]any{
		"id": "1", "name": "fs.write",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := runService.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	var st string
	if err := db.QueryRow(`SELECT status FROM runs WHERE id=$1`, runID).Scan(&st); err != nil {
		t.Fatal(err)
	}
	if st != StatusInterrupted {
		t.Fatalf("status=%s", st)
	}
	if err := runService.Decide(ctx, runID, seq, true); !errors.Is(err, ErrNotWaiting) {
		t.Fatalf("decide after recover: %v", err)
	}
}

func TestTwoMessagesSameSessionAreTwoRuns(t *testing.T) {
	runService, db, work := askEnv(t)
	ctx := context.Background()
	sessionID := testdb.InsertSession(t, db)
	a, err := runService.PostUserMessage(ctx, sessionID, "one", work)
	if err != nil {
		t.Fatal(err)
	}
	b, err := runService.PostUserMessage(ctx, sessionID, "two", work)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("same run_id")
	}
	waitStatus(t, db, a, StatusWaitingApproval)
	waitStatus(t, db, b, StatusWaitingApproval)
	if err := runService.Decide(ctx, a, requestedSeq(t, db, a), false); err != nil {
		t.Fatal(err)
	}
	if err := runService.Decide(ctx, b, requestedSeq(t, db, b), false); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, a, StatusCompleted)
	waitStatus(t, db, b, StatusCompleted)
}

func TestDuplicateDecisionConflicts(t *testing.T) {
	runService, db, work := askEnv(t)
	runID := postRun(t, runService, db, work)
	seq := requestedSeq(t, db, runID)
	var first, second error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		first = runService.Decide(context.Background(), runID, seq, false)
	}()
	go func() {
		defer wg.Done()
		second = runService.Decide(context.Background(), runID, seq, false)
	}()
	wg.Wait()
	ok, conflict := 0, 0
	for _, err := range []error{first, second} {
		if err == nil {
			ok++
			continue
		}
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotWaiting) {
			conflict++
			continue
		}
		t.Fatalf("unexpected %v", err)
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("ok=%d conflict=%d first=%v second=%v", ok, conflict, first, second)
	}
	waitStatus(t, db, runID, StatusCompleted)
}

func TestCancelDuringToolLeavesInterruptedNotCompleted(t *testing.T) {
	runService, db, work := askEnv(t)
	runService.Worker = sleepStub{}
	sessionID := testdb.InsertSession(t, db)
	runID, err := runService.PostUserMessage(context.Background(), sessionID, "sleep", work)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runID, StatusRunning)
	if err := runService.Cancel(runID); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runID, StatusInterrupted)
	var completed int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`,
		runID, event.TypeRunCompleted,
	).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if completed != 0 {
		t.Fatal("interrupted run must not complete")
	}
}

func TestCancelVersusDecisionOneTerminal(t *testing.T) {
	runService, db, work := askEnv(t)
	runID := postRun(t, runService, db, work)
	seq := requestedSeq(t, db, runID)
	var decideErr, cancelErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		decideErr = runService.Decide(context.Background(), runID, seq, false)
	}()
	go func() {
		defer wg.Done()
		cancelErr = runService.Cancel(runID)
	}()
	wg.Wait()
	deadline := time.Now().Add(3 * time.Second)
	var st string
	for time.Now().Before(deadline) {
		if err := db.QueryRow(`SELECT status FROM runs WHERE id=$1`, runID).Scan(&st); err != nil {
			t.Fatal(err)
		}
		if Terminal(st) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !Terminal(st) {
		t.Fatalf("status=%s decide=%v cancel=%v", st, decideErr, cancelErr)
	}
	if st != StatusCompleted && st != StatusFailed && st != StatusInterrupted {
		t.Fatalf("status=%s", st)
	}
	var started int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`,
		runID, event.TypeToolStarted,
	).Scan(&started); err != nil {
		t.Fatal(err)
	}
	if started > 1 {
		t.Fatalf("tool.started=%d", started)
	}
}

func TestDenyThenAllowIsRejected(t *testing.T) {
	runService, db, work := askEnv(t)
	runID := postRun(t, runService, db, work)
	seq := requestedSeq(t, db, runID)
	if err := runService.Decide(context.Background(), runID, seq, false); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runID, StatusCompleted)
	err := runService.Decide(context.Background(), runID, seq, true)
	if !errors.Is(err, ErrNotWaiting) && !errors.Is(err, ErrConflict) {
		t.Fatalf("second decide: %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "d12.txt")); !os.IsNotExist(err) {
		t.Fatal("file exists")
	}
	var started int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`,
		runID, event.TypeToolStarted,
	).Scan(&started); err != nil {
		t.Fatal(err)
	}
	if started != 0 {
		t.Fatalf("tool.started=%d", started)
	}
}

func TestTerminalDecideAndCancelAreNoops(t *testing.T) {
	runService, db, _ := askEnv(t)
	ctx := context.Background()
	for _, st := range []string{StatusCompleted, StatusFailed, StatusInterrupted} {
		sessionID, runID := testdb.InsertSession(t, db), ids.New()
		if _, err := db.Exec(
			`INSERT INTO runs (id, session_id, status, workspace_dir) VALUES ($1,$2,$3,'')`,
			runID, sessionID, st,
		); err != nil {
			t.Fatal(err)
		}
		if err := runService.Decide(ctx, runID, 1, true); !errors.Is(err, ErrNotWaiting) {
			t.Fatalf("status=%s decide=%v", st, err)
		}
		if err := runService.Cancel(runID); !errors.Is(err, ErrNotWaiting) {
			t.Fatalf("status=%s cancel=%v", st, err)
		}
		var started int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`,
			runID, event.TypeToolStarted,
		).Scan(&started); err != nil {
			t.Fatal(err)
		}
		if started != 0 {
			t.Fatalf("status=%s tool.started=%d", st, started)
		}
	}
}

func TestCancelDuringApprovalInterrupts(t *testing.T) {
	runService, db, work := askEnv(t)
	runID := postRun(t, runService, db, work)
	seq := requestedSeq(t, db, runID)
	if err := runService.Cancel(runID); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runID, StatusInterrupted)
	if err := runService.Decide(context.Background(), runID, seq, true); !errors.Is(err, ErrNotWaiting) {
		t.Fatalf("decide after cancel: %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "d12.txt")); !os.IsNotExist(err) {
		t.Fatal("file exists")
	}
	var completed int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`,
		runID, event.TypeRunCompleted,
	).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if completed != 0 {
		t.Fatal("completed")
	}
}

type crashStub struct{}

func (crashStub) Handle(worker.In, func(worker.Out) error) (*worker.Out, error) {
	return nil, fmt.Errorf("worker_exit")
}

func (crashStub) Done(string) {}

func TestWorkerExitFailsRun(t *testing.T) {
	runService, db, work := askEnv(t)
	runService.Worker = crashStub{}
	sessionID := testdb.InsertSession(t, db)
	runID, err := runService.PostUserMessage(context.Background(), sessionID, "crash", work)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runID, StatusFailed)
	var failed, completed int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`,
		runID, event.TypeRunFailed,
	).Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`,
		runID, event.TypeRunCompleted,
	).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if failed != 1 || completed != 0 {
		t.Fatalf("failed=%d completed=%d", failed, completed)
	}
}
