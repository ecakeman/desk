package run

import (
	"context"
	"errors"
	"testing"

	"desk/internal/ids"
	"desk/internal/testdb"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestCan(t *testing.T) {
	if !Can(StatusRunning, StatusFailed) {
		t.Fatal("running → failed should be allowed")
	}
	if Can(StatusCompleted, StatusRunning) {
		t.Fatal("completed → running should be forbidden")
	}
	if Can(StatusInterrupted, StatusCompleted) || Can(StatusCompleted, StatusInterrupted) {
		t.Fatal("terminal statuses must not re-enter the machine")
	}
	if !Terminal(StatusCompleted) || Terminal(StatusRunning) {
		t.Fatal("Terminal()")
	}
}

func TestIllegalTransitionLeavesDB(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	sessID, runID := testdb.InsertSession(t, db), ids.New()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO runs (id, session_id, status, workspace_dir) VALUES ($1,$2,$3,'')`,
		runID, sessID, StatusCompleted); err != nil {
		t.Fatal(err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = Transition(ctx, tx, runID, StatusCompleted, StatusRunning)
	_ = tx.Rollback()
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}

	var status string
	if err := db.QueryRowContext(ctx,
		`SELECT status FROM runs WHERE id=$1`, runID,
	).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != StatusCompleted {
		t.Fatalf("status = %s, want completed", status)
	}
}
