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
func (service *Service) waitDecision(requestContext context.Context, runID string, seq int) (bool, error) {
	decisionChannel := make(chan bool, 1)
	service.mu.Lock()
	service.pending[runID] = &pendingApproval{seq: seq, ch: decisionChannel}
	service.mu.Unlock()
	defer func() {
		service.mu.Lock()
		delete(service.pending, runID)
		service.mu.Unlock()
	}()

	tx, err := service.DB.BeginTx(requestContext, nil)
	if err != nil {
		return false, err
	}
	if err := Transition(requestContext, tx, runID, StatusRunning, StatusWaitingApproval); err != nil {
		_ = tx.Rollback()
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}

	select {
	case allow := <-decisionChannel:
		tx, err := service.DB.BeginTx(requestContext, nil)
		if err != nil {
			return false, err
		}
		if err := Transition(requestContext, tx, runID, StatusWaitingApproval, StatusRunning); err != nil {
			_ = tx.Rollback()
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return allow, nil
	case <-requestContext.Done():
		return false, requestContext.Err()
	}
}

// Decide 消费当前 pending 的那一次批准；seq 必须等于内存里的 pending。
func (service *Service) Decide(requestContext context.Context, runID string, seq int, allow bool) error {
	var status string
	err := service.DB.QueryRowContext(requestContext, `SELECT status FROM runs WHERE id=$1`, runID).Scan(&status)
	if err != nil {
		return err
	}
	if status != StatusWaitingApproval {
		return ErrNotWaiting
	}
	runEvent, err := service.Events.Get(requestContext, runID, seq)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrBadSeq
	}
	if err != nil {
		return err
	}
	if runEvent.Type != event.TypeToolRequested {
		return ErrBadSeq
	}
	service.mu.Lock()
	waitingApproval, ok := service.pending[runID]
	service.mu.Unlock()
	if !ok || waitingApproval.seq != seq {
		return ErrNotWaiting
	}
	if !waitingApproval.taken.CompareAndSwap(false, true) {
		return ErrConflict
	}
	waitingApproval.ch <- allow
	return nil
}
