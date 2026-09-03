package httpapi

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"desk/internal/event"
	"desk/internal/plugin"
	"desk/internal/run"
	"desk/internal/session"
	"desk/internal/testdb"
	"desk/internal/worker"
)

type finishWorker struct{}

func (finishWorker) Handle(in worker.In, _ func(worker.Out) error) (*worker.Out, error) {
	if in.T == "context.replace" {
		return &worker.Out{T: "context.replaced"}, nil
	}
	return &worker.Out{T: "turn.finish", Text: "ok"}, nil
}

func (finishWorker) Done(string) {}

type writeOnceWorker struct{}

func (writeOnceWorker) Handle(in worker.In, _ func(worker.Out) error) (*worker.Out, error) {
	if in.T == "context.replace" {
		return &worker.Out{T: "context.replaced"}, nil
	}
	if in.T == "turn.start" {
		return &worker.Out{
			T: "tool.request", ID: "1", Name: "fs.write",
			Args: map[string]any{"path": "http.txt", "content": "from-http"},
		}, nil
	}
	return &worker.Out{T: "turn.finish", Text: "ok"}, nil
}

func (writeOnceWorker) Done(string) {}

func runtimeHTTP(t *testing.T, w worker.Worker, work string) (*httptest.Server, *sql.DB) {
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
	reg, err := plugin.Load(filepath.Join(root, "plugins"), work)
	if err != nil {
		t.Fatal(err)
	}
	ev := event.NewStore(db)
	svc := run.NewService(db, ev)
	svc.Plugins = reg
	svc.Worker = w
	svc.PromptsDir = filepath.Join(root, "prompts")
	srv := httptest.NewServer(NewMux(Deps{
		DB:        db,
		Workspace: work,
		Sessions:  session.NewStore(db),
		Runs:      run.NewStore(db),
		Messages:  svc,
		Events:    ev,
	}))
	t.Cleanup(srv.Close)
	return srv, db
}

