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
func (service *Service) Drive(requestContext context.Context, runID string) error {
	var sessionID, workspace string
	if err := service.DB.QueryRowContext(requestContext,
		`SELECT session_id, workspace_dir FROM runs WHERE id=$1`, runID,
	).Scan(&sessionID, &workspace); err != nil {
		return err
	}
	defer service.Worker.Done(runID)
	promptSnapshot, err := prompt.Load(service.PromptsDir)
	if err != nil {
		return err
	}
	if _, err := service.appendOne(requestContext, runID, event.TypePromptApplied, map[string]any{
		"hash":  promptSnapshot.Hash(),
		"files": promptSnapshot.Files(),
	}); err != nil {
		return err
	}
	var tools []any
	for _, tool := range promptSnapshot.ApplyTools(service.Plugins.Tools()) {
		tools = append(tools, tool)
	}
	flashCallCount := 0
	toolFailureCount := 0
	proReviewCount := 0
	currentPhase := "plan"
	currentModelSlot := "pro"

	ask := func(workerInput worker.In) (*worker.Out, error) {
		workerInput.Phase = boundReview(workerInput.Phase, proReviewCount)
		currentPhase = workerInput.Phase
		if workerInput.Phase == "review" {
			proReviewCount++
		}
		workerOutput, err := service.ask(requestContext, runID, sessionID, workspace, promptSnapshot, workerInput)
		if err != nil {
			return nil, err
		}
		currentModelSlot = slotOf(currentPhase)
		return workerOutput, nil
	}

	workerOutput, err := ask(worker.In{
		T:     "turn.start",
		RunID: runID,
		Tools: tools,
		Phase: currentPhase,
	})
	if err != nil {
		return err
	}

	for i := 0; i < 64; i++ {
		switch workerOutput.T {
		case "tool.request":
			toolResult, err := service.runTool(requestContext, runID, currentPhase, workerOutput)
			toolCallID := workerOutput.ID
			if errors.Is(err, errDenied) {
				workerOutput, err = ask(worker.In{T: "tool.denied", ID: toolCallID, Phase: currentPhase})
				if err != nil {
					return err
				}
				continue
			}
			var toolFailure toolFailedError
			if errors.As(err, &toolFailure) {
				toolFailureCount++
				if toolFailureCount >= 2 {
					currentPhase = "review"
				} else {
					currentPhase = "act"
				}
				workerOutput, err = ask(worker.In{
					T:     "tool.result",
					RunID: runID,
					ID:    toolCallID,
					OK:    false,
					Error: toolFailure.msg,
					Phase: currentPhase,
				})
				if err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			toolFailureCount = 0
			flashCallCount++
			if flashCallCount%5 == 0 {
				currentPhase = "review"
			} else {
				currentPhase = "act"
			}
			workerOutput, err = ask(worker.In{
				T:     "tool.result",
				RunID: runID,
				ID:    toolCallID,
				OK:    true,
				Data:  toolResult,
				Phase: currentPhase,
			})
			if err != nil {
				return err
			}
		case "turn.finish":
			return service.finish(requestContext, runID, workerOutput.Text, currentModelSlot, currentPhase, promptSnapshot.Hash())
		case "turn.fail":
			return fmt.Errorf("%s", workerOutput.Error)
		default:
			return fmt.Errorf("unknown worker t: %s", workerOutput.T)
		}
	}
	return fmt.Errorf("tool_limit")
}

// maxProReview 是单个 Run 内 phase=review 且走 Pro 槽的次数上限。
const maxProReview = 2

// boundReview：Pro review 预算用尽后不再进入 review，改走 act，不强制 finish。
func boundReview(phase string, proReviewCount int) string {
	if phase == "review" && proReviewCount >= maxProReview {
		return "act"
	}
	return phase
}
