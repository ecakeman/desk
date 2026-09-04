package run

import (
	"context"

	"desk/internal/event"
)

// finish 在同一事务里写 message.completed（若有）、切 completed、写 run.completed。
func (service *Service) finish(requestContext context.Context, runID, text, model, phase, promptHash string) error {
	tx, err := service.DB.BeginTx(requestContext, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if text != "" {
		if _, err := service.Events.Append(requestContext, tx, runID, event.TypeMessageCompleted, map[string]string{
			"text":        text,
			"model":       model,
			"phase":       phase,
			"prompt_hash": promptHash,
		}); err != nil {
			return err
		}
	}
	if err := Transition(requestContext, tx, runID, StatusRunning, StatusCompleted); err != nil {
		return err
	}
	if _, err := service.Events.Append(requestContext, tx, runID, event.TypeRunCompleted, map[string]string{}); err != nil {
		return err
	}
	return tx.Commit()
}

// appendOne 单独开事务写一条事件。
func (service *Service) appendOne(requestContext context.Context, runID, typ string, payload any) (int, error) {
	tx, err := service.DB.BeginTx(requestContext, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	seq, err := service.Events.Append(requestContext, tx, runID, typ, payload)
	if err != nil {
		return 0, err
	}
	return seq, tx.Commit()
}
