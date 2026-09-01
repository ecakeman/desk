package run

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"desk/internal/event"
	"desk/internal/plugin"
	"desk/internal/testdb"
	"desk/internal/worker"
)

type askRec struct {
	T     string
	Phase string
	Model string
}

type pingPlugin struct{}

func (pingPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name: "ping",
		Risk: "read",
		Ops:  []plugin.OpSpec{{Name: "ok", Description: "noop", Parameters: json.RawMessage(`{}`)}},
	}
}

func (pingPlugin) Exec(context.Context, string, map[string]any) (json.RawMessage, error) {
	return json.RawMessage(`{"ok":true}`), nil
}

type budgetStub struct {
	mu            sync.Mutex
	asks          []askRec
	tools         int
	finishReviews int
	finishTools   int
	failReview    bool
}

func (b *budgetStub) Handle(in worker.In, _ func(worker.Out) error) (*worker.Out, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.asks = append(b.asks, askRec{T: in.T, Phase: in.Phase, Model: in.Model})
	nReview := 0
	for _, a := range b.asks {
		if a.Phase == "review" {
			nReview++
		}
	}
	ping := &worker.Out{
		T: "tool.request", ID: "1", Name: "ping.ok", Args: map[string]any{},
	}
	switch in.T {
	case "turn.start":
		return ping, nil
	case "tool.result", "tool.denied":
		if in.T == "tool.result" {
			b.tools++
		}
		if in.Phase == "review" && b.failReview {
			return &worker.Out{T: "turn.fail", Error: "review_broken"}, nil
		}
		if in.Phase == "review" && b.finishReviews > 0 && nReview >= b.finishReviews {
			return &worker.Out{T: "turn.finish", Text: "ok"}, nil
		}
		if b.finishTools > 0 && b.tools >= b.finishTools {
			return &worker.Out{T: "turn.finish", Text: "ok"}, nil
		}
		return ping, nil
	default:
		return &worker.Out{T: "turn.finish", Text: "ok"}, nil
	}
}

func (b *budgetStub) Done(string) {}

func (b *budgetStub) proReviews() []askRec {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []askRec
	for _, a := range b.asks {
		if a.Phase == "review" && a.Model == "pro" {
			out = append(out, a)
		}
	}
	return out
}

func budgetEnv(t *testing.T, stub *budgetStub) *Service {
	t.Helper()
	db := testdb.Open(t)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	reg, err := plugin.Load(filepath.Join(root, "plugins"), work)
	if err != nil {
		t.Fatal(err)
	}
	reg.Put(pingPlugin{})
	svc := NewService(db, event.NewStore(db))
	svc.Plugins = reg
	svc.Worker = stub
	svc.PromptsDir = filepath.Join(root, "prompts")
	return svc
}

func TestBoundReview(t *testing.T) {
	if boundReview("review", 0) != "review" || boundReview("review", 1) != "review" {
		t.Fatal("first two reviews must pass")
	}
	if boundReview("review", 2) != "act" || boundReview("review", 3) != "act" {
		t.Fatal("exhausted budget must stay on act")
	}
	if boundReview("act", 2) != "act" || boundReview("plan", 2) != "plan" {
		t.Fatal("non-review phases must not change")
	}
}

func TestReviewBudgetOneReviewFinishes(t *testing.T) {
	stub := &budgetStub{finishReviews: 1}
	svc := budgetEnv(t, stub)
	sess := testdb.InsertSession(t, svc.DB)
	runID, err := svc.PostUserMessage(context.Background(), sess, "one review", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, svc.DB, runID, StatusCompleted)
	got := stub.proReviews()
	if len(got) != 1 {
		t.Fatalf("pro review calls=%d %+v", len(got), stub.asks)
	}
}

func TestReviewBudgetTwoReviewsFinishes(t *testing.T) {
	stub := &budgetStub{finishReviews: 2}
	svc := budgetEnv(t, stub)
	sess := testdb.InsertSession(t, svc.DB)
	runID, err := svc.PostUserMessage(context.Background(), sess, "two reviews", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, svc.DB, runID, StatusCompleted)
	got := stub.proReviews()
	if len(got) != 2 {
		t.Fatalf("pro review calls=%d", len(got))
	}
}

func TestReviewBudgetBlocksThirdProReview(t *testing.T) {
	// 第 5、10 次成功工具会请求 review；第 15 次若预算生效则仍是 act。
	stub := &budgetStub{finishTools: 15}
	svc := budgetEnv(t, stub)
	sess := testdb.InsertSession(t, svc.DB)
	runID, err := svc.PostUserMessage(context.Background(), sess, "block third", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, svc.DB, runID, StatusCompleted)
	got := stub.proReviews()
	if len(got) != 2 {
		t.Fatalf("pro review calls=%d want 2; asks=%+v", len(got), stub.asks)
	}
	stub.mu.Lock()
	var fifteenth *askRec
	nResult := 0
	for i := range stub.asks {
		if stub.asks[i].T == "tool.result" {
			nResult++
			if nResult == 15 {
				fifteenth = &stub.asks[i]
			}
		}
	}
	stub.mu.Unlock()
	if fifteenth == nil {
		t.Fatal("missing 15th tool.result")
	}
	if fifteenth.Phase == "review" || fifteenth.Model == "pro" && fifteenth.Phase == "review" {
		t.Fatalf("3rd review launched: %+v", *fifteenth)
	}
	if fifteenth.Phase != "act" || fifteenth.Model != "flash" {
		t.Fatalf("15th ask = %+v want act/flash", *fifteenth)
	}
	var nReviewEvents int
	if err := svc.DB.QueryRow(
		`SELECT COUNT(*) FROM events WHERE run_id=$1 AND type=$2`,
		runID, event.TypeReviewCompleted,
	).Scan(&nReviewEvents); err != nil {
		t.Fatal(err)
	}
	if nReviewEvents != 2 {
		t.Fatalf("review.completed=%d want 2", nReviewEvents)
	}
}

func TestReviewBudgetReviewFailStillFails(t *testing.T) {
	stub := &budgetStub{failReview: true}
	svc := budgetEnv(t, stub)
	sess := testdb.InsertSession(t, svc.DB)
	runID, err := svc.PostUserMessage(context.Background(), sess, "review fail", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, svc.DB, runID, StatusFailed)
	if len(stub.proReviews()) != 1 {
		t.Fatalf("pro reviews=%d", len(stub.proReviews()))
	}
}

func TestReviewBudgetPlanActFinishUnchanged(t *testing.T) {
	stub := &budgetStub{finishTools: 1}
	svc := budgetEnv(t, stub)
	sess := testdb.InsertSession(t, svc.DB)
	runID, err := svc.PostUserMessage(context.Background(), sess, "no review", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, svc.DB, runID, StatusCompleted)
	if len(stub.proReviews()) != 0 {
		t.Fatalf("unexpected review %+v", stub.proReviews())
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.asks) < 2 || stub.asks[0].Phase != "plan" || stub.asks[1].Phase != "act" {
		t.Fatalf("asks=%+v", stub.asks)
	}
}
