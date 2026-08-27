package event

import (
	"context"
	"encoding/json"
)

func (s *Store) Messages(ctx context.Context, sessionID, currentRunID string) ([]map[string]any, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT e.run_id, e.type, e.payload
		FROM events e
		JOIN runs r ON r.id = e.run_id
		WHERE r.session_id = $1
		ORDER BY r.created_at, e.seq`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []map[string]any
	for rows.Next() {
		var runID, typ string
		var raw json.RawMessage
		if err := rows.Scan(&runID, &typ, &raw); err != nil {
			return nil, err
		}
		switch typ {
		case TypeMessageUser:
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"role": "user", "content": p.Text})
		case TypeToolCompleted:
			if runID != currentRunID {
				continue
			}
			var p struct {
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"role": "tool", "content": string(p.Data)})
		case TypeMessageCompleted:
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"role": "assistant", "content": p.Text})
		}
	}
	return out, rows.Err()
}