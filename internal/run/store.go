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

func Transition(ctx context.Context, tx *sql.Tx, id, from, to string) error {
	if !Can(from, to) {
		return ErrConflict
	}
	res,err := tx.ExecContext(ctx,
		`UPDATE runs SET status=$1,updated_at=now() WHERE id=$2 AND status=$3`,
		to,id,from,
	)
	if err != nil {
		return err
	}
	n,err :=res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}