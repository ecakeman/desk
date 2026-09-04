package run

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"desk/internal/event"
	"desk/internal/plugin"
	"desk/internal/testdb"
	"desk/internal/worker"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type writeStub struct{}

func (writeStub) Handle(workerInput worker.In, _ func(worker.Out) error) (*worker.Out, error) {
	switch workerInput.T {
	case "context.replace":
		return &worker.Out{T: "context.replaced"}, nil
	case "turn.start":
		return &worker.Out{
			T: "tool.request", ID: "1", Name: "fs.write",
			Args: map[string]any{"path": "d12.txt", "content": "hello"},
		}, nil
	default:
		return &worker.Out{T: "turn.finish", Text: "ok"}, nil
	}
}

func (writeStub) Done(string) {}

func askEnv(t *testing.T) (*Service, *sql.DB, string) {
	t.Helper()
	db := testdb.Open(t)

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", filepath.Join(root, "plugins/fs/fs"), "./plugins/fs")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fs: %s %v", out, err)
	}
	work := t.TempDir()
	reg, err := plugin.Load(filepath.Join(root, "plugins"), work)
	if err != nil {
		t.Fatal(err)
	}
	ev := event.NewStore(db)
	runService := NewService(db, ev)
	runService.Plugins = reg
	runService.Worker = writeStub{}
	runService.PromptsDir = filepath.Join(root, "prompts")
	return runService, db, work
}

func waitStatus(t *testing.T, db *sql.DB, runID, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var st string
		if err := db.QueryRow(`SELECT status FROM runs WHERE id=$1`, runID).Scan(&st); err != nil {
			t.Fatal(err)
		}
		if st == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("status want %s", want)
}

func postRun(t *testing.T, runService *Service, db *sql.DB, work string) string {
	t.Helper()
	ctx := context.Background()
	sessionID := testdb.InsertSession(t, db)
	runID, err := runService.PostUserMessage(ctx, sessionID, "write", work)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runID, StatusWaitingApproval)
	return runID
}

func requestedSeq(t *testing.T, db *sql.DB, runID string) int {
	t.Helper()
	var seq int
	err := db.QueryRow(
		`SELECT seq FROM events WHERE run_id=$1 AND type=$2 ORDER BY seq DESC LIMIT 1`,
		runID, event.TypeToolRequested,
	).Scan(&seq)
	if err != nil {
		t.Fatal(err)
	}
	return seq
}

func TestAskBadSeqDoesNotWrite(t *testing.T) {
	runService, db, work := askEnv(t)
	runID := postRun(t, runService, db, work)
	pendingSeq := requestedSeq(t, db, runID)
	err := runService.Decide(context.Background(), runID, 999, true)
	if err != ErrBadSeq {
		t.Fatalf("err=%v", err)
	}
	waitStatus(t, db, runID, StatusWaitingApproval)
	if _, err := os.Stat(filepath.Join(work, "d12.txt")); !os.IsNotExist(err) {
		t.Fatalf("file exists: %v", err)
	}
	if err := runService.Decide(context.Background(), runID, pendingSeq, false); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runID, StatusCompleted)
}

func TestAskOnlyAcceptsPendingRequestSeq(t *testing.T) {
	runService, db, work := askEnv(t)
	runID := postRun(t, runService, db, work)
	pendingSeq := requestedSeq(t, db, runID)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	otherSeq, err := runService.Events.Append(context.Background(), tx, runID, event.TypeToolRequested, map[string]any{
		"id": "other", "name": "fs.write",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := runService.Decide(context.Background(), runID, otherSeq, true); err != ErrNotWaiting {
		t.Fatalf("err=%v", err)
	}
	waitStatus(t, db, runID, StatusWaitingApproval)
	if err := runService.Decide(context.Background(), runID, pendingSeq, false); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runID, StatusCompleted)
	if _, err := os.Stat(filepath.Join(work, "d12.txt")); !os.IsNotExist(err) {
		t.Fatalf("file exists: %v", err)
	}
}

func TestAskDenyNoFile(t *testing.T) {
	runService, db, work := askEnv(t)
	runID := postRun(t, runService, db, work)
	seq := requestedSeq(t, db, runID)
	if err := runService.Decide(context.Background(), runID, seq, false); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runID, StatusCompleted)
	if _, err := os.Stat(filepath.Join(work, "d12.txt")); !os.IsNotExist(err) {
		t.Fatal("file should be absent")
	}
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`,
		runID, event.TypeToolDenied,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("denied events=%d", n)
	}
}

func TestAskAllowWrites(t *testing.T) {
	runService, db, work := askEnv(t)
	runID := postRun(t, runService, db, work)
	seq := requestedSeq(t, db, runID)
	if err := runService.Decide(context.Background(), runID, seq, true); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runID, StatusCompleted)
	b, err := os.ReadFile(filepath.Join(work, "d12.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "hello" {
		t.Fatalf("content=%q", b)
	}
}
