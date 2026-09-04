package run

import (
	"context"

	"desk/internal/event"
)

// Fail 把非终态 Run 标成 failed 并追加 run.failed。
func (service *Service) Fail(requestContext context.Context, runID, reason string) error {
	tx, err := service.DB.BeginTx(requestContext, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var currentStatus string
	if err := tx.QueryRowContext(requestContext,
		`SELECT status FROM runs WHERE id=$1`, runID,
	).Scan(&currentStatus); err != nil {
		return err
	}
	if Terminal(currentStatus) {
		return nil
	}
	if err := Transition(requestContext, tx, runID, currentStatus, StatusFailed); err != nil {
		return err
	}
	if _, err := service.Events.Append(requestContext, tx, runID, event.TypeRunFailed, map[string]string{
		"reason": reason,
	}); err != nil {
		return err
	}
	return tx.Commit()
}
