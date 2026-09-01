package run

import (
	"context"
	"database/sql"
	"time"
)

// Run 是 runs 表的一行：一句用户消息的执行状态。
type Run struct {
	ID           string    `json:"id"`
	SessionID    string    `json:"session_id"`
	Status       string    `json:"status"`
	WorkspaceDir string    `json:"workspace_dir"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Store 读写 runs 行；不编排 Drive。
type Store struct {
	DB *sql.DB
}

// NewStore 绑定 runs 表。
func NewStore(db *sql.DB) *Store {
	return &Store{DB: db}
}

// Get 按 id 取 Run。
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

// ListBySession 返回该 Session 最近的 Run，新的在前。
func (s *Store) ListBySession(ctx context.Context, sessionID string) ([]Run, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, session_id, status, workspace_dir, created_at, updated_at
		FROM runs
		WHERE session_id=$1
		ORDER BY created_at DESC
		LIMIT 200`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Run, 0)
	for rows.Next() {
		var item Run
		if err := rows.Scan(
			&item.ID, &item.SessionID, &item.Status, &item.WorkspaceDir,
			&item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// Delete 删该 Run 的 memory_docs、events 和 runs 行。
func (s *Store) Delete(ctx context.Context, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := s.Get(ctx, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM memory_docs WHERE run_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE run_id=$1`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM runs WHERE id=$1`, id)
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

// Transition 在事务里把 status 从 from 改为 to；非法转移返回 ErrConflict。
func Transition(ctx context.Context, tx *sql.Tx, id, from, to string) error {
	if !Can(from, to) {
		return ErrConflict
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE runs SET status=$1,updated_at=now() WHERE id=$2 AND status=$3`,
		to, id, from,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
