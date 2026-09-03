package run

import (
	"context"
	"strings"
	"unicode/utf8"

	"desk/internal/ctxmgr"
	"desk/internal/event"
	"desk/internal/prompt"
	"desk/internal/worker"
)

// slotOf：plan/review 用 Pro，其余用 Flash。
func slotOf(phase string) string {
	if phase == "plan" || phase == "review" {
		return "pro"
	}
	return "flash"
}

// applySlot 按 phase 填 Worker 的模型槽位与 API 地址。
func (s *Service) applySlot(in *worker.In) {
	cfg := s.Flash
	in.Model = "flash"
	if in.Phase == "plan" || in.Phase == "review" {
		cfg = s.Pro
		in.Model = "pro"
	}
	if in.Phase == "" {
		in.Phase = "act"
	}
	in.APIModel = cfg.Model
	in.BaseURL = cfg.BaseURL
	in.APIKey = cfg.APIKey
}

func (s *Service) contextMgr() *ctxmgr.Manager {
	if s.Context == nil {
		s.Context = ctxmgr.New(s.Events, s.Index, ctxmgr.Settings{
			WindowTokens: 1_000_000,
			TotalTokens:  3_000_000,
			PromptsDir:   s.PromptsDir,
		})
	}
	if s.Context.Index == nil {
		s.Context.Index = s.Index
	}
	if s.Context.Settings.PromptsDir == "" {
		s.Context.Settings.PromptsDir = s.PromptsDir
	}
	return s.Context
}

// ask 一次模型回合：ContextManager 组装权威 snapshot，钉 system，phase 放尾部。
func (s *Service) ask(ctx context.Context, runID, sessionID, workspace string, snapshot *prompt.Snapshot, in worker.In) (*worker.Out, error) {
	in.RunID = runID
	pending := ""
	if in.T == "tool.result" || in.T == "tool.denied" {
		pending = in.ID
	}
	cm := s.contextMgr()
	asm, err := cm.Prepare(ctx, ctxmgr.PrepareIn{
		SessionID:    sessionID,
		RunID:        runID,
		Workspace:    workspace,
		Phase:        in.Phase,
		PromptHash:   snapshot.Hash(),
		Runtime:      snapshot.Runtime(in.Phase),
		PendingTool:  pending,
		WantRetrieve: in.Phase == "review",
	})
	if err != nil {
		return nil, err
	}
	if in.T == "turn.start" {
		in.Messages = asm.Messages
	} else if asm.Rebuild {
		replaced := append([]map[string]any{}, asm.Messages...)
		if rt := snapshot.Runtime(in.Phase); rt != "" {
			replaced = append(replaced, map[string]any{"role": "user", "content": rt})
		}
		if _, err := s.Worker.Handle(worker.In{
			T:        "context.replace",
			RunID:    runID,
			Messages: replaced,
			System:   snapshot.System(),
		}, nil); err != nil {
			return nil, err
		}
		in.SkipRuntime = true
		in.Messages = nil
	} else if in.Phase == "review" && len(asm.Layers.Retrieval) > 0 {
		in.Messages = asm.Layers.Retrieval
	}
	s.applySlot(&in)
	in.System = snapshot.System()
	in.Runtime = snapshot.Runtime(in.Phase)
	in.PromptHash = snapshot.Hash()
	if in.SkipRuntime {
		in.Runtime = ""
	}
	out, err := s.Worker.Handle(in, func(out worker.Out) error {
		if out.T == "model.usage" {
			_, err := s.appendOne(ctx, runID, event.TypeModelUsage, map[string]any{
				"model":         in.Model,
				"api_model":     in.APIModel,
				"phase":         in.Phase,
				"prompt_hash":   in.PromptHash,
				"input_tokens":  out.InputTokens,
				"output_tokens": out.OutputTokens,
				"cached_tokens": out.CachedTokens,
			})
			return err
		}
		if out.T != "message.delta" || out.Text == "" {
			return nil
		}
		_, err := s.appendOne(ctx, runID, event.TypeMessageDelta, map[string]string{
			"text":        out.Text,
			"model":       in.Model,
			"phase":       in.Phase,
			"prompt_hash": in.PromptHash,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	applied := asm.Applied
	applied.SkipRuntime = in.SkipRuntime
	if _, err := s.appendOne(ctx, runID, event.TypeContextApplied, applied); err != nil {
		return nil, err
	}
	if len(asm.Applied.Retrieval) > 0 {
		brief := make([]map[string]any, 0, len(asm.Applied.Retrieval))
		for _, h := range asm.Applied.Retrieval {
			brief = append(brief, map[string]any{"run_id": h.RunID, "seq": h.Seq, "kind": h.Kind})
		}
		_, _ = s.appendOne(ctx, runID, event.TypeMemoryRetrieved, map[string]any{
			"phase": in.Phase,
			"hits":  brief,
		})
	}
	if in.Phase == "review" {
		if _, err := s.appendOne(ctx, runID, event.TypeReviewCompleted, map[string]any{
			"model":       in.Model,
			"phase":       in.Phase,
			"summary":     reviewSummary(out),
			"continue":    out.T == "tool.request",
			"prompt_hash": in.PromptHash,
		}); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func reviewSummary(out *worker.Out) string {
	if out == nil {
		return "review returned no result"
	}
	var summary string
	switch out.T {
	case "tool.request":
		summary = "continue with " + out.Name
	case "turn.finish":
		summary = out.Text
	case "turn.fail":
		summary = out.Error
	default:
		summary = out.T
	}
	summary = strings.TrimSpace(summary)
	if utf8.RuneCountInString(summary) > 240 {
		summary = string([]rune(summary)[:240])
	}
	return summary
}

// InspectContext 进程内 assembled，否则从 context.applied 重建。
func (s *Service) InspectContext(ctx context.Context, sessionID, runID string) (ctxmgr.Assembly, string, bool) {
	return s.contextMgr().Inspect(ctx, sessionID, runID)
}
