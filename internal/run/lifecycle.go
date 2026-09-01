package run

import (
	"context"

	"desk/internal/event"
)

// finish 在同一事务里写 message.completed（若有）、切 completed、写 run.completed。
func (s *Service) finish(ctx context.Context, runID, text, model, phase, promptHash string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if text != "" {
		if _, err := s.Events.Append(ctx, tx, runID, event.TypeMessageCompleted, map[string]string{
			"text":        text,
			"model":       model,
			"phase":       phase,
			"prompt_hash": promptHash,
		}); err != nil {
			return err
		}
	}
	if err := Transition(ctx, tx, runID, StatusRunning, StatusCompleted); err != nil {
		return err
	}
	if _, err := s.Events.Append(ctx, tx, runID, event.TypeRunCompleted, map[string]string{}); err != nil {
		return err
	}
	return tx.Commit()
}

// appendOne 单独开事务写一条事件。
func (s *Service) appendOne(ctx context.Context, runID, typ string, payload any) (int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	seq, err := s.Events.Append(ctx, tx, runID, typ, payload)
	if err != nil {
		return 0, err
	}
	return seq, tx.Commit()
}
