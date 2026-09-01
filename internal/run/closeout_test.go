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
	"desk/internal/worker"
)

func TestFailDoesNotMoveCompleted(t *testing.T) {
	svc, db, _ := askEnv(t)
	ctx := context.Background()
	sess, runID := ids.New(), ids.New()
	if _, err := db.Exec(`INSERT INTO sessions (id, status) VALUES ($1, 'open')`, sess); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO runs (id, session_id, status, workspace_dir) VALUES ($1,$2,$3,'')`,
		runID, sess, StatusCompleted,
	); err != nil {
		t.Fatal(err)
	}
	if err := svc.Fail(ctx, runID, "late"); err != nil {
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
	svc, db, _ := askEnv(t)
	ctx := context.Background()
	sess, runID := ids.New(), ids.New()
	if _, err := db.Exec(`INSERT INTO sessions (id, status) VALUES ($1, 'open')`, sess); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO runs (id, session_id, status, workspace_dir) VALUES ($1,$2,$3,'')`,
		runID, sess, StatusWaitingApproval,
	); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	seq, err := svc.Events.Append(ctx, tx, runID, event.TypeToolRequested, map[string]any{
		"id": "1", "name": "fs.write",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := svc.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	var st string
	if err := db.QueryRow(`SELECT status FROM runs WHERE id=$1`, runID).Scan(&st); err != nil {
		t.Fatal(err)
	}
	if st != StatusInterrupted {
		t.Fatalf("status=%s", st)
	}
	if err := svc.Decide(ctx, runID, seq, true); !errors.Is(err, ErrNotWaiting) {
		t.Fatalf("decide after recover: %v", err)
	}
}

func TestTwoMessagesSameSessionAreTwoRuns(t *testing.T) {
	svc, db, work := askEnv(t)
	ctx := context.Background()
	sess := ids.New()
	if _, err := db.Exec(`INSERT INTO sessions (id, status) VALUES ($1, 'open')`, sess); err != nil {
		t.Fatal(err)
	}
	a, err := svc.PostUserMessage(ctx, sess, "one", work)
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.PostUserMessage(ctx, sess, "two", work)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("same run_id")
	}
	waitStatus(t, db, a, StatusWaitingApproval)
	waitStatus(t, db, b, StatusWaitingApproval)
	if err := svc.Decide(ctx, a, requestedSeq(t, db, a), false); err != nil {
		t.Fatal(err)
	}
	if err := svc.Decide(ctx, b, requestedSeq(t, db, b), false); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, a, StatusCompleted)
	waitStatus(t, db, b, StatusCompleted)
}

func TestDuplicateDecisionConflicts(t *testing.T) {
	svc, db, work := askEnv(t)
	runID := postRun(t, svc, db, work)
	seq := requestedSeq(t, db, runID)
	var first, second error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		first = svc.Decide(context.Background(), runID, seq, false)
	}()
	go func() {
		defer wg.Done()
		second = svc.Decide(context.Background(), runID, seq, false)
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
	svc, db, work := askEnv(t)
	svc.Worker = sleepStub{}
	sess := ids.New()
	if _, err := db.Exec(`INSERT INTO sessions (id, status) VALUES ($1, 'open')`, sess); err != nil {
		t.Fatal(err)
	}
	runID, err := svc.PostUserMessage(context.Background(), sess, "sleep", work)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runID, StatusRunning)
	if err := svc.Cancel(runID); err != nil {
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
	svc, db, work := askEnv(t)
	runID := postRun(t, svc, db, work)
	seq := requestedSeq(t, db, runID)
	var decideErr, cancelErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		decideErr = svc.Decide(context.Background(), runID, seq, false)
	}()
	go func() {
		defer wg.Done()
		cancelErr = svc.Cancel(runID)
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
	svc, db, work := askEnv(t)
	runID := postRun(t, svc, db, work)
	seq := requestedSeq(t, db, runID)
	if err := svc.Decide(context.Background(), runID, seq, false); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runID, StatusCompleted)
	err := svc.Decide(context.Background(), runID, seq, true)
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
	svc, db, _ := askEnv(t)
	ctx := context.Background()
	for _, st := range []string{StatusCompleted, StatusFailed, StatusInterrupted} {
		sess, runID := ids.New(), ids.New()
		if _, err := db.Exec(`INSERT INTO sessions (id, status) VALUES ($1, 'open')`, sess); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(
			`INSERT INTO runs (id, session_id, status, workspace_dir) VALUES ($1,$2,$3,'')`,
			runID, sess, st,
		); err != nil {
			t.Fatal(err)
		}
		if err := svc.Decide(ctx, runID, 1, true); !errors.Is(err, ErrNotWaiting) {
			t.Fatalf("status=%s decide=%v", st, err)
		}
		if err := svc.Cancel(runID); !errors.Is(err, ErrNotWaiting) {
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
	svc, db, work := askEnv(t)
	runID := postRun(t, svc, db, work)
	seq := requestedSeq(t, db, runID)
	if err := svc.Cancel(runID); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runID, StatusInterrupted)
	if err := svc.Decide(context.Background(), runID, seq, true); !errors.Is(err, ErrNotWaiting) {
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
	svc, db, work := askEnv(t)
	svc.Worker = crashStub{}
	sess := ids.New()
	if _, err := db.Exec(`INSERT INTO sessions (id, status) VALUES ($1, 'open')`, sess); err != nil {
		t.Fatal(err)
	}
	runID, err := svc.PostUserMessage(context.Background(), sess, "crash", work)
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
