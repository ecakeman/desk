package run

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"desk/internal/ctxmgr"
	"desk/internal/event"
	"desk/internal/ids"
	"desk/internal/prompt"
	"desk/internal/testdb"
	"desk/internal/worker"
)

var (
	errReplaceFail = errors.New("replace_fail")
	errChatFail    = errors.New("chat_fail")
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
		TotalTokens:     1_000_000,
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
	first, src, ok := svc.InspectContext(context.Background(), sess, runID)
	if !ok || src != "assembled" || len(first.Messages) == 0 {
		t.Fatal("missing assembly")
	}
	svc.contextMgr().Forget(runID)
	again, src2, ok := svc.InspectContext(context.Background(), sess, runID)
	if !ok || src2 != "reconstructable" || len(again.Messages) == 0 {
		t.Fatalf("durable inspect src=%s ok=%v", src2, ok)
	}
}

func TestRetrievalBoundedAndEmpty(t *testing.T) {
	w := &recWorker{fn: func(in worker.In) *worker.Out {
		return &worker.Out{T: "turn.finish", Text: "ok"}
	}}
	svc, db := contractEnv(t, w, t.TempDir())
	sess := testdb.InsertSession(t, db)
	runID := postWait(t, svc, db, sess, "no history yet", t.TempDir(), StatusCompleted)
	asm, _, _ := svc.InspectContext(context.Background(), sess, runID)
	if len(asm.Applied.Retrieval) > 8 {
		t.Fatalf("unbounded retrieval %d", len(asm.Applied.Retrieval))
	}
}

func TestReplaceFailDoesNotWriteApplied(t *testing.T) {
	inner := &recWorker{fn: func(in worker.In) *worker.Out {
		return &worker.Out{T: "turn.finish", Text: "ok"}
	}}
	w := &failReplaceWorker{inner: inner}
	work := t.TempDir()
	svc, db := contractEnv(t, w, work)
	okJSON := []byte(`{"summary":"保留当前工具推进状态足够长","facts":[{"key":"step","value":"ping","status":"active","confidence":0.9,"source_event_seqs":[1]}],"open_items":[],"decisions":["continue"]}`)
	cm := ctxmgr.New(svc.Events, svc.Index, ctxmgr.Settings{
		WindowTokens:    20,
		TotalTokens:     1_000_000,
		SmallTriggerTok: 1,
		LargeSmallCount: 99,
		PromptsDir:      filepath.Join(repoRoot(t), "prompts"),
	})
	cm.Compactor = &ctxmgr.StubCompactor{Raw: okJSON}
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
	snapshot, err := prompt.Load(svc.PromptsDir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.ask(context.Background(), runID, sess, work, snapshot, worker.In{
		T: "tool.result", ID: "call-1", Phase: "act", OK: true,
	})
	if err == nil {
		t.Fatal("expected replace failure")
	}
	var applied, smalls int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`, runID, event.TypeContextApplied).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`, runID, event.TypeContextSmallCompact).Scan(&smalls); err != nil {
		t.Fatal(err)
	}
	if smalls < 1 {
		t.Fatal("compact should have committed before replace")
	}
	if applied != 0 {
		t.Fatalf("applied=%d want 0 after replace fail", applied)
	}
	if w.replaces == 0 {
		t.Fatal("expected context.replace attempt")
	}
}

func TestChatHandleFailDoesNotWriteApplied(t *testing.T) {
	w := &errStartWorker{}
	svc, db := contractEnv(t, w, t.TempDir())
	sess := testdb.InsertSession(t, db)
	runID, err := svc.PostUserMessage(context.Background(), sess, "chat fail", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runID, StatusFailed)
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`, runID, event.TypeContextApplied).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("applied=%d", n)
	}
}

func TestAskSuccessWritesApplied(t *testing.T) {
	w := &recWorker{}
	work := t.TempDir()
	svc, db := contractEnv(t, w, work)
	sess := testdb.InsertSession(t, db)
	runID := ids.New()
	if _, err := db.Exec(`INSERT INTO runs(id,session_id,status,workspace_dir) VALUES($1,$2,'running',$3)`, runID, sess, work); err != nil {
		t.Fatal(err)
	}
	snapshot, err := prompt.Load(svc.PromptsDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ask(context.Background(), runID, sess, work, snapshot, worker.In{
		T: "turn.start", Phase: "plan",
	}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`, runID, event.TypeContextApplied).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("applied=%d want 1 after chat success", n)
	}
}

type failReplaceWorker struct {
	inner    *recWorker
	replaces int
}

func (w *failReplaceWorker) Handle(in worker.In, emit func(worker.Out) error) (*worker.Out, error) {
	if in.T == "context.replace" {
		w.replaces++
		return nil, errReplaceFail
	}
	return w.inner.Handle(in, emit)
}

func (w *failReplaceWorker) Done(id string) { w.inner.Done(id) }

type errStartWorker struct{}

func (errStartWorker) Handle(worker.In, func(worker.Out) error) (*worker.Out, error) {
	return nil, errChatFail
}

func (errStartWorker) Done(string) {}

func TestInterruptRecoverUnchanged(t *testing.T) {
	db := testdb.Open(t)
	sess := testdb.InsertSession(t, db)
	runID := ids.New()
	if _, err := db.Exec(`INSERT INTO runs(id,session_id,status,workspace_dir) VALUES($1,$2,'running','')`, runID, sess); err != nil {
		t.Fatal(err)
	}
	w := &recWorker{}
	svc := NewService(db, event.NewStore(db))
	svc.Worker = w
	waitID := ids.New()
	if _, err := db.Exec(`INSERT INTO runs(id,session_id,status,workspace_dir) VALUES($1,$2,'waiting_approval','')`, waitID, sess); err != nil {
		t.Fatal(err)
	}
	if err := svc.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runStatus(t, db, runID) != StatusInterrupted {
		t.Fatalf("running status=%s", runStatus(t, db, runID))
	}
	if runStatus(t, db, waitID) != StatusInterrupted {
		t.Fatalf("waiting_approval status=%s", runStatus(t, db, waitID))
	}
	if n := len(w.snapshot()); n != 0 {
		t.Fatalf("Recover must not Drive asks=%d", n)
	}
}