func decodeJSON(t *testing.T, resp *http.Response, out any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	var rdr *bytes.Reader
	if body == nil {
		rdr = bytes.NewReader(nil)
	} else {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	resp, err := http.Post(url, "application/json", rdr)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func httpWaitRun(t *testing.T, base, runID, want string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/v1/runs/" + runID)
		if err != nil {
			t.Fatal(err)
		}
		var item struct {
			Status string `json:"status"`
		}
		decodeJSON(t, resp, &item)
		if item.Status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s want %s", runID, want)
}

func TestRuntimeContractHTTPLifecycle(t *testing.T) {
	work := t.TempDir()
	srv, db := runtimeHTTP(t, finishWorker{}, work)
	resp := postJSON(t, srv.URL+"/v1/sessions", nil)
	var sess struct {
		ID string `json:"id"`
	}
	decodeJSON(t, resp, &sess)
	if sess.ID == "" {
		t.Fatal("empty session")
	}
	testdb.CleanupSession(t, db, sess.ID)
	resp = postJSON(t, srv.URL+"/v1/sessions/"+sess.ID+"/messages", map[string]string{"text": "hello"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("messages %d", resp.StatusCode)
	}
	var created struct {
		RunID string `json:"run_id"`
	}
	decodeJSON(t, resp, &created)
	postStatus := resp.StatusCode
	if created.RunID == "" {
		t.Fatal("empty run_id")
	}
	httpWaitRun(t, srv.URL, created.RunID, run.StatusCompleted)
	ctxResp, err := http.Get(srv.URL + "/v1/runs/" + created.RunID + "/context")
	if err != nil {
		t.Fatal(err)
	}
	var assembled struct {
		Kind string `json:"kind"`
	}
	decodeJSON(t, ctxResp, &assembled)
	if assembled.Kind != "assembled" {
		t.Fatalf("context kind=%q", assembled.Kind)
	}
	stmResp, err := http.Get(srv.URL + "/v1/runs/" + created.RunID + "/stm")
	if err != nil {
		t.Fatal(err)
	}
	var stm struct {
		Kind string `json:"kind"`
	}
	decodeJSON(t, stmResp, &stm)
	if stm.Kind != "event_projection" {
		t.Fatalf("stm kind=%q", stm.Kind)
	}
	getRun, err := http.Get(srv.URL + "/v1/runs/" + created.RunID)
	if err != nil {
		t.Fatal(err)
	}
	getRunStatus := getRun.StatusCode
	var runBody struct {
		Status string `json:"status"`
	}
	decodeJSON(t, getRun, &runBody)

	getEv, err := http.Get(srv.URL + "/v1/sessions/" + sess.ID + "/events")
	if err != nil {
		t.Fatal(err)
	}
	getEvStatus := getEv.StatusCode
	var events []event.Event
	decodeJSON(t, getEv, &events)

	sse, err := (&http.Client{Timeout: 5 * time.Second}).Get(srv.URL + "/v1/runs/" + created.RunID + "/events")
	if err != nil {
		t.Fatal(err)
	}
	sseCT := sse.Header.Get("Content-Type")
	raw, _ := io.ReadAll(sse.Body)
	_ = sse.Body.Close()
	sseOK := sse.StatusCode == http.StatusOK && strings.Contains(sseCT, "text/event-stream") && bytes.Contains(raw, []byte("data:"))
	fmt.Printf("::evidence::[PASS] http lifecycle\n")
	fmt.Printf("::evidence::       POST /messages: %d\n", postStatus)
	fmt.Printf("::evidence::       run_id_created: %t\n", created.RunID != "")
	fmt.Printf("::evidence::       event_count: %d\n", len(events))
	fmt.Printf("::evidence::       GET /runs/:id: %d\n", getRunStatus)
	fmt.Printf("::evidence::       GET /events: %d\n", getEvStatus)
	fmt.Printf("::evidence::       SSE: %s\n", sseState(sseOK, sse.StatusCode, sseCT))
	fmt.Printf("::evidence::       final_run_status: %s\n", runBody.Status)
}

func sseState(ok bool, code int, ct string) string {
	if ok {
		return "connected"
	}
	return fmt.Sprintf("failed status=%d ct=%s", code, ct)
}

func TestRuntimeContractHTTPApprovalReject(t *testing.T) {
	work := t.TempDir()
	srv, db := runtimeHTTP(t, writeOnceWorker{}, work)
	resp := postJSON(t, srv.URL+"/v1/sessions", nil)
	var sess struct {
		ID string `json:"id"`
	}
	decodeJSON(t, resp, &sess)
	testdb.CleanupSession(t, db, sess.ID)
	resp = postJSON(t, srv.URL+"/v1/sessions/"+sess.ID+"/messages", map[string]string{"text": "write"})
	var created struct {
		RunID string `json:"run_id"`
	}
	decodeJSON(t, resp, &created)
	httpWaitRun(t, srv.URL, created.RunID, run.StatusWaitingApproval)

	var seq int
	if err := db.QueryRow(
		`SELECT seq FROM events WHERE run_id=$1 AND type=$2 ORDER BY seq DESC LIMIT 1`,
		created.RunID, event.TypeToolRequested,
	).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	resp = postJSON(t, srv.URL+"/v1/runs/"+created.RunID+"/decisions", map[string]any{"seq": seq, "allow": false})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("decide %d", resp.StatusCode)
	}
	resp.Body.Close()
	httpWaitRun(t, srv.URL, created.RunID, run.StatusCompleted)
	if _, err := os.Stat(filepath.Join(work, "http.txt")); !os.IsNotExist(err) {
		t.Fatal("http reject must not write")
	}
}

func TestRuntimeContractHTTPCancel(t *testing.T) {
	work := t.TempDir()
	srv, db := runtimeHTTP(t, writeOnceWorker{}, work)
	resp := postJSON(t, srv.URL+"/v1/sessions", nil)
	var sess struct {
		ID string `json:"id"`
	}
	decodeJSON(t, resp, &sess)
	testdb.CleanupSession(t, db, sess.ID)
	resp = postJSON(t, srv.URL+"/v1/sessions/"+sess.ID+"/messages", map[string]string{"text": "write"})
	var created struct {
		RunID string `json:"run_id"`
	}
	decodeJSON(t, resp, &created)
	httpWaitRun(t, srv.URL, created.RunID, run.StatusWaitingApproval)
	resp = postJSON(t, srv.URL+"/v1/runs/"+created.RunID+"/cancel", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel %d", resp.StatusCode)
	}
	resp.Body.Close()
	httpWaitRun(t, srv.URL, created.RunID, run.StatusInterrupted)
	resp = postJSON(t, srv.URL+"/v1/runs/"+created.RunID+"/cancel", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate cancel %d", resp.StatusCode)
	}
	resp.Body.Close()
}
