package run

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"errors"

	"desk/internal/approve"
	"desk/internal/event"
	"desk/internal/plugin"
	"desk/internal/worker"
)

var errDenied = errors.New("tool_denied")

type toolFailedError struct{ msg string }

func (e toolFailedError) Error() string { return e.msg }

func (s *Service) Drive(ctx context.Context, runID string) error {
	var sessionID string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT session_id FROM runs WHERE id=$1`, runID,
	).Scan(&sessionID); err != nil {
		return err
	}
	defer s.Worker.Done(runID)
	if err := s.Events.EnsureCompact(ctx, sessionID, runID); err != nil {
		return err
	}
	msgs, err := s.Events.Messages(ctx, sessionID, runID)
	if err != nil {
		return err
	}
	var tools []any
	for _, t := range s.Plugins.Tools() {
		tools = append(tools, t)
	}
	nFlash := 0
	nFail := 0
	phase := "plan"
	slot := "pro"

	out, err := s.ask(ctx, runID, worker.In{
		T:        "turn.start",
		RunID:    runID,
		Messages: msgs,
		Tools:    tools,
		Phase:    phase,
	})
	if err != nil {
		return err
	}
	slot = slotOf(phase)

	for i := 0; i < 64; i++ {
		switch out.T {
		case "tool.request":
			data, err := s.runTool(ctx, runID, out)
			id := out.ID
			if errors.Is(err, errDenied) {
				out, err = s.ask(ctx, runID, worker.In{T: "tool.denied", ID: id, Phase: phase})
				if err != nil {
					return err
				}
				slot = slotOf(phase)
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
				out, err = s.ask(ctx, runID, worker.In{
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
				slot = slotOf(phase)
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
			out, err = s.ask(ctx, runID, worker.In{
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
			slot = slotOf(phase)
		case "turn.finish":
			return s.finish(ctx, runID, out.Text, slot)
		case "turn.fail":
			return fmt.Errorf("%s", out.Error)
		default:
			return fmt.Errorf("unknown worker t: %s", out.T)
		}
	}
	return fmt.Errorf("tool_limit")
}

func (s *Service) runTool(ctx context.Context, runID string, req *worker.Out) (json.RawMessage, error) {
	seq, err := s.appendOne(ctx, runID, event.TypeToolRequested, map[string]any{
		"id": req.ID, "name": req.Name, "args": req.Args,
	})
	if err != nil {
		return nil, err
	}
	switch approve.Decide(toolRisk(s.Plugins, req.Name)) {
	case approve.Deny:
		return nil, fmt.Errorf("denied")
	case approve.Ask:
		allow, err := s.waitDecision(ctx, runID)
		if err != nil {
			return nil, err
		}
		if !allow {
			if _, err := s.appendOne(ctx, runID, event.TypeToolDenied, map[string]any{
				"id": req.ID, "seq": seq, "name": req.Name,
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
		"id": req.ID, "name": req.Name,
	}); err != nil {
		return nil, err
	}
	data, err := s.Plugins.Exec(plugin.WithRunID(ctx, runID), plug, op, req.Args)
	if err != nil {
		if _, aerr := s.appendOne(ctx, runID, event.TypeToolFailed, map[string]any{
			"id": req.ID, "name": req.Name, "error": err.Error(),
		}); aerr != nil {
			return nil, aerr
		}
		return nil, toolFailedError{msg: err.Error()}
	}
	payload := struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Data json.RawMessage `json:"data"`
	}{ID: req.ID, Name: req.Name, Data: data}
	if _, err := s.appendOne(ctx, runID, event.TypeToolCompleted, payload); err != nil {
		return nil, err
	}
	return data, nil
}

func (s *Service) finish(ctx context.Context, runID, text, model string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if text != "" {
		if _, err := s.Events.Append(ctx, tx, runID, event.TypeMessageCompleted, map[string]string{
			"text":  text,
			"model": model,
		}); err != nil {
			return err
		}
	}
	if err := Transition(ctx, tx, runID, StatusRunning, StatusCompleted); err != nil {
		return err
	}
	if _,err := s.Events.Append(ctx, tx, runID, event.TypeRunCompleted, map[string]string{}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) appendOne(ctx context.Context, runID, typ string, payload any) (int,error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0,err
	}
	defer tx.Rollback()
	seq,err := s.Events.Append(ctx, tx, runID, typ, payload)
	if err != nil {
		return 0,err
	}
	return seq, tx.Commit()
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

func slotOf(phase string) string {
	if phase == "plan" || phase == "review" {
		return "pro"
	}
	return "flash"
}

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

func (s *Service) ask(ctx context.Context, runID string, in worker.In) (*worker.Out, error) {
	in.RunID = runID
	s.applySlot(&in)
	return s.Worker.Handle(in, func(o worker.Out) error {
		if o.T != "message.delta" || o.Text == "" {
			return nil
		}
		_, err := s.appendOne(ctx, runID, event.TypeMessageDelta, map[string]string{
			"text":  o.Text,
			"model": in.Model,
		})
		return err
	})
}