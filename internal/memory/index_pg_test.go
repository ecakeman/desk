package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"desk/internal/db"
	"desk/internal/event"
	"desk/internal/ids"
)

func TestIndexLexAndEmbedFailKeepsText(t *testing.T) {
	dsn := os.Getenv("DESK_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://desk:desk@127.0.0.1:5432/desk?sslmode=disable"
	}
	ctx := context.Background()
	sqlDB, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skip(err)
	}
	defer sqlDB.Close()
	mig := migrationDir()
	if err := db.Migrate(ctx, sqlDB, mig); err != nil {
		t.Skip(err)
	}
	idx := New(sqlDB)
	idx.Embedder = errEmbed{}
	runID := "memtest-embed-fail"
	_, _ = sqlDB.ExecContext(ctx, `DELETE FROM memory_docs WHERE run_id=$1`, runID)
	raw, _ := json.Marshal(map[string]string{"text": "fs.write 越狱 path_escaped"})
	if err := idx.Index(ctx, runID, 1, event.TypeMessageUser, raw); err != nil {
		t.Fatal(err)
	}
	var text string
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT text FROM memory_docs WHERE run_id=$1 AND seq=1`, runID,
	).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if text == "" {
		t.Fatal("text missing")
	}
	searchIndex := New(sqlDB)
	hits, err := searchIndex.Search(ctx, "path_escaped", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("bm25 miss")
	}
	failingSearch := New(sqlDB)
	failingSearch.Embedder = errEmbed{}
	hits, err = failingSearch.Search(ctx, "path_escaped", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("query embed fail must fall back to bm25")
	}
	failingSearch.Reranker = errRerank{}
	hits, err = failingSearch.Search(ctx, "path_escaped", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("rerank fail must fall back to lexical order")
	}
}

func TestSyncReconcilesEventsAndOrphans(t *testing.T) {
	dsn := os.Getenv("DESK_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://desk:desk@127.0.0.1:5432/desk?sslmode=disable"
	}
	ctx := context.Background()
	sqlDB, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skip(err)
	}
	defer sqlDB.Close()
	sessionID, runID := ids.New(), ids.New()
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO sessions (id,status) VALUES ($1,'open')`, sessionID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO runs (id,session_id,status,workspace_dir) VALUES ($1,$2,'running','')`,
		runID, sessionID,
	); err != nil {
		t.Fatal(err)
	}
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := event.NewStore(sqlDB)
	if _, err := store.Append(ctx, tx, runID, event.TypeMessageUser, map[string]string{"text": "sync fact"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx, `
		INSERT INTO memory_docs(run_id,seq,kind,text)
		VALUES ('orphan-sync-test',1,'message.user','orphan')
		ON CONFLICT (run_id,seq) DO UPDATE SET text=EXCLUDED.text`,
	); err != nil {
		t.Fatal(err)
	}

	idx := New(sqlDB)
	if err := idx.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	var text string
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT text FROM memory_docs WHERE run_id=$1 AND seq=1`, runID,
	).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if text != "sync fact" {
		t.Fatalf("text=%q", text)
	}
	var orphanCount int
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_docs WHERE run_id='orphan-sync-test'`,
	).Scan(&orphanCount); err != nil {
		t.Fatal(err)
	}
	if orphanCount != 0 {
		t.Fatalf("orphans=%d", orphanCount)
	}
}

func TestSearchRerankChangesOrder(t *testing.T) {
	dsn := os.Getenv("DESK_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://desk:desk@127.0.0.1:5432/desk?sslmode=disable"
	}
	ctx := context.Background()
	sqlDB, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skip(err)
	}
	defer sqlDB.Close()
	if err := db.Migrate(ctx, sqlDB, migrationDir()); err != nil {
		t.Skip(err)
	}
	runA, runB := ids.New(), ids.New()
	_, _ = sqlDB.ExecContext(ctx, `DELETE FROM memory_docs WHERE run_id=$1 OR run_id=$2`, runA, runB)
	idx := New(sqlDB)
	rawA, _ := json.Marshal(map[string]string{"text": "zzqmarker zzqmarker zzqmarker zzqmarker unique-a-only"})
	rawB, _ := json.Marshal(map[string]string{"text": "zzqmarker unique-b-only"})
	if err := idx.Index(ctx, runA, 1, event.TypeMessageUser, rawA); err != nil {
		t.Fatal(err)
	}
	if err := idx.Index(ctx, runB, 1, event.TypeMessageUser, rawB); err != nil {
		t.Fatal(err)
	}
	hits, err := idx.Search(ctx, "zzqmarker", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 2 || hits[0].RunID != runA {
		t.Fatalf("bm25 first=%+v want %s", hits, runA)
	}
	idx.Reranker = reverseRerank{}
	idx.RerankPool = 2
	hits, err = idx.Search(ctx, "zzqmarker", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].RunID != runB {
		t.Fatalf("rerank first=%+v want %s", hits, runB)
	}
}

func TestRebuildRestoresDocsWithoutChangingEvents(t *testing.T) {
	dsn := os.Getenv("DESK_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://desk:desk@127.0.0.1:5432/desk?sslmode=disable"
	}
	ctx := context.Background()
	sqlDB, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Skip(err)
	}
	defer sqlDB.Close()
	if err := db.Migrate(ctx, sqlDB, migrationDir()); err != nil {
		t.Skip(err)
	}
	sessionID, runID := ids.New(), ids.New()
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO sessions (id,status) VALUES ($1,'open')`, sessionID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO runs (id,session_id,status,workspace_dir) VALUES ($1,$2,'running','')`,
		runID, sessionID,
	); err != nil {
		t.Fatal(err)
	}
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := event.NewStore(sqlDB)
	if _, err := store.Append(ctx, tx, runID, event.TypeMessageUser, map[string]string{"text": "rebuild fact"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var nEvents int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM events`).Scan(&nEvents); err != nil {
		t.Fatal(err)
	}
	var payload []byte
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT payload FROM events WHERE run_id=$1 AND seq=1`, runID,
	).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	idx := New(sqlDB)
	if err := idx.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM events`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != nEvents {
		t.Fatalf("events %d → %d", nEvents, after)
	}
	var again []byte
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT payload FROM events WHERE run_id=$1 AND seq=1`, runID,
	).Scan(&again); err != nil {
		t.Fatal(err)
	}
	if string(again) != string(payload) {
		t.Fatalf("payload changed")
	}
	var text string
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT text FROM memory_docs WHERE run_id=$1 AND seq=1`, runID,
	).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if text != "rebuild fact" {
		t.Fatalf("text=%q", text)
	}
}

type errEmbed struct{}

func (errEmbed) Embed(context.Context, string) ([]float32, error) {
	return nil, fmt.Errorf("embed_down")
}
