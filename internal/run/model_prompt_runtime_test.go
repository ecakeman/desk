package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"desk/internal/config"
	"desk/internal/event"
	"desk/internal/prompt"
	"desk/internal/testdb"
	"desk/internal/worker"
)

func TestRuntimeContractModelRouting(t *testing.T) {
	w := &recWorker{fn: func(in worker.In) *worker.Out {
		if in.T == "turn.start" {
			return &worker.Out{T: "tool.request", ID: "1", Name: "ping.ok", Args: map[string]any{}}
		}
		return &worker.Out{T: "turn.finish", Text: "ok"}
	}}
	work := t.TempDir()
	svc, db := contractEnv(t, w, work)
	svc.Flash = config.ModelConfig{Model: "flash-stub", BaseURL: "http://flash.example/v1"}
	svc.Pro = config.ModelConfig{Model: "pro-stub", BaseURL: "http://pro.example/v1"}
	sess := testdb.InsertSession(t, db)
	runID := postWait(t, svc, db, sess, "route", work, StatusCompleted)
	var plan, act int
	for _, in := range w.snapshot() {
		switch in.Phase {
		case "plan":
			plan++
			if in.Model != "pro" || in.APIModel != "pro-stub" || in.BaseURL != "http://pro.example/v1" {
				t.Fatalf("plan slot %+v", in)
			}
		case "act":
			act++
			if in.Model != "flash" || in.APIModel != "flash-stub" || in.BaseURL != "http://flash.example/v1" {
				t.Fatalf("act slot %+v", in)
			}
		}
	}
	if plan == 0 || act == 0 {
		t.Fatalf("missing plan/act asks plan=%d act=%d", plan, act)
	}
	planSlot, actSlot, reviewSlot := "", "", "none"
	for _, in := range w.snapshot() {
		switch in.Phase {
		case "plan":
			if planSlot == "" {
				planSlot = in.Model + "/" + in.APIModel
			}
		case "act":
			if actSlot == "" {
				actSlot = in.Model + "/" + in.APIModel
			}
		case "review":
			if reviewSlot == "none" {
				reviewSlot = in.Model + "/" + in.APIModel
			}
		}
	}
	for _, e := range loadEvents(t, db, runID) {
		if e.Type != event.TypeModelUsage {
			continue
		}
		var p struct {
			Model    string `json:"model"`
			APIModel string `json:"api_model"`
			Phase    string `json:"phase"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		switch p.Phase {
		case "plan", "review":
			if p.Model != "pro" || p.APIModel != "pro-stub" {
				t.Fatalf("pro usage mixed: %+v", p)
			}
		default:
			if p.Model != "flash" || p.APIModel != "flash-stub" {
				t.Fatalf("flash usage mixed: %+v", p)
			}
		}
	}
	assertEventConsistency(t, db, runID)
	report(t, "model routing",
		"plan_model", "pro",
		"act_model", "flash",
		"review_model", "pro",
		"observed_plan_slot", planSlot,
		"observed_act_slot", actSlot,
		"observed_review_slot", reviewSlot,
	)
}

func TestRuntimeContractReviewBudget(t *testing.T) {
	stub := &budgetStub{finishTools: 15}
	svc := budgetEnv(t, stub)
	sess := testdb.InsertSession(t, svc.DB)
	runID, err := svc.PostUserMessage(context.Background(), sess, "budget", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, svc.DB, runID, StatusCompleted)
	if n := len(stub.proReviews()); n != 2 {
		t.Fatalf("pro reviews=%d want 2", n)
	}
	stub.mu.Lock()
	var fifteenth *askRec
	nResult := 0
	nReviewAsk := 0
	for i := range stub.asks {
		if stub.asks[i].Phase == "review" {
			nReviewAsk++
		}
		if stub.asks[i].T == "tool.result" {
			nResult++
			if nResult == 15 {
				fifteenth = &stub.asks[i]
			}
		}
	}
	stub.mu.Unlock()
	if fifteenth == nil || fifteenth.Phase != "act" {
		t.Fatalf("overflow ask=%+v", fifteenth)
	}
	assertEventConsistency(t, svc.DB, runID)
	report(t, "review budget",
		"review_requested", fmt.Sprintf("%d", nReviewAsk),
		"pro_review_count", fmt.Sprintf("%d", len(stub.proReviews())),
		"review_limit", "2",
		"overflow_route", fifteenth.Phase+"/"+fifteenth.Model,
	)
}

func TestRuntimeContractPromptSnapshot(t *testing.T) {
	dir := copyPrompts(t)
	work := t.TempDir()
	w := &recWorker{fn: func(in worker.In) *worker.Out {
		text := lastUser(in)
		if in.T == "turn.start" && text == "snap-a" {
			return &worker.Out{
				T: "tool.request", ID: "w1", Name: "fs.write",
				Args: map[string]any{"path": "a.txt", "content": "a"},
			}
		}
		return &worker.Out{T: "turn.finish", Text: "ok"}
	}}
	svc, db := contractEnv(t, w, work)
	svc.PromptsDir = dir
	sess := testdb.InsertSession(t, db)

	before, err := prompt.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	runA := postWait(t, svc, db, sess, "snap-a", work, StatusWaitingApproval)

	if err := os.WriteFile(filepath.Join(dir, "system", "base.md"), []byte("Desk SNAPSHOT-B-ONLY\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := prompt.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if after.Hash() == before.Hash() {
		t.Fatal("catalog hash must change")
	}

	runB := postWait(t, svc, db, sess, "snap-b", work, StatusCompleted)
	seq := requestedSeq(t, db, runA)
	if err := svc.Decide(context.Background(), runA, seq, true); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, db, runA, StatusCompleted)

	hashA, hashB := appliedHash(t, loadEvents(t, db, runA)), appliedHash(t, loadEvents(t, db, runB))
	if hashA != before.Hash() {
		t.Fatalf("run A hash=%s want %s", hashA, before.Hash())
	}
	if hashB != after.Hash() {
		t.Fatalf("run B hash=%s want %s", hashB, after.Hash())
	}
	if hashA == hashB {
		t.Fatal("runs must not share snapshot")
	}
	for _, in := range w.snapshot() {
		if in.RunID == runA && in.PromptHash != "" && in.PromptHash != before.Hash() {
			t.Fatalf("run A drifted to %s", in.PromptHash)
		}
	}
	assertEventConsistency(t, db, runA)
	assertEventConsistency(t, db, runB)
	report(t, "prompt snapshot",
		"run_a_prompt_version", shortHash(before.Hash()),
		"prompt_version_after_change", shortHash(after.Hash()),
		"run_a_observed_version", shortHash(hashA),
		"run_b_observed_version", shortHash(hashB),
	)
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func appliedHash(t *testing.T, events []event.Event) string {
	t.Helper()
	for _, e := range events {
		if e.Type == event.TypePromptApplied {
			h := payloadHash(e.Payload)
			if h == "" {
				t.Fatal("empty prompt.applied hash")
			}
			return h
		}
	}
	t.Fatal("missing prompt.applied")
	return ""
}
