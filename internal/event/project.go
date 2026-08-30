package event

import (
	"context"
	"encoding/json"
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

func (s *Store) Messages(ctx context.Context, sessionID, currentRunID string) ([]map[string]any, error) {
	rows, err := s.sessionRows(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	skip := compactedSkip(rows)
	_ = currentRunID
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
			if skip[e.RunID][e.Seq] {
				continue
			}
			var p struct {
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(e.Raw, &p); err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"role": "tool", "content": Redact(string(p.Data))})
		case TypeEpisodeCompacted:
			var p compactPayload
			if err := json.Unmarshal(e.Raw, &p); err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"role": "tool", "content": Redact(p.Text)})
		case TypeMessageCompleted:
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(e.Raw, &p); err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"role": "assistant", "content": Redact(p.Text)})
		}
	}
	return out, nil
}