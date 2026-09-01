package run

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"

	"desk/internal/event"
)

var (
	// ErrNotWaiting 表示 Run 不在 waiting_approval，或 seq 不是当前 pending。
	ErrNotWaiting = errors.New("not_waiting")
	// ErrBadSeq 表示 seq 对不上 tool.requested。
	ErrBadSeq = errors.New("bad_seq")
)

type pendingApproval struct {
	seq   int
	ch    chan bool
	taken atomic.Bool
}

// waitDecision 把 Run 切到 waiting_approval，只接受这一次 seq 的 Decide。
func (s *Service) waitDecision(ctx context.Context, runID string, seq int) (bool, error) {
	ch := make(chan bool, 1)
	s.mu.Lock()
	s.pending[runID] = &pendingApproval{seq: seq, ch: ch}
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

// Decide 消费当前 pending 的那一次批准；seq 必须等于内存里的 pending。
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
	pending, ok := s.pending[runID]
	s.mu.Unlock()
	if !ok || pending.seq != seq {
		return ErrNotWaiting
	}
	if !pending.taken.CompareAndSwap(false, true) {
		return ErrConflict
	}
	pending.ch <- allow
	return nil
}
