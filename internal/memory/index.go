// Package memory 把可检索事件投影到 memory_docs；可由 events 重建，不是第二事实源。
package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"desk/internal/event"
)

// Hit 是一条检索结果，指向 (run_id, seq)。
type Hit struct {
	Text  string  `json:"text"`
	Score float64 `json:"score"`
	RunID string  `json:"run_id"`
	Seq   int     `json:"seq"`
	Kind  string  `json:"kind"`
}

// SearchTrace 说明一次检索实际经过了哪些阶段。
type SearchTrace struct {
	LexicalHits    int  `json:"lexical_hits"`
	EmbeddingTried bool `json:"embedding_tried"`
	EmbeddingOK    bool `json:"embedding_ok"`
	VectorHits     int  `json:"vector_hits"`
	RerankTried    bool `json:"rerank_tried"`
	RerankOK       bool `json:"rerank_ok"`
	RerankFellBack bool `json:"rerank_fell_back"`
}

// Index 维护 memory_docs。Embedder / Reranker 为 nil 则跳过对应阶段。
type Index struct {
	DB            *sql.DB
	Embedder      Embedder
	Reranker      Reranker
	Dim           int
	OnError       func(error)
	RerankTimeout time.Duration
	RerankPool    int
}

// New 绑定 memory_docs；不自动挂 embed/rerank。
func New(db *sql.DB) *Index {
	return &Index{DB: db}
}

// Index 写入一条文档；有 Embedder 时异步补向量。
func (i *Index) Index(ctx context.Context, runID string, seq int, typ string, payload json.RawMessage) error {
	return i.index(ctx, i.DB, runID, seq, typ, payload, false)
}

