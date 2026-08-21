package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"desk/internal/event"
	"desk/internal/run"
	"desk/internal/session"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestHealthzDBDown(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://desk:desk@127.0.0.1:1/desk?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	srv := httptest.NewServer(NewMux(Deps{DB: db}))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != false {
		t.Fatalf("body = %#v", body)
	}
}

func testMux(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()
	db, err := sql.Open("pgx", "postgres://desk:desk@127.0.0.1:5432/desk?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Skip("postgres 未启动：先 compose up")
	}
	t.Cleanup(func() { _ = db.Close() })

	ev := event.NewStore(db)
	srv := httptest.NewServer(NewMux(Deps{
		DB:        db,
		Workspace: ".",
		Sessions:  session.NewStore(db),
		Runs:      run.NewStore(db),
		Messages:  run.NewService(db, ev),
	}))
	t.Cleanup(srv.Close)
	return srv, db
}

func TestPostUserMessageCreatesRun(t *testing.T) {
	srv, db := testMux(t)

	resp, err := http.Post(srv.URL+"/v1/sessions", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create session status = %d", resp.StatusCode)
	}
	var sess struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		t.Fatal(err)
	}
	if sess.ID == "" || sess.Status != "open" {
		t.Fatalf("session = %#v", sess)
	}

	body, err := json.Marshal(map[string]string{"text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.Post(srv.URL+"/v1/sessions/"+sess.ID+"/messages", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post message status = %d", resp.StatusCode)
	}
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.RunID == "" {
		t.Fatal("empty run_id")
	}

	resp, err = http.Get(srv.URL + "/v1/runs/" + created.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var runOut struct {
		ID           string `json:"id"`
		SessionID    string `json:"session_id"`
		Status       string `json:"status"`
		WorkspaceDir string `json:"workspace_dir"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&runOut); err != nil {
		t.Fatal(err)
	}
	if runOut.Status != "running" || runOut.SessionID != sess.ID {
		t.Fatalf("run = %#v", runOut)
	}

	ctx := context.Background()
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE run_id=$1`, created.RunID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("events = %d, want 2", n)
	}
}