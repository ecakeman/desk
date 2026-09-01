package event

import (
	"context"
	"strings"
	"testing"

	"desk/internal/ids"
	"desk/internal/testdb"
)

func TestEnsureCompactReplacesHistoricalToolPayload(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	sessionID, oldRun, currentRun := ids.New(), ids.New(), ids.New()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions(id,status) VALUES($1,'open')`, sessionID,
	); err != nil {
		t.Fatal(err)
	}
	testdb.CleanupSession(t, db, sessionID)
	for _, item := range []struct {
		id     string
		status string
	}{{oldRun, "completed"}, {currentRun, "completed"}} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO runs(id,session_id,status,workspace_dir)
			VALUES($1,$2,$3,'')`,
			item.id, sessionID, item.status,
		); err != nil {
			t.Fatal(err)
		}
	}
	store := NewStore(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, tx, oldRun, TypeToolCompleted, map[string]any{
		"id": "large", "name": "fs.read", "data": strings.Repeat("x", MaxSTMChars+500),
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if err := store.EnsureCompact(ctx, sessionID, currentRun); err != nil {
		t.Fatal(err)
	}
	var compacted int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`,
		oldRun, TypeEpisodeCompacted,
	).Scan(&compacted); err != nil {
		t.Fatal(err)
	}
	if compacted != 1 {
		t.Fatalf("compacted=%d", compacted)
	}
	messages, err := store.Messages(ctx, sessionID, currentRun)
	if err != nil {
		t.Fatal(err)
	}
	rendered := ""
	for _, message := range messages {
		if content, ok := message["content"].(string); ok {
			rendered += content
		}
	}
	if !strings.Contains(rendered, "[event episode.compacted") {
		t.Fatalf("missing compact context: %q", rendered)
	}
	if strings.Contains(rendered, strings.Repeat("x", 1000)) {
		t.Fatal("raw historical tool payload was not replaced")
	}
}