// IndexTx 在事件事务里投影；失败由 EventStore savepoint 吞掉。
func (i *Index) IndexTx(ctx context.Context, tx *sql.Tx, runID string, seq int, typ string, payload json.RawMessage) error {
	return i.index(ctx, tx, runID, seq, typ, payload, false)
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (i *Index) index(ctx context.Context, exec execer, runID string, seq int, typ string, payload json.RawMessage, syncEmbed bool) error {
	text, ok := extract(typ, payload)
	if !ok {
		return nil
	}
	_, err := exec.ExecContext(ctx, `
		INSERT INTO memory_docs (run_id, seq, kind, text, tsv)
		VALUES ($1,$2,$3,$4, to_tsvector('simple', $4))
		ON CONFLICT (run_id, seq) DO UPDATE SET
			kind = EXCLUDED.kind,
			text = EXCLUDED.text,
			tsv = EXCLUDED.tsv`,
		runID, seq, typ, text,
	)
	if err != nil {
		return err
	}
	if i.Embedder == nil {
		return nil
	}
	if syncEmbed {
		return i.writeEmbedding(ctx, runID, seq, text)
	}
	go func() {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := i.writeEmbedding(c, runID, seq, text); err != nil {
			i.report(err)
		}
	}()
	return nil
}

func (i *Index) report(err error) {
	if err != nil && i.OnError != nil {
		i.OnError(err)
	}
}

func (i *Index) writeEmbedding(ctx context.Context, runID string, seq int, text string) error {
	vec, err := i.Embedder.Embed(ctx, text)
	if err != nil {
		return err
	}
	_, err = i.DB.ExecContext(ctx, `
		UPDATE memory_docs SET embedding = $3::vector
		WHERE run_id=$1 AND seq=$2`,
		runID, seq, formatVector(vec),
	)
	return err
}

// Search：BM25（候选可 ILIKE 并入）→ 可选向量+RRF → 可选 rerank；rerank 失败退回融合序。
func (i *Index) Search(ctx context.Context, q string, topK int) ([]Hit, error) {
	hits, _, err := i.SearchWithTrace(ctx, q, topK)
	return hits, err
}

// SearchWithTrace 返回命中及实际执行的检索阶段，供正式测试与回源审计。
func (i *Index) SearchWithTrace(ctx context.Context, q string, topK int) ([]Hit, SearchTrace, error) {
	var trace SearchTrace
	if topK <= 0 {
		topK = 8
	}
	pool := i.RerankPool
	if pool <= 0 {
		pool = 20
	}
	lex, err := i.searchBM25(ctx, q, pool)
	if err != nil {
		return nil, trace, err
	}
	trace.LexicalHits = len(lex)
	fused := lex
	if i.Embedder != nil {
		trace.EmbeddingTried = true
		if vecq, err := i.Embedder.Embed(ctx, q); err == nil {
			trace.EmbeddingOK = true
			vec, err := i.searchVec(ctx, vecq, pool)
			if err != nil {
				return nil, trace, err
			}
			trace.VectorHits = len(vec)
			fused = rrfMerge(lex, vec, pool)
		}
	}
	fused = clipHits(fused, pool)
	if i.Reranker == nil {
		return clipHits(fused, topK), trace, nil
	}
	trace.RerankTried = true
	timeout := i.RerankTimeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ranked, err := i.Reranker.Rerank(rctx, q, fused, topK)
	if err != nil || ranked == nil {
		// rerank 失败不得打掉检索；不伪造 rerank.success。
		trace.RerankFellBack = true
		return clipHits(fused, topK), trace, nil
	}
	trace.RerankOK = true
	return clipHits(ranked, topK), trace, nil
}

func (i *Index) searchBM25(ctx context.Context, q string, limit int) ([]Hit, error) {
	cands, err := i.loadCandidates(ctx, q)
	if err != nil {
		return nil, err
	}
	if len(tokenize(q)) == 0 {
		var contain []Hit
		for _, h := range cands {
			if containsFold(h.Text, q) {
				contain = append(contain, h)
			}
		}
		if contain == nil {
			contain = []Hit{}
		}
		return clipHits(contain, limit), nil
	}
	return clipHits(bm25Rank(cands, q), limit), nil
}

func (i *Index) loadCandidates(ctx context.Context, q string) ([]Hit, error) {
	rows, err := i.DB.QueryContext(ctx, `
		SELECT run_id, seq, kind, text, 0
		FROM memory_docs
		ORDER BY run_id, seq
		LIMIT 5000`)
	if err != nil {
		return nil, err
	}
	pool, err := scanHits(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]Hit, len(pool))
	order := make([]string, 0, len(pool))
	add := func(h Hit) {
		id := hitID(h)
		if _, ok := byID[id]; ok {
			return
		}
		byID[id] = h
		order = append(order, id)
	}
	for _, h := range pool {
		add(h)
	}
	q = strings.TrimSpace(q)
	if q != "" {
		rows, err = i.DB.QueryContext(ctx, `
			SELECT run_id, seq, kind, text, 0
			FROM memory_docs
			WHERE text ILIKE '%' || $1 || '%' ESCAPE '\'`, likeContainment(q))
		if err != nil {
			return nil, err
		}
		extra, err := scanHits(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, h := range extra {
			add(h)
		}
	}
	out := make([]Hit, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out, nil
}

// searchFTS 仅供对照 benchmark，Search 主路径不再用 ts_rank。
func (i *Index) searchFTS(ctx context.Context, q string, limit int) ([]Hit, error) {
	rows, err := i.DB.QueryContext(ctx, `
		SELECT run_id, seq, kind, text,
			COALESCE(ts_rank(tsv, websearch_to_tsquery('simple', $1)), 0)
		FROM memory_docs
		WHERE tsv @@ websearch_to_tsquery('simple', $1)
		   OR text ILIKE '%' || $1 || '%'
		ORDER BY 5 DESC, run_id, seq
		LIMIT $2`, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHits(rows)
}

func (i *Index) searchVec(ctx context.Context, vec []float32, limit int) ([]Hit, error) {
	rows, err := i.DB.QueryContext(ctx, `
		SELECT run_id, seq, kind, text,
			(1 - (embedding <=> $1::vector))
		FROM memory_docs
		WHERE embedding IS NOT NULL
		ORDER BY embedding <=> $1::vector
		LIMIT $2`, formatVector(vec), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHits(rows)
}

func scanHits(rows *sql.Rows) ([]Hit, error) {
	var out []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.RunID, &h.Seq, &h.Kind, &h.Text, &h.Score); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	if out == nil {
		out = []Hit{}
	}
	return out, rows.Err()
}

// Rebuild 清空后从全部 events 重建；启动路径用 Sync 做增量对账。
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
		if err := i.index(ctx, i.DB, runID, seq, typ, raw, true); err != nil {
			i.report(err)
		}
	}
	return rows.Err()
}

// Sync 按 events 对账 memory_docs；已有向量的行不重 embed。
func (i *Index) Sync(ctx context.Context) error {
	type key struct {
		runID string
		seq   int
	}
	type doc struct {
		kind         string
		text         string
		hasEmbedding bool
	}
	existing := make(map[key]doc)
	query := `SELECT run_id, seq, kind, text, false FROM memory_docs`
	if i.Embedder != nil {
		query = `SELECT run_id, seq, kind, text, embedding IS NOT NULL FROM memory_docs`
	}
	docRows, err := i.DB.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	for docRows.Next() {
		var runID, kind, text string
		var seq int
		var hasEmbedding bool
		if err := docRows.Scan(&runID, &seq, &kind, &text, &hasEmbedding); err != nil {
			docRows.Close()
			return err
		}
		existing[key{runID: runID, seq: seq}] = doc{
			kind: kind, text: text, hasEmbedding: hasEmbedding,
		}
	}
	if err := docRows.Err(); err != nil {
		docRows.Close()
		return err
	}
	if err := docRows.Close(); err != nil {
		return err
	}

	wanted := make(map[key]bool)
	eventRows, err := i.DB.QueryContext(ctx, `SELECT run_id, seq, type, payload FROM events`)
	if err != nil {
		return err
	}
	defer eventRows.Close()
	for eventRows.Next() {
		var runID, typ string
		var seq int
		var raw json.RawMessage
		if err := eventRows.Scan(&runID, &seq, &typ, &raw); err != nil {
			return err
		}
		text, ok := extract(typ, raw)
		if !ok {
			continue
		}
		itemKey := key{runID: runID, seq: seq}
		wanted[itemKey] = true
		current, found := existing[itemKey]
		if found && current.kind == typ && current.text == text {
			if i.Embedder != nil && !current.hasEmbedding {
				if err := i.writeEmbedding(ctx, runID, seq, text); err != nil {
					i.report(err)
				}
			}
			continue
		}
		if err := i.index(ctx, i.DB, runID, seq, typ, raw, true); err != nil {
			i.report(err)
		}
	}
	if err := eventRows.Err(); err != nil {
		return err
	}
	for itemKey := range existing {
		if wanted[itemKey] {
			continue
		}
		if _, err := i.DB.ExecContext(ctx,
			`DELETE FROM memory_docs WHERE run_id=$1 AND seq=$2`,
			itemKey.runID, itemKey.seq,
		); err != nil {
			return err
		}
	}
	return nil
}

func formatVector(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%g", x)
	}
	b.WriteByte(']')
	return b.String()
}

// extract 决定哪类事件进索引；未列出的类型不写 memory_docs。
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
	case event.TypeReviewCompleted:
		var p struct {
			Summary string `json:"summary"`
		}
		if json.Unmarshal(raw, &p) != nil || strings.TrimSpace(p.Summary) == "" {
			return "", false
		}
		return strings.TrimSpace(p.Summary), true
	case event.TypeTaskUpdated:
		var p struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Title  string `json:"title"`
		}
		if json.Unmarshal(raw, &p) != nil {
			return "", false
		}
		s := strings.TrimSpace(p.Title + " " + p.Status)
		return s, s != ""
	case event.TypeSkillRevised:
		var p struct {
			Path     string `json:"path"`
			DiffHead string `json:"diff_head"`
			Text     string `json:"text"`
		}
		if json.Unmarshal(raw, &p) != nil || p.Path == "" {
			return "", false
		}
		if t := strings.TrimSpace(p.Text); t != "" {
			return clipRunes(t, 2000), true
		}
		return strings.TrimSpace(p.Path + " " + p.DiffHead), p.Path != ""
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

func clipRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}
