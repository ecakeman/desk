package run

import (
	"context"
	"strings"
	"unicode/utf8"

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

// ask 一次模型回合：钉稳定 system、把 phase 放在消息尾部、选槽并记录 delta。
func (s *Service) ask(ctx context.Context, runID string, snapshot *prompt.Snapshot, in worker.In) (*worker.Out, error) {
	in.RunID = runID
	if in.Phase == "review" {
		extra := s.attachReview(ctx, runID)
		if len(extra) > 0 {
			in.Messages = append(in.Messages, extra...)
		}
	}
	s.applySlot(&in)
	in.System = snapshot.System()
	in.Runtime = snapshot.Runtime(in.Phase)
	in.PromptHash = snapshot.Hash()
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
