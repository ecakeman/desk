package run

import (
	"context"
	"database/sql"

	"desk/internal/event"
	"desk/internal/ids"
)

type Service struct {
	DB     *sql.DB
	Events *event.Store
}

func NewService(db *sql.DB, events *event.Store) *Service {
	return &Service{DB: db, Events: events}
}

func (s *Service) PostUserMessage(ctx context.Context, sessionID, text, workspace string) (string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var sess string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM sessions WHERE id=$1`, sessionID).Scan(&sess); err != nil {
		return "", err
	}

	runID := ids.New()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO runs (id, session_id, status, workspace_dir) VALUES ($1,$2,'running',$3)`,
		runID, sessionID, workspace,
	); err != nil {
		return "", err
	}
	if err := s.Events.Append(ctx, tx, runID, event.TypeRunCreated, map[string]string{
		"session_id": sessionID,
	}); err != nil {
		return "", err
	}
	if err := s.Events.Append(ctx, tx, runID, event.TypeMessageUser, map[string]string{
		"text": text,
	}); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return runID, nil
}