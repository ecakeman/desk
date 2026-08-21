package session

import (
	"context"
	"database/sql"
	"time"

	"desk/internal/ids"
)

type Session struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct {
	DB *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{DB: db}
}

func (s *Store) Create(ctx context.Context) (*Session, error) {
	id := ids.New()
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO sessions (id, status) VALUES ($1, 'open')`, id)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Store) Get(ctx context.Context, id string) (*Session, error) {
	var out Session
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, status, created_at FROM sessions WHERE id=$1`, id,
	).Scan(&out.ID, &out.Status, &out.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &out, nil
}