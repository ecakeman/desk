package run

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"desk/internal/ids"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestCan(t *testing.T) {
	if !Can(StatusRunning, StatusFailed) {
		t.Fatal("running → failed should be allowed")
	}
	if Can(StatusCompleted, StatusRunning) {
		t.Fatal("completed → running should be forbidden")
	}
}

func TestIllegalTransitionLeavesDB(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://desk:desk@127.0.0.1:5432/desk?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		t.Skip("postgres 未启动")
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	sessID, runID := ids.New(), ids.New()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions (id, status) VALUES ($1, 'open')`, sessID); err != nil {
		t.Fatal(err)
	}
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