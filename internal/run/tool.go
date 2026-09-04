package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"desk/internal/approve"
	"desk/internal/event"
	"desk/internal/plugin"
	"desk/internal/skill"
	"desk/internal/worker"
)

var errDenied = errors.New("tool_denied")

type toolFailedError struct{ msg string }

func (e toolFailedError) Error() string { return e.msg }

// runTool 走 tool.requested → 批准 → plugin.Exec → completed/denied/failed。
func (service *Service) runTool(requestContext context.Context, runID, phase string, toolRequest *worker.Out) (json.RawMessage, error) {
	seq, err := service.appendOne(requestContext, runID, event.TypeToolRequested, map[string]any{
		"id": toolRequest.ID, "name": toolRequest.Name, "args": toolRequest.Args,
		"model": slotOf(phase), "phase": phase,
	})
	if err != nil {
		return nil, err
	}
	if toolRequest.Name == "fs.write" {
		path, _ := toolRequest.Args["path"].(string)
		if skill.IsRel(path) {
			if phase != "review" {
				return service.denyTool(requestContext, runID, seq, phase, toolRequest, "skill_write_requires_review")
			}
			revised, err := service.Events.HasSkillRevision(requestContext, runID, path)
			if err != nil {
				return nil, err
			}
			if revised {
				return service.denyTool(requestContext, runID, seq, phase, toolRequest, "skill_already_revised")
			}
		}
	}
	switch approve.Decide(toolRisk(service.Plugins, toolRequest.Name)) {
	case approve.Deny:
		return service.denyTool(requestContext, runID, seq, phase, toolRequest, "policy")
	case approve.Ask:
		allow, err := service.waitDecision(requestContext, runID, seq)
		if err != nil {
			return nil, err
		}
		if !allow {
			if _, err := service.appendOne(requestContext, runID, event.TypeToolDenied, map[string]any{
				"id": toolRequest.ID, "seq": seq, "name": toolRequest.Name,
				"model": slotOf(phase), "phase": phase, "reason": "user",
			}); err != nil {
				return nil, err
			}
			return nil, errDenied
		}
	}
	pluginName, operationName, err := splitTool(toolRequest.Name)
	if err != nil {
		return nil, err
	}
	if _, err := service.appendOne(requestContext, runID, event.TypeToolStarted, map[string]string{
		"id": toolRequest.ID, "name": toolRequest.Name, "model": slotOf(phase), "phase": phase,
	}); err != nil {
		return nil, err
	}
	pluginContext := plugin.WithPhase(plugin.WithRunID(requestContext, runID), phase)
	toolResult, err := service.Plugins.Exec(pluginContext, pluginName, operationName, toolRequest.Args)
	if err != nil {
		if _, appendErr := service.appendOne(requestContext, runID, event.TypeToolFailed, map[string]any{
			"id": toolRequest.ID, "name": toolRequest.Name, "error": err.Error(),
			"model": slotOf(phase), "phase": phase,
		}); appendErr != nil {
			return nil, appendErr
		}
		return nil, toolFailedError{msg: err.Error()}
	}
	payload := struct {
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Data  json.RawMessage `json:"data"`
		Model string          `json:"model"`
		Phase string          `json:"phase"`
	}{ID: toolRequest.ID, Name: toolRequest.Name, Data: toolResult, Model: slotOf(phase), Phase: phase}
	doneSeq, err := service.appendOne(requestContext, runID, event.TypeToolCompleted, payload)
	if err != nil {
		return nil, err
	}
	if toolRequest.Name == "fs.write" {
		path, _ := toolRequest.Args["path"].(string)
		content, _ := toolRequest.Args["content"].(string)
		if skillRevision, ok := skill.NewRevision(path, content, doneSeq); ok {
			if _, err := service.appendOne(requestContext, runID, event.TypeSkillRevised, skillRevision); err != nil {
				return nil, err
			}
		}
	}
	return toolResult, nil
}

func (service *Service) denyTool(
	requestContext context.Context,
	runID string,
	seq int,
	phase string,
	toolRequest *worker.Out,
	reason string,
) (json.RawMessage, error) {
	if _, err := service.appendOne(requestContext, runID, event.TypeToolDenied, map[string]any{
		"id": toolRequest.ID, "seq": seq, "name": toolRequest.Name, "reason": reason,
		"model": slotOf(phase), "phase": phase,
	}); err != nil {
		return nil, err
	}
	return nil, errDenied
}

func splitTool(name string) (string, string, error) {
	pluginName, operationName, ok := strings.Cut(name, ".")
	if !ok || pluginName == "" || operationName == "" {
		return "", "", fmt.Errorf("bad_tool: %s", name)
	}
	return pluginName, operationName, nil
}

func toolRisk(registry *plugin.Registry, name string) string {
	for _, tool := range registry.Tools() {
		if tool.Name == name {
			return tool.Risk
		}
	}
	return "write"
}
