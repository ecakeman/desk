package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"unicode/utf8"

	"desk/internal/event"
)

type Hit struct {
	Text  string  `json:"text"`
	Score float64 `json:"score"`
	RunID string  `json:"run_id"`
	Seq   int     `json:"seq"`
	Kind  string  `json:"kind"`
}

type Index struct {
	DB *sql.DB
}

func New(db *sql.DB) *Index {
	return &Index{DB: db}
}

func (i *Index) Index(ctx context.Context, runID string, seq int, typ string, payload json.RawMessage) error {
	text, ok := extract(typ, payload)
	if !ok {
		return nil
	}
	_, err := i.DB.ExecContext(ctx, `
		INSERT INTO memory_docs (run_id, seq, kind, text, tsv)
		VALUES ($1,$2,$3,$4, to_tsvector('simple', $4))
		ON CONFLICT (run_id, seq) DO UPDATE SET
			kind = EXCLUDED.kind,
			text = EXCLUDED.text,
			tsv = EXCLUDED.tsv`,
		runID, seq, typ, text,
	)
	return err
}

func (i *Index) Search(ctx context.Context, q string, topK int) ([]Hit, error) {
	if topK <= 0 {
		topK = 8
	}
	rows, err := i.DB.QueryContext(ctx, `
		SELECT run_id, seq, kind, text,
			COALESCE(ts_rank(tsv, websearch_to_tsquery('simple', $1)), 0)
		FROM memory_docs
		WHERE tsv @@ websearch_to_tsquery('simple', $1)
		   OR text ILIKE '%' || $1 || '%'
		ORDER BY 5 DESC
		LIMIT $2`, q, topK)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.RunID, &h.Seq, &h.Kind, &h.Text, &h.Score); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (i *Index) Rebuild(ctx context.Context) error {
	if _, err := i.DB.ExecContext(ctx, `DELETE FROM memory_docs`); err != nil {
		return err
	}
	rows, err := i.DB.QueryContext(ctx, `SELECT run_id, seq, type, payload FROM events`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var runID, typ string
		var seq int
		var raw json.RawMessage
		if err := rows.Scan(&runID, &seq, &typ, &raw); err != nil {
			return err
		}
		_ = i.Index(ctx, runID, seq, typ, raw)
	}
	return rows.Err()
}

func extract(typ string, raw json.RawMessage) (string, bool) {
	switch typ {
	case event.TypeMessageUser, event.TypeMessageCompleted, event.TypeEpisodeCompacted:
		var p struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &p) != nil || p.Text == "" {
			return "", false
		}
		return p.Text, true
	case event.TypeToolCompleted:
		var p struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return "", false
		}
		s := string(p.Data)
		if n := utf8.RuneCountInString(s); n > 500 {
			s = string([]rune(s)[:500])
		}
		return s, s != ""
	default:
		return "", false
	}
}