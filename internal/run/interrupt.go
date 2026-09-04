package run

import (
	"context"

	"desk/internal/event"
)

// Interrupt 把非终态 Run 标成 interrupted 并追加 run.interrupted。
func (service *Service) Interrupt(requestContext context.Context, runID, reason string) error {
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
	if err := Transition(requestContext, tx, runID, currentStatus, StatusInterrupted); err != nil {
		return err
	}
	if _, err := service.Events.Append(requestContext, tx, runID, event.TypeRunInterrupted, map[string]string{
		"reason": reason,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

// Cancel 取消本进程里这条 Drive 的 Context；没有活跃 Drive 则 ErrNotWaiting。
func (service *Service) Cancel(runID string) error {
	service.mu.Lock()
	cancel, ok := service.cancels[runID]
	service.mu.Unlock()
	if !ok {
		return ErrNotWaiting
	}
	cancel()
	return nil
}

// Recover 启动时把仍 running / waiting_approval 的 Run 标成 interrupted。
func (service *Service) Recover(requestContext context.Context) error {
	rows, err := service.DB.QueryContext(requestContext,
		`SELECT id, status FROM runs WHERE status IN ('running','waiting_approval')`,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct{ id, st string }
	var list []row
	for rows.Next() {
		var runRow row
		if err := rows.Scan(&runRow.id, &runRow.st); err != nil {
			return err
		}
		list = append(list, runRow)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, runRow := range list {
		if err := service.Interrupt(requestContext, runRow.id, "startup"); err != nil {
			return err
		}
	}
	return nil
}
