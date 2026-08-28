package run

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"desk/internal/approve"
	"desk/internal/event"
	"desk/internal/plugin"
	"desk/internal/worker"
)

func (s *Service) Drive(ctx context.Context, runID string) error {
	var sessionID string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT session_id FROM runs WHERE id=$1`, runID,
	).Scan(&sessionID); err != nil {
		return err
	}
	defer s.Worker.Done(runID)

	msgs, err := s.Events.Messages(ctx, sessionID, runID)
	if err != nil {
		return err
	}
	var tools []any
	for _, t := range s.Plugins.Tools() {
		tools = append(tools, t)
	}

	out, err := s.ask(ctx, runID, worker.In{
		T:        "turn.start",
		RunID:    runID,
		Messages: msgs,
		Tools:    tools,
	})
	if err != nil {
		return err
	}

	for i := 0; i < 64; i++ {
		switch out.T {
		case "tool.request":
			data, err := s.runTool(ctx, runID, out)
			if err != nil {
				return err
			}
			id := out.ID
			out, err = s.ask(ctx, runID, worker.In{
				T:     "tool.result",
				RunID: runID,
				ID:    id,
				OK:    true,
				Data:  data,
			})
			if err != nil {
				return err
			}
		case "turn.finish":
			return s.finish(ctx,runID,out.Text)
		case "turn.fail":
			return fmt.Errorf("%s", out.Error)
		default:
			return fmt.Errorf("unknown worker t: %s", out.T)
		}
	}
	return fmt.Errorf("tool_limit")
}

func (s *Service) runTool(ctx context.Context, runID string, req *worker.Out) (json.RawMessage, error) {
	if err := s.appendOne(ctx, runID, event.TypeToolRequested, map[string]any{
		"id": req.ID, "name": req.Name, "args": req.Args,
	}); err != nil {
		return nil, err
	}
	if approve.Decide(toolRisk(s.Plugins, req.Name)) != approve.Allow {
		return nil, fmt.Errorf("need_approval")
	}
	plug, op, err := splitTool(req.Name)
	if err != nil {
		return nil, err
	}
	if err := s.appendOne(ctx, runID, event.TypeToolStarted, map[string]string{
		"id": req.ID, "name": req.Name,
	}); err != nil {
		return nil, err
	}
	data, err := s.Plugins.Exec(ctx, plug, op, req.Args)
	if err != nil {
		return nil, err
	}
	payload := struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Data json.RawMessage `json:"data"`
	}{ID: req.ID, Name: req.Name, Data: data}
	if err := s.appendOne(ctx, runID, event.TypeToolCompleted, payload); err != nil {
		return nil, err
	}
	return data, nil
}

func (s *Service) finish(ctx context.Context, runID, text string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if text != "" {
		if err := s.Events.Append(ctx, tx, runID, event.TypeMessageCompleted, map[string]string{
			"text": text,
		}); err != nil {
			return err
		}
	}
	if err := Transition(ctx, tx, runID, StatusRunning, StatusCompleted); err != nil {
		return err
	}
	if err := s.Events.Append(ctx, tx, runID, event.TypeRunCompleted, map[string]string{}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) appendOne(ctx context.Context, runID, typ string, payload any) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.Events.Append(ctx, tx, runID, typ, payload); err != nil {
		return err
	}
	return tx.Commit()
}

func splitTool(name string) (string, string, error) {
	plug, op, ok := strings.Cut(name, ".")
	if !ok || plug == "" || op == "" {
		return "", "", fmt.Errorf("bad_tool: %s", name)
	}
	return plug, op, nil
}

func toolRisk(r *plugin.Registry, name string) string {
	for _, t := range r.Tools() {
		if t.Name == name {
			return t.Risk
		}
	}
	return "write"
}

func (s *Service) ask(ctx context.Context, runID string, in worker.In) (*worker.Out, error) {
	in.RunID = runID
	return s.Worker.Handle(in, func(o worker.Out) error {
		if o.T != "message.delta" || o.Text == "" {
			return nil
		}
		return s.appendOne(ctx, runID, event.TypeMessageDelta, map[string]string{
			"text": o.Text,
		})
	})
}