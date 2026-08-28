package run

import (
	"context"
	"database/sql"
	"errors"

	"desk/internal/event"
)

var (
	ErrNotWaiting = errors.New("not_waiting")
	ErrBadSeq     = errors.New("bad_seq")
)

func (s *Service) waitDecision(ctx context.Context, runID string) (bool, error) {
	ch := make(chan bool, 1)
	s.mu.Lock()
	s.pending[runID] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, runID)
		s.mu.Unlock()
	}()

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	if err := Transition(ctx, tx, runID, StatusRunning, StatusWaitingApproval); err != nil {
		_ = tx.Rollback()
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}

	select {
	case allow := <-ch:
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return false, err
		}
		if err := Transition(ctx, tx, runID, StatusWaitingApproval, StatusRunning); err != nil {
			_ = tx.Rollback()
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return allow, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (s *Service) Decide(ctx context.Context, runID string, seq int, allow bool) error {
	var status string
	err := s.DB.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=$1`, runID).Scan(&status)
	if err != nil {
		return err
	}
	if status != StatusWaitingApproval {
		return ErrNotWaiting
	}
	ev, err := s.Events.Get(ctx, runID, seq)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrBadSeq
	}
	if err != nil {
		return err
	}
	if ev.Type != event.TypeToolRequested {
		return ErrBadSeq
	}
	s.mu.Lock()
	ch, ok := s.pending[runID]
	s.mu.Unlock()
	if !ok {
		return ErrNotWaiting
	}
	select {
	case ch <- allow:
		return nil
	default:
		return ErrConflict
	}
}