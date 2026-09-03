package run

import (
	"context"
	"errors"
	"fmt"

	"desk/internal/event"
	"desk/internal/prompt"
	"desk/internal/worker"
)

// Drive 钉一份 Prompt Snapshot，由 ContextManager 组装上下文，按 plan → tool/ask → 终态转圈。
// HTTP 调不到这里。ctx 取消走 Interrupt，其它错误走 Fail。
func (s *Service) Drive(ctx context.Context, runID string) error {
	var sessionID, workspace string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT session_id, workspace_dir FROM runs WHERE id=$1`, runID,
	).Scan(&sessionID, &workspace); err != nil {
		return err
	}
	defer s.Worker.Done(runID)
	snapshot, err := prompt.Load(s.PromptsDir)
	if err != nil {
		return err
	}
	if _, err := s.appendOne(ctx, runID, event.TypePromptApplied, map[string]any{
		"hash":  snapshot.Hash(),
		"files": snapshot.Files(),
	}); err != nil {
		return err
	}
	var tools []any
	for _, t := range snapshot.ApplyTools(s.Plugins.Tools()) {
		tools = append(tools, t)
	}
	nFlash := 0
	nFail := 0
	nProReview := 0
	phase := "plan"
	slot := "pro"

	ask := func(in worker.In) (*worker.Out, error) {
		in.Phase = boundReview(in.Phase, nProReview)
		phase = in.Phase
		if in.Phase == "review" {
			nProReview++
		}
		out, err := s.ask(ctx, runID, sessionID, workspace, snapshot, in)
		if err != nil {
			return nil, err
		}
		slot = slotOf(phase)
		return out, nil
	}

	out, err := ask(worker.In{
		T:     "turn.start",
		RunID: runID,
		Tools: tools,
		Phase: phase,
	})
	if err != nil {
		return err
	}

	for i := 0; i < 64; i++ {
		switch out.T {
		case "tool.request":
			data, err := s.runTool(ctx, runID, phase, out)
			id := out.ID
			if errors.Is(err, errDenied) {
				out, err = ask(worker.In{T: "tool.denied", ID: id, Phase: phase})
				if err != nil {
					return err
				}
				continue
			}
			var tf toolFailedError
			if errors.As(err, &tf) {
				nFail++
				if nFail >= 2 {
					phase = "review"
				} else {
					phase = "act"
				}
				out, err = ask(worker.In{
					T:     "tool.result",
					RunID: runID,
					ID:    id,
					OK:    false,
					Error: tf.msg,
					Phase: phase,
				})
				if err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			nFail = 0
			nFlash++
			if nFlash%5 == 0 {
				phase = "review"
			} else {
				phase = "act"
			}
			out, err = ask(worker.In{
				T:     "tool.result",
				RunID: runID,
				ID:    id,
				OK:    true,
				Data:  data,
				Phase: phase,
			})
			if err != nil {
				return err
			}
		case "turn.finish":
			return s.finish(ctx, runID, out.Text, slot, phase, snapshot.Hash())
		case "turn.fail":
			return fmt.Errorf("%s", out.Error)
		default:
			return fmt.Errorf("unknown worker t: %s", out.T)
		}
	}
	return fmt.Errorf("tool_limit")
}

// maxProReview 是单个 Run 内 phase=review 且走 Pro 槽的次数上限。
const maxProReview = 2

// boundReview：Pro review 预算用尽后不再进入 review，改走 act，不强制 finish。
func boundReview(phase string, nProReview int) string {
	if phase == "review" && nProReview >= maxProReview {
		return "act"
	}
	return phase
}
