package run

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"desk/internal/ctxmgr"
	"desk/internal/event"
	"desk/internal/ids"
	"desk/internal/testdb"
	"desk/internal/worker"
)

func TestContextRebuildReplacesWorkerMessages(t *testing.T) {
	w := &recWorker{fn: func(in worker.In) *worker.Out {
		return &worker.Out{T: "turn.finish", Text: "ok"}
	}}
	work := t.TempDir()
	svc, db := contractEnv(t, w, work)
	ok := []byte(`{"summary":"保留当前工具推进状态","facts":[{"key":"step","value":"ping","status":"active","confidence":0.9,"source_event_seqs":[1]}],"open_items":[],"decisions":["continue"]}`)
	cm := ctxmgr.New(svc.Events, svc.Index, ctxmgr.Settings{
		WindowTokens:    20,
		SmallTriggerTok: 1,
		LargeSmallCount: 99,
		PromptsDir:      filepath.Join(repoRoot(t), "prompts"),
	})
	cm.Compactor = &ctxmgr.StubCompactor{Raw: ok}
	svc.Context = cm
	sess := testdb.InsertSession(t, db)
	runID := ids.New()
	if _, err := db.Exec(`INSERT INTO runs(id,session_id,status,workspace_dir) VALUES($1,$2,'running',$3)`, runID, sess, work); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Events.Append(context.Background(), tx, runID, event.TypeMessageUser, map[string]string{"text": strings.Repeat("evict-me ", 12)}); err != nil {
			t.Fatal(err)
		}
		_ = tx.Commit()
	}
	asm, err := cm.Prepare(context.Background(), ctxmgr.PrepareIn{SessionID: sess, RunID: runID, Workspace: work})
	if err != nil {
		t.Fatal(err)
	}
	out, err := svc.Worker.Handle(worker.In{T: "context.replace", RunID: runID, Messages: asm.Messages}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.T != "context.replaced" {
		t.Fatalf("t=%s", out.T)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`, runID, event.TypeContextSmallCompact).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatal("expected small compact")
	}
}

func TestContextReconstructionFromDurableState(t *testing.T) {
	w := &recWorker{fn: func(in worker.In) *worker.Out {
		return &worker.Out{T: "turn.finish", Text: "done"}
	}}
	work := t.TempDir()
	svc, db := contractEnv(t, w, work)
	sess := testdb.InsertSession(t, db)
	runID := postWait(t, svc, db, sess, "inspect me", work, StatusCompleted)
	first, ok := svc.InspectContext(runID)
	if !ok || len(first.Messages) == 0 {
		t.Fatal("missing assembly")
	}
	svc.Worker.Done(runID)
	again, err := svc.contextMgr().Prepare(context.Background(), ctxmgr.PrepareIn{
		SessionID: sess, RunID: runID, Workspace: work, Phase: "act",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Messages) == 0 {
		t.Fatal("reconstruct empty")
	}
}

func TestRetrievalBoundedAndEmpty(t *testing.T) {
	w := &recWorker{fn: func(in worker.In) *worker.Out {
		return &worker.Out{T: "turn.finish", Text: "ok"}
	}}
	svc, db := contractEnv(t, w, t.TempDir())
	sess := testdb.InsertSession(t, db)
	runID := postWait(t, svc, db, sess, "no history yet", t.TempDir(), StatusCompleted)
	asm, _ := svc.InspectContext(runID)
	if len(asm.Applied.Retrieval) > 8 {
		t.Fatalf("unbounded retrieval %d", len(asm.Applied.Retrieval))
	}
}

func TestInterruptRecoverUnchanged(t *testing.T) {
	db := testdb.Open(t)
	sess := testdb.InsertSession(t, db)
	runID := ids.New()
	if _, err := db.Exec(`INSERT INTO runs(id,session_id,status,workspace_dir) VALUES($1,$2,'running','')`, runID, sess); err != nil {
		t.Fatal(err)
	}
	svc := NewService(db, event.NewStore(db))
	if err := svc.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runStatus(t, db, runID) != StatusInterrupted {
		t.Fatalf("status=%s", runStatus(t, db, runID))
	}
}
