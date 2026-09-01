package event

import (
	"context"
	"encoding/json"
	"fmt"
)

type sessRow struct {
	RunID string
	Seq   int
	Type  string
	Raw   json.RawMessage
}

func (s *Store) sessionRows(ctx context.Context, sessionID string) ([]sessRow, error) {
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
	var out []sessRow
	for rows.Next() {
		var e sessRow
		if err := rows.Scan(&e.RunID, &e.Seq, &e.Type, &e.Raw); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Messages 从本 Session 事件投影 STM；当前 Run 的 tool 消息由 Python 进程自己持有。
func (s *Store) Messages(ctx context.Context, sessionID, currentRunID string) ([]map[string]any, error) {
	rows, err := s.sessionRows(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	skip := compactedSkip(rows)
	var out []map[string]any
	for _, e := range rows {
		switch e.Type {
		case TypeMessageUser:
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(e.Raw, &p); err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"role": "user", "content": Redact(p.Text)})
		case TypeToolCompleted:
			// 当前 Run 的完整 tool_call/tool 消息由 Python 进程维护；
			// 历史 Run 不能用缺少 tool_call_id 的 role=tool，改成带回源的上下文块。
			if e.RunID == currentRunID {
				continue
			}
			if skip[e.RunID][e.Seq] {
				continue
			}
			var p struct {
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(e.Raw, &p); err != nil {
				return nil, err
			}
			out = append(out, map[string]any{
				"role": "user",
				"content": Redact(
					fmt.Sprintf("[event tool.completed %s:%d]\n%s", e.RunID, e.Seq, string(p.Data)),
				),
			})
		case TypeEpisodeCompacted:
			var p compactPayload
			if err := json.Unmarshal(e.Raw, &p); err != nil {
				return nil, err
			}
			out = append(out, map[string]any{
				"role": "user",
				"content": Redact(
					fmt.Sprintf("[event episode.compacted %s:%d]\n%s", e.RunID, e.Seq, p.Text),
				),
			})
		case TypeMessageCompleted:
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(e.Raw, &p); err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"role": "assistant", "content": Redact(p.Text)})
		case TypeTaskUpdated:
			var p struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Title  string `json:"title"`
			}
			if err := json.Unmarshal(e.Raw, &p); err != nil {
				return nil, err
			}
			line := "task " + p.Status + " " + p.Title
			if p.ID != "" {
				line = "task " + p.ID + " " + p.Status + " " + p.Title
			}
			out = append(out, map[string]any{
				"role": "user",
				"content": Redact(
					"[CONTEXT: TASK]\n" + line + "\n[/CONTEXT]",
				),
			})
		}
	}
	return out, nil
}
