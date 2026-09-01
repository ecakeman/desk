package run

import (
	"context"

	"desk/internal/event"
)

// Fail 把非终态 Run 标成 failed 并追加 run.failed。
func (s *Service) Fail(ctx context.Context, runID, reason string) error {
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
	if err := Transition(ctx, tx, runID, cur, StatusFailed); err != nil {
		return err
	}
	if _, err := s.Events.Append(ctx, tx, runID, event.TypeRunFailed, map[string]string{
		"reason": reason,
	}); err != nil {
		return err
	}
	return tx.Commit()
}
