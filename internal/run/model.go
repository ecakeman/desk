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
func (service *Service) applySlot(workerInput *worker.In) {
	modelSlot := service.Flash
	workerInput.Model = "flash"
	if workerInput.Phase == "plan" || workerInput.Phase == "review" {
		modelSlot = service.Pro
		workerInput.Model = "pro"
	}
	if workerInput.Phase == "" {
		workerInput.Phase = "act"
	}
	workerInput.APIModel = modelSlot.Model
	workerInput.BaseURL = modelSlot.BaseURL
	workerInput.APIKey = modelSlot.APIKey
}

func (service *Service) contextMgr() *ctxmgr.Manager {
	if service.Context == nil {
		service.Context = ctxmgr.New(service.Events, service.Index, ctxmgr.Settings{
			WindowTokens: 1_000_000,
			TotalTokens:  3_000_000,
			PromptsDir:   service.PromptsDir,
		})
	}
	if service.Context.Index == nil {
		service.Context.Index = service.Index
	}
	if service.Context.Settings.PromptsDir == "" {
		service.Context.Settings.PromptsDir = service.PromptsDir
	}
	return service.Context
}

// ask 一次模型回合：ContextManager 组装权威 snapshot，钉 system，phase 放尾部。
func (service *Service) ask(requestContext context.Context, runID, sessionID, workspace string, promptSnapshot *prompt.Snapshot, workerInput worker.In) (*worker.Out, error) {
	workerInput.RunID = runID
	pendingToolID := ""
	if workerInput.T == "tool.result" || workerInput.T == "tool.denied" {
		pendingToolID = workerInput.ID
	}
	contextManager := service.contextMgr()
	var toolSpecs []ctxmgr.ToolSpec
	if service.Plugins != nil && promptSnapshot != nil {
		for _, tool := range promptSnapshot.ApplyTools(service.Plugins.Tools()) {
			toolSpecs = append(toolSpecs, ctxmgr.ToolSpec{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters})
		}
	}
	contextAssembly, err := contextManager.Prepare(requestContext, ctxmgr.PrepareIn{
		SessionID:    sessionID,
		RunID:        runID,
		Workspace:    workspace,
		Phase:        workerInput.Phase,
		PromptHash:   promptSnapshot.Hash(),
		Runtime:      promptSnapshot.Runtime(workerInput.Phase),
		System:       promptSnapshot.System(),
		Tools:        toolSpecs,
		PendingTool:  pendingToolID,
		WantRetrieve: workerInput.Phase == "review",
	})
	if err != nil {
		return nil, err
	}
	if workerInput.T == "turn.start" {
		workerInput.Messages = contextAssembly.Messages
	} else if contextAssembly.Rebuild {
		replacedMessages := append([]map[string]any{}, contextAssembly.Messages...)
		if runtimeText := promptSnapshot.Runtime(workerInput.Phase); runtimeText != "" {
			replacedMessages = append(replacedMessages, map[string]any{"role": "user", "content": runtimeText})
		}
		if _, err := service.Worker.Handle(worker.In{
			T:        "context.replace",
			RunID:    runID,
			Messages: replacedMessages,
			System:   promptSnapshot.System(),
		}, nil); err != nil {
			return nil, err
		}
		workerInput.SkipRuntime = true
		workerInput.Messages = nil
	} else if workerInput.Phase == "review" && len(contextAssembly.Layers.Retrieval) > 0 {
		workerInput.Messages = contextAssembly.Layers.Retrieval
	}
	service.applySlot(&workerInput)
	workerInput.System = promptSnapshot.System()
	workerInput.Runtime = promptSnapshot.Runtime(workerInput.Phase)
	workerInput.PromptHash = promptSnapshot.Hash()
	if workerInput.SkipRuntime {
		workerInput.Runtime = ""
	}
	workerOutput, err := service.Worker.Handle(workerInput, func(streamedOutput worker.Out) error {
		if streamedOutput.T == "model.usage" {
			_, err := service.appendOne(requestContext, runID, event.TypeModelUsage, map[string]any{
				"model":         workerInput.Model,
				"api_model":     workerInput.APIModel,
				"phase":         workerInput.Phase,
				"prompt_hash":   workerInput.PromptHash,
				"input_tokens":  streamedOutput.InputTokens,
				"output_tokens": streamedOutput.OutputTokens,
				"cached_tokens": streamedOutput.CachedTokens,
			})
			return err
		}
		if streamedOutput.T != "message.delta" || streamedOutput.Text == "" {
			return nil
		}
		_, err := service.appendOne(requestContext, runID, event.TypeMessageDelta, map[string]string{
			"text":        streamedOutput.Text,
			"model":       workerInput.Model,
			"phase":       workerInput.Phase,
			"prompt_hash": workerInput.PromptHash,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	appliedContext := contextAssembly.Applied
	appliedContext.SkipRuntime = workerInput.SkipRuntime
	if _, err := service.appendOne(requestContext, runID, event.TypeContextApplied, appliedContext); err != nil {
		return nil, err
	}
	if len(contextAssembly.Applied.Retrieval) > 0 {
		retrievalBrief := make([]map[string]any, 0, len(contextAssembly.Applied.Retrieval))
		for _, memoryHit := range contextAssembly.Applied.Retrieval {
			retrievalBrief = append(retrievalBrief, map[string]any{"run_id": memoryHit.RunID, "seq": memoryHit.Seq, "kind": memoryHit.Kind})
		}
		_, _ = service.appendOne(requestContext, runID, event.TypeMemoryRetrieved, map[string]any{
			"phase": workerInput.Phase,
			"hits":  retrievalBrief,
		})
	}
	if workerInput.Phase == "review" {
		if _, err := service.appendOne(requestContext, runID, event.TypeReviewCompleted, map[string]any{
			"model":       workerInput.Model,
			"phase":       workerInput.Phase,
			"summary":     reviewSummary(workerOutput),
			"continue":    workerOutput.T == "tool.request",
			"prompt_hash": workerInput.PromptHash,
		}); err != nil {
			return nil, err
		}
	}
	return workerOutput, nil
}

func reviewSummary(workerOutput *worker.Out) string {
	if workerOutput == nil {
		return "review returned no result"
	}
	var summary string
	switch workerOutput.T {
	case "tool.request":
		summary = "continue with " + workerOutput.Name
	case "turn.finish":
		summary = workerOutput.Text
	case "turn.fail":
		summary = workerOutput.Error
	default:
		summary = workerOutput.T
	}
	summary = strings.TrimSpace(summary)
	if utf8.RuneCountInString(summary) > 240 {
		summary = string([]rune(summary)[:240])
	}
	return summary
}

// InspectContext 进程内 assembled，否则从 context.applied 重建（非 byte replay）。
func (service *Service) InspectContext(requestContext context.Context, sessionID, runID string) (ctxmgr.Assembly, string, bool) {
	return service.contextMgr().Inspect(requestContext, sessionID, runID)
}
