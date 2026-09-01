package run

import (
	"context"
	"database/sql"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"desk/internal/event"
	"desk/internal/ids"
	"desk/internal/plugin"
	"desk/internal/worker"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type sleepStub struct{}

func (sleepStub) Handle(in worker.In, _ func(worker.Out) error) (*worker.Out, error) {
	switch in.T {
	case "turn.start":
		return &worker.Out{T: "tool.request", ID: "1", Name: "fs.sleep", Args: map[string]any{}}, nil
	default:
		return &worker.Out{T: "turn.finish", Text: "ok"}, nil
	}
}

func (sleepStub) Done(string) {}

func TestCancelStopsSleep(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://desk:desk@127.0.0.1:5432/desk?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Skip("postgres 未启动")
	}
	t.Cleanup(func() { _ = db.Close() })

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
	svc := NewService(db, event.NewStore(db))
	svc.Plugins = reg
	svc.Worker = sleepStub{}
	svc.PromptsDir = filepath.Join(root, "prompts")

	sess := ids.New()
	if _, err := db.Exec(`INSERT INTO sessions (id, status) VALUES ($1, 'open')`, sess); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	runID, err := svc.PostUserMessage(context.Background(), sess, "sleep", work)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var st string
		if err := db.QueryRow(`SELECT status FROM runs WHERE id=$1`, runID).Scan(&st); err != nil {
			t.Fatal(err)
		}
		if st == StatusRunning {
			if err := svc.Cancel(runID); err != nil {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var st string
		if err := db.QueryRow(`SELECT status FROM runs WHERE id=$1`, runID).Scan(&st); err != nil {
			t.Fatal(err)
		}
		if st == StatusInterrupted {
			if time.Since(start) > 4*time.Second {
				t.Fatalf("too slow: %s", time.Since(start))
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("not interrupted")
}

func TestRecoverMarksRunning(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://desk:desk@127.0.0.1:5432/desk?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Skip("postgres 未启动")
	}
	t.Cleanup(func() { _ = db.Close() })

	sess, runID := ids.New(), ids.New()
	if _, err := db.Exec(`INSERT INTO sessions (id, status) VALUES ($1, 'open')`, sess); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO runs (id, session_id, status, workspace_dir) VALUES ($1,$2,'running','')`,
		runID, sess,
	); err != nil {
		t.Fatal(err)
	}
	svc := NewService(db, event.NewStore(db))
	if err := svc.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	var st string
	if err := db.QueryRow(`SELECT status FROM runs WHERE id=$1`, runID).Scan(&st); err != nil {
		t.Fatal(err)
	}
	if st != StatusInterrupted {
		t.Fatalf("status=%s", st)
	}
	if err := svc.Cancel(runID); err != ErrNotWaiting {
		t.Fatalf("cancel completed: %v", err)
	}
}
