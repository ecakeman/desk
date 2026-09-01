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
func (s *Service) runTool(ctx context.Context, runID, phase string, req *worker.Out) (json.RawMessage, error) {
	seq, err := s.appendOne(ctx, runID, event.TypeToolRequested, map[string]any{
		"id": req.ID, "name": req.Name, "args": req.Args,
		"model": slotOf(phase), "phase": phase,
	})
	if err != nil {
		return nil, err
	}
	if req.Name == "fs.write" {
		path, _ := req.Args["path"].(string)
		if skill.IsRel(path) {
			if phase != "review" {
				return s.denyTool(ctx, runID, seq, phase, req, "skill_write_requires_review")
			}
			revised, err := s.Events.HasSkillRevision(ctx, runID, path)
			if err != nil {
				return nil, err
			}
			if revised {
				return s.denyTool(ctx, runID, seq, phase, req, "skill_already_revised")
			}
		}
	}
	switch approve.Decide(toolRisk(s.Plugins, req.Name)) {
	case approve.Deny:
		return s.denyTool(ctx, runID, seq, phase, req, "policy")
	case approve.Ask:
		allow, err := s.waitDecision(ctx, runID, seq)
		if err != nil {
			return nil, err
		}
		if !allow {
			if _, err := s.appendOne(ctx, runID, event.TypeToolDenied, map[string]any{
				"id": req.ID, "seq": seq, "name": req.Name,
				"model": slotOf(phase), "phase": phase, "reason": "user",
			}); err != nil {
				return nil, err
			}
			return nil, errDenied
		}
	}
	plug, op, err := splitTool(req.Name)
	if err != nil {
		return nil, err
	}
	if _, err := s.appendOne(ctx, runID, event.TypeToolStarted, map[string]string{
		"id": req.ID, "name": req.Name, "model": slotOf(phase), "phase": phase,
	}); err != nil {
		return nil, err
	}
	pluginCtx := plugin.WithPhase(plugin.WithRunID(ctx, runID), phase)
	data, err := s.Plugins.Exec(pluginCtx, plug, op, req.Args)
	if err != nil {
		if _, appendErr := s.appendOne(ctx, runID, event.TypeToolFailed, map[string]any{
			"id": req.ID, "name": req.Name, "error": err.Error(),
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
	}{ID: req.ID, Name: req.Name, Data: data, Model: slotOf(phase), Phase: phase}
	doneSeq, err := s.appendOne(ctx, runID, event.TypeToolCompleted, payload)
	if err != nil {
		return nil, err
	}
	if req.Name == "fs.write" {
		path, _ := req.Args["path"].(string)
		content, _ := req.Args["content"].(string)
		if revision, ok := skill.NewRevision(path, content, doneSeq); ok {
			if _, err := s.appendOne(ctx, runID, event.TypeSkillRevised, revision); err != nil {
				return nil, err
			}
		}
	}
	return data, nil
}

func (s *Service) denyTool(
	ctx context.Context,
	runID string,
	seq int,
	phase string,
	req *worker.Out,
	reason string,
) (json.RawMessage, error) {
	if _, err := s.appendOne(ctx, runID, event.TypeToolDenied, map[string]any{
		"id": req.ID, "seq": seq, "name": req.Name, "reason": reason,
		"model": slotOf(phase), "phase": phase,
	}); err != nil {
		return nil, err
	}
	return nil, errDenied
}

func splitTool(name string) (string, string, error) {
	plug, op, ok := strings.Cut(name, ".")
	if !ok || plug == "" || op == "" {
		return "", "", fmt.Errorf("bad_tool: %s", name)
	}
	return plug, op, nil
}

func toolRisk(registry *plugin.Registry, name string) string {
	for _, tool := range registry.Tools() {
		if tool.Name == name {
			return tool.Risk
		}
	}
	return "write"
}
