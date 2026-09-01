package run

import (
	"context"

	"desk/internal/event"
)

// Interrupt 把非终态 Run 标成 interrupted 并追加 run.interrupted。
func (s *Service) Interrupt(ctx context.Context, runID, reason string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var cur string
	if err := tx.QueryRowContext(ctx,
		`SELECT status FROM runs WHERE id=$1`, runID,
	).Scan(&cur); err != nil {
		return err
	}
	if Terminal(cur) {
		return nil
	}
	if err := Transition(ctx, tx, runID, cur, StatusInterrupted); err != nil {
		return err
	}
	if _, err := s.Events.Append(ctx, tx, runID, event.TypeRunInterrupted, map[string]string{
		"reason": reason,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

// Cancel 取消本进程里这条 Drive 的 Context；没有活跃 Drive 则 ErrNotWaiting。
func (s *Service) Cancel(runID string) error {
	s.mu.Lock()
	cancel, ok := s.cancels[runID]
	s.mu.Unlock()
	if !ok {
		return ErrNotWaiting
	}
	cancel()
	return nil
}

// Recover 启动时把仍 running / waiting_approval 的 Run 标成 interrupted。
func (s *Service) Recover(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, status FROM runs WHERE status IN ('running','waiting_approval')`,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct{ id, st string }
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.st); err != nil {
			return err
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range list {
		if err := s.Interrupt(ctx, r.id, "startup"); err != nil {
			return err
		}
	}
	return nil
}
