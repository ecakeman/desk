package event

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"testing"

	"desk/internal/ids"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", "postgres://desk:desk@127.0.0.1:5432/desk?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		t.Skip("postgres 未启动：先 compose up")
	}
	t.Cleanup(func() {
		db.Close()
	})
	return db
}

func TestAppendRollbackLeavesNothing(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	sessID := ids.New()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions (id, status) VALUES ($1, 'open')`, sessID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	runID := ids.New()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO runs (id, session_id, status, workspace_dir) VALUES ($1,$2,'running','')`,
		runID, sessID); err != nil {
		t.Fatal(err)
	}
	st := NewStore(db)
	st.OnInsert = func(
		ctx context.Context,
		tx *sql.Tx,
		runID string,
		seq int,
		typ string,
		_ json.RawMessage,
	) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO memory_docs (run_id, seq, kind, text)
			VALUES ($1,$2,$3,'projection')`,
			runID, seq, typ,
		)
		return err
	}
	if _, err := st.Append(ctx, tx, runID, TypeRunCreated, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO events (run_id, seq, type, payload) VALUES ($1, 1, 'dup', '{}')`,
		runID); err == nil {
		t.Fatal("expected unique violation")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE id=$1`, runID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("runs leftover = %d", n)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE run_id=$1`, runID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("events leftover = %d", n)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_docs WHERE run_id=$1`, runID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("memory_docs leftover = %d", n)
	}
}

func TestAppendSerializesSeq(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	sessID, runID := ids.New(), ids.New()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions (id, status) VALUES ($1, 'open')`, sessID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO runs (id, session_id, status, workspace_dir) VALUES ($1,$2,'running','')`,
		runID, sessID,
	); err != nil {
		t.Fatal(err)
	}
	st := NewStore(db)
	const count = 16
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				errs <- err
				return
			}
			defer tx.Rollback()
			if _, err := st.Append(ctx, tx, runID, TypeMessageUser, map[string]string{"text": "x"}); err != nil {
				errs <- err
				return
			}
			errs <- tx.Commit()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var got int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE run_id=$1`, runID,
	).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != count {
		t.Fatalf("events=%d want=%d", got, count)
	}
}
