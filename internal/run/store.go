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
func (runStore *Store) Get(requestContext context.Context, id string) (*Run, error) {
	var foundRun Run
	err := runStore.DB.QueryRowContext(requestContext,
		`SELECT id, session_id, status, workspace_dir, created_at, updated_at FROM runs WHERE id=$1`,
		id,
	).Scan(&foundRun.ID, &foundRun.SessionID, &foundRun.Status, &foundRun.WorkspaceDir, &foundRun.CreatedAt, &foundRun.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &foundRun, nil
}

// ListBySession 返回该 Session 最近的 Run，新的在前。
func (runStore *Store) ListBySession(requestContext context.Context, sessionID string) ([]Run, error) {
	rows, err := runStore.DB.QueryContext(requestContext, `
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
	sessionRuns := make([]Run, 0)
	for rows.Next() {
		var runRow Run
		if err := rows.Scan(
			&runRow.ID, &runRow.SessionID, &runRow.Status, &runRow.WorkspaceDir,
			&runRow.CreatedAt, &runRow.UpdatedAt,
		); err != nil {
			return nil, err
		}
		sessionRuns = append(sessionRuns, runRow)
	}
	return sessionRuns, rows.Err()
}

// Delete 删该 Run 的 memory_docs、events 和 runs 行。
func (runStore *Store) Delete(requestContext context.Context, id string) error {
	tx, err := runStore.DB.BeginTx(requestContext, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := runStore.Get(requestContext, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(requestContext, `DELETE FROM memory_docs WHERE run_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(requestContext, `DELETE FROM events WHERE run_id=$1`, id); err != nil {
		return err
	}
	sqlResult, err := tx.ExecContext(requestContext, `DELETE FROM runs WHERE id=$1`, id)
	if err != nil {
		return err
	}
	affectedRows, err := sqlResult.RowsAffected()
	if err != nil {
		return err
	}
	if affectedRows != 1 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

// Transition 在事务里把 status 从 from 改为 to；非法转移返回 ErrConflict。
func Transition(requestContext context.Context, tx *sql.Tx, id, from, to string) error {
	if !Can(from, to) {
		return ErrConflict
	}
	sqlResult, err := tx.ExecContext(requestContext,
		`UPDATE runs SET status=$1,updated_at=now() WHERE id=$2 AND status=$3`,
		to, id, from,
	)
	if err != nil {
		return err
	}
	affectedRows, err := sqlResult.RowsAffected()
	if err != nil {
		return err
	}
	if affectedRows != 1 {
		return ErrConflict
	}
	return nil
}
