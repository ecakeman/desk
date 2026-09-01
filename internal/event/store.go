// Package event 是历史事实源：只追加 events，投影 STM 时再算，不落第二张聊天表。
package event

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
)

// Store 追加并查询 events。OnInsert 在同一事务里做派生投影，失败不影响事实。
type Store struct {
	DB       *sql.DB
	OnInsert func(ctx context.Context, tx *sql.Tx, runID string, seq int, typ string, payload json.RawMessage) error
	OnError  func(error)
}

// NewStore 绑定 events 表。
func NewStore(db *sql.DB) *Store {
	return &Store{
		DB: db,
		OnError: func(err error) {
			log.Printf("event projection: %v", err)
		},
	}
}

// Event 是 (run_id, seq) 唯一的一条事实。
type Event struct {
	RunID   string          `json:"run_id,omitempty"`
	Seq     int             `json:"seq"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// Append 锁 runs 行后分配 seq+1；调用方必须已在事务里。
func (s *Store) Append(ctx context.Context, tx *sql.Tx, runID, typ string, payload any) (int, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	// 同一 Run 的所有 Append 先锁 runs 行，保证 MAX(seq)+1 串行。
	var locked string
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM runs WHERE id=$1 FOR UPDATE`,
		runID,
	).Scan(&locked); err != nil {
		return 0, err
	}
	var seq int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq),0)+1 FROM events WHERE run_id=$1`,
		runID,
	).Scan(&seq); err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO events (run_id,seq,type,payload) VALUES ($1,$2,$3,$4)`,
		runID, seq, typ, raw,
	)
	if err != nil {
		return seq, err
	}
	if s.OnInsert != nil {
		// 派生投影和事实事件处于同一事务。投影失败回滚到
		// savepoint，不影响 event；外层事务回滚时也不会留下孤儿。
		if _, err := tx.ExecContext(ctx, `SAVEPOINT event_projection`); err == nil {
			if err := s.OnInsert(ctx, tx, runID, seq, typ, raw); err != nil {
				_, _ = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT event_projection`)
				s.report(err)
			}
			_, _ = tx.ExecContext(ctx, `RELEASE SAVEPOINT event_projection`)
		}
	}
	return seq, nil
}

func (s *Store) report(err error) {
	if err == nil {
		return
	}
	if s.OnError != nil {
		s.OnError(err)
	}
}

// Get 取指定 seq。
func (s *Store) Get(ctx context.Context, runID string, seq int) (Event, error) {
	e := Event{RunID: runID}
	err := s.DB.QueryRowContext(ctx,
		`SELECT seq,type,payload FROM events WHERE run_id=$1 AND seq=$2`,
		runID, seq,
	).Scan(&e.Seq, &e.Type, &e.Payload)
	return e, err
}

// FirstUserText 取本 Run 第一条 message.user。
func (s *Store) FirstUserText(ctx context.Context, runID string) (string, error) {
	var raw json.RawMessage
	err := s.DB.QueryRowContext(ctx,
		`SELECT payload FROM events WHERE run_id=$1 AND type=$2 ORDER BY seq LIMIT 1`,
		runID, TypeMessageUser,
	).Scan(&raw)
	if err != nil {
		return "", err
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", err
	}
	return payload.Text, nil
}

// LastTaskTitle 取本 Run 最近一次 task.updated 的 title。
func (s *Store) LastTaskTitle(ctx context.Context, runID string) (string, error) {
	var raw json.RawMessage
	err := s.DB.QueryRowContext(ctx,
		`SELECT payload FROM events WHERE run_id=$1 AND type=$2 ORDER BY seq DESC LIMIT 1`,
		runID, TypeTaskUpdated,
	).Scan(&raw)
	if err != nil {
		return "", err
	}
	var payload struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", err
	}
	return payload.Title, nil
}

// HasSkillRevision 判断本 Run 是否已对 path 写过 skill.revised。
func (s *Store) HasSkillRevision(ctx context.Context, runID, path string) (bool, error) {
	var exists bool
	err := s.DB.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM events
			WHERE run_id=$1 AND type=$2 AND payload->>'path'=$3
		)`,
		runID, TypeSkillRevised, path,
	).Scan(&exists)
	return exists, err
}

// ListAfter 返回 seq > after 的事件，供 SSE 增量推送。
func (s *Store) ListAfter(ctx context.Context, runID string, after int) ([]Event, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT run_id,seq,type,payload FROM events WHERE run_id=$1 AND seq>$2 ORDER BY seq`,
		runID, after,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.RunID, &e.Seq, &e.Type, &e.Payload); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListBySession 按 Run 创建时间再按 seq 列出 Session 时间线。
func (s *Store) ListBySession(ctx context.Context, sessionID string) ([]Event, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT e.run_id, e.seq, e.type, e.payload
		FROM events e
		JOIN runs r ON r.id = e.run_id
		WHERE r.session_id = $1
		ORDER BY r.created_at, e.seq`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.RunID, &e.Seq, &e.Type, &e.Payload); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
