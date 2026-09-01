// Package session 拥有 sessions 表；列表 title 来自首条 message.user。
package session

import (
	"context"
	"database/sql"
	"time"

	"desk/internal/ids"
)

// Session 是 sessions 表的一行。
type Session struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Title     string    `json:"title,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Store 读写 sessions。
type Store struct {
	DB *sql.DB
}

// NewStore 绑定 sessions 表。
func NewStore(db *sql.DB) *Store {
	return &Store{DB: db}
}

// Create 插入 open 的 Session。
func (s *Store) Create(ctx context.Context) (*Session, error) {
	id := ids.New()
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO sessions (id, status) VALUES ($1, 'open')`, id)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Get 按 id 取 Session。
func (s *Store) Get(ctx context.Context, id string) (*Session, error) {
	return scanSession(s.DB.QueryRowContext(ctx,
		`SELECT id, status, created_at FROM sessions WHERE id=$1`, id,
	))
}

// GetTx 在调用方事务里取 Session。
func (s *Store) GetTx(ctx context.Context, tx *sql.Tx, id string) (*Session, error) {
	return scanSession(tx.QueryRowContext(ctx,
		`SELECT id, status, created_at FROM sessions WHERE id=$1`, id,
	))
}

type rowScanner interface {
	Scan(...any) error
}

func scanSession(row rowScanner) (*Session, error) {
	var out Session
	err := row.Scan(&out.ID, &out.Status, &out.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// List 新的在前；title 来自该 Session 第一条用户消息。
func (s *Store) List(ctx context.Context) ([]Session, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT s.id, s.status, s.created_at, COALESCE(t.title, '')
		FROM sessions s
		LEFT JOIN LATERAL (
			SELECT e.payload->>'text' AS title
			FROM runs r
			JOIN events e ON e.run_id = r.id AND e.type = 'message.user'
			WHERE r.session_id = s.id
			ORDER BY r.created_at ASC, e.seq ASC
			LIMIT 1
		) t ON true
		ORDER BY s.created_at DESC
		LIMIT 200`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Session, 0)
	for rows.Next() {
		var item Session
		if err := rows.Scan(&item.ID, &item.Status, &item.CreatedAt, &item.Title); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// Delete 级联删该 Session 的 memory_docs、events、runs。
func (s *Store) Delete(ctx context.Context, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := s.GetTx(ctx, tx, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM memory_docs
		WHERE run_id IN (SELECT id FROM runs WHERE session_id=$1)`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM events
		WHERE run_id IN (SELECT id FROM runs WHERE session_id=$1)`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM runs WHERE session_id=$1`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id=$1`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

// DeleteAll 清空 sessions / runs / events / memory_docs。
func (s *Store) DeleteAll(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM memory_docs
		WHERE run_id IN (SELECT id FROM runs)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM events
		WHERE run_id IN (SELECT id FROM runs)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM runs`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
		return err
	}
	return tx.Commit()
}
