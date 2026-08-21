package run

import (
	"context"
	"database/sql"
	"time"
)

type Run struct {
	ID           string    `json:"id"`
	SessionID    string    `json:"session_id"`
	Status       string    `json:"status"`
	WorkspaceDir string    `json:"workspace_dir"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Store struct {
	DB *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{DB: db}
}

func (s *Store) Get(ctx context.Context, id string) (*Run, error) {
	var out Run
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, session_id, status, workspace_dir, created_at, updated_at FROM runs WHERE id=$1`,
		id,
	).Scan(&out.ID, &out.SessionID, &out.Status, &out.WorkspaceDir, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &out, nil
}