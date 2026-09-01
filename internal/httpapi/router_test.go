package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"desk/internal/event"
	"desk/internal/ids"
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
		Events:    ev,
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

func TestDashboardReadEndpoints(t *testing.T) {
	srv, _ := testMux(t)
	resp, err := http.Post(srv.URL+"/v1/sessions", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sess session.Session
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"text": "dashboard trace"})
	resp, err = http.Post(
		srv.URL+"/v1/sessions/"+sess.ID+"/messages",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	assertJSONArray(t, srv.URL+"/v1/sessions")
	assertJSONArray(t, srv.URL+"/v1/sessions/"+sess.ID+"/runs")

	resp, err = http.Get(srv.URL + "/v1/runs/" + created.RunID + "/stm")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var stm struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&stm); err != nil {
		t.Fatal(err)
	}
	if len(stm.Messages) != 1 || stm.Messages[0]["content"] != "dashboard trace" {
		t.Fatalf("stm=%#v", stm.Messages)
	}

	resp, err = http.Get(srv.URL + "/v1/runs/" + created.RunID + "/events/1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var item event.Event
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		t.Fatal(err)
	}
	if item.RunID != created.RunID || item.Seq != 1 || item.Type != event.TypeRunCreated {
		t.Fatalf("event=%#v", item)
	}
}

func assertJSONArray(t *testing.T, url string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s status=%d", url, resp.StatusCode)
	}
	var out []any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatalf("%s returned no rows", url)
	}
}

func TestStaticWebFallbackDoesNotSwallowAPI(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("desk-ui"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", "postgres://desk:desk@127.0.0.1:1/desk?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	srv := httptest.NewServer(NewMux(Deps{DB: db, WebDir: dir}))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/some/client/route")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("spa status=%d", resp.StatusCode)
	}

	resp, err = http.Get(srv.URL + "/v1/missing")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("api status=%d", resp.StatusCode)
	}
}

func TestAPIErrorsAreStable(t *testing.T) {
	srv, db := testMux(t)
	resp, err := http.Get(srv.URL + "/v1/runs/" + ids.New())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing run status=%d", resp.StatusCode)
	}

	var sessID, runID string
	if err := db.QueryRow(`INSERT INTO sessions (id,status) VALUES ($1,'open') RETURNING id`, ids.New()).Scan(&sessID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(
		`INSERT INTO runs (id,session_id,status,workspace_dir) VALUES ($1,$2,'completed','') RETURNING id`,
		ids.New(), sessID,
	).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	resp, err = http.Get(srv.URL + "/v1/runs/" + runID + "/events?after=nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad after status=%d", resp.StatusCode)
	}
	resp, err = http.Post(srv.URL+"/v1/runs/"+runID+"/cancel", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("cancel terminal status=%d", resp.StatusCode)
	}
	resp, err = http.Post(
		srv.URL+"/v1/runs/"+runID+"/decisions",
		"application/json",
		bytes.NewReader([]byte(`{"seq":1,"allow":true}`)),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("decide terminal status=%d", resp.StatusCode)
	}
}

func TestSSEStopsAfterTerminalEvent(t *testing.T) {
	srv, db := testMux(t)
	ctx := context.Background()
	var sessID, runID string
	if err := db.QueryRow(`INSERT INTO sessions (id,status) VALUES ($1,'open') RETURNING id`, ids.New()).Scan(&sessID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(
		`INSERT INTO runs (id,session_id,status,workspace_dir) VALUES ($1,$2,'completed','') RETURNING id`,
		ids.New(), sessID,
	).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	ev := event.NewStore(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ev.Append(ctx, tx, runID, event.TypeRunCreated, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ev.Append(ctx, tx, runID, event.TypeRunCompleted, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/runs/"+runID+"/events?after=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("run.completed")) {
		t.Fatalf("body=%s", body)
	}
}

func TestSSEReconnectAfterSeqSkipsSeen(t *testing.T) {
	srv, db := testMux(t)
	ctx := context.Background()
	var sessID, runID string
	if err := db.QueryRow(`INSERT INTO sessions (id,status) VALUES ($1,'open') RETURNING id`, ids.New()).Scan(&sessID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(
		`INSERT INTO runs (id,session_id,status,workspace_dir) VALUES ($1,$2,'completed','') RETURNING id`,
		ids.New(), sessID,
	).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	ev := event.NewStore(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ev.Append(ctx, tx, runID, event.TypeRunCreated, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ev.Append(ctx, tx, runID, event.TypeRunCompleted, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/runs/"+runID+"/events?after=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("run.created")) {
		t.Fatalf("replayed seq 1: %s", body)
	}
	if !bytes.Contains(body, []byte("run.completed")) {
		t.Fatalf("body=%s", body)
	}
}

func TestSSEReconnectLastEventID(t *testing.T) {
	srv, db := testMux(t)
	ctx := context.Background()
	var sessID, runID string
	if err := db.QueryRow(`INSERT INTO sessions (id,status) VALUES ($1,'open') RETURNING id`, ids.New()).Scan(&sessID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(
		`INSERT INTO runs (id,session_id,status,workspace_dir) VALUES ($1,$2,'completed','') RETURNING id`,
		ids.New(), sessID,
	).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	ev := event.NewStore(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ev.Append(ctx, tx, runID, event.TypeRunCreated, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ev.Append(ctx, tx, runID, event.TypeRunCompleted, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/runs/"+runID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Last-Event-ID", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("run.created")) {
		t.Fatalf("replayed seq 1: %s", body)
	}
	if !bytes.Contains(body, []byte("run.completed")) {
		t.Fatalf("body=%s", body)
	}
}

func TestSSEStopsOnClientDisconnect(t *testing.T) {
	srv, db := testMux(t)
	var sessID, runID string
	if err := db.QueryRow(`INSERT INTO sessions (id,status) VALUES ($1,'open') RETURNING id`, ids.New()).Scan(&sessID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(
		`INSERT INTO runs (id,session_id,status,workspace_dir) VALUES ($1,$2,'running','') RETURNING id`,
		ids.New(), sessID,
	).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/runs/"+runID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if time.Since(start) > time.Second {
		t.Fatalf("disconnect too slow: %s", time.Since(start))
	}
}

func TestPostMessageRequiresText(t *testing.T) {
	srv, _ := testMux(t)
	resp, err := http.Post(srv.URL+"/v1/sessions", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sess session.Session
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		t.Fatal(err)
	}
	resp, err = http.Post(
		srv.URL+"/v1/sessions/"+sess.ID+"/messages",
		"application/json",
		bytes.NewReader([]byte(`{"text":"  "}`)),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("text_required")) {
		t.Fatalf("body=%s", body)
	}
}

func TestSessionListTitleAndDelete(t *testing.T) {
	srv, db := testMux(t)
	sessID := ids.New()
	runID := ids.New()
	if err := db.QueryRow(`INSERT INTO sessions (id,status) VALUES ($1,'open') RETURNING id`, sessID).Scan(&sessID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(
		`INSERT INTO runs (id,session_id,status,workspace_dir) VALUES ($1,$2,'completed','') RETURNING id`,
		runID, sessID,
	).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO events (run_id,seq,type,payload) VALUES ($1,1,'message.user','{"text":"列表标题探测"}')`,
		runID,
	); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(srv.URL + "/v1/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var items []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.ID == sessID {
			found = true
			if item.Title != "列表标题探测" {
				t.Fatalf("title=%q", item.Title)
			}
		}
	}
	if !found {
		t.Fatal("session missing from list")
	}

	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/v1/sessions/"+sessID, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status=%d", resp.StatusCode)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sessions WHERE id=$1`, sessID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("session still present")
	}
}
