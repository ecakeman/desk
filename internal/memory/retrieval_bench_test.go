package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"testing"

	"desk/internal/db"
	"desk/internal/event"
	"desk/internal/testdb"
)

type benchQuery struct {
	Name string
	Q    string
	Gold []string // hitID
}

func retrievalFixture() ([]Hit, []benchQuery) {
	docs := []Hit{
		{RunID: "d-jail", Seq: 1, Kind: event.TypeMessageUser, Text: "fs.read 越狱 path_escaped：拒绝 .. 与绝对路径"},
		{RunID: "d-write", Seq: 1, Kind: event.TypeToolCompleted, Text: "fs.write 写入 d12.txt 需要批准"},
		{RunID: "d-skill", Seq: 1, Kind: event.TypeSkillRevised, Text: "memory/skills/event-index.md 事件是事实 memory_docs 是派生索引"},
		{RunID: "d-status", Seq: 1, Kind: event.TypeRunFailed, Text: "illegal transition：completed 不能再进入 running"},
		{RunID: "d-tx", Seq: 1, Kind: event.TypeMessageCompleted, Text: "事务和事件必须同一提交，回滚不留孤儿"},
		{RunID: "d-stm", Seq: 1, Kind: event.TypeMessageCompleted, Text: "short-term memory is the current session projection, not a search index"},
		{RunID: "d-cancel", Seq: 1, Kind: event.TypeRunInterrupted, Text: "cancel 中断正在执行的 fs.sleep，Run 进入 interrupted"},
		{RunID: "d-rrf", Seq: 1, Kind: event.TypeMessageUser, Text: "RRF 融合 BM25 与向量排名，分数空间不可直接相加"},
	}
	qs := []benchQuery{
		{Name: "exact-term", Q: "path_escaped", Gold: []string{"d-jail:1"}},
		{Name: "tool-name", Q: "fs.write", Gold: []string{"d-write:1"}},
		{Name: "filename", Q: "event-index.md", Gold: []string{"d-skill:1"}},
		{Name: "error-string", Q: "illegal transition", Gold: []string{"d-status:1"}},
		{Name: "zh-nl", Q: "事务和事件必须同一提交", Gold: []string{"d-tx:1"}},
		{Name: "en-nl", Q: "short-term memory is the current session projection", Gold: []string{"d-stm:1"}},
		{Name: "paraphrase", Q: "how do we stop path traversal", Gold: []string{"d-jail:1"}},
		{Name: "zh-paraphrase", Q: "取消正在跑的工具", Gold: []string{"d-cancel:1"}},
	}
	return docs, qs
}

func TestRetrievalBenchmark(t *testing.T) {
	ctx := context.Background()
	sqlDB := testdb.Open(t)
	_ = db.EnsureEmbeddingColumn(ctx, sqlDB, 1024)
	docs, queries := retrievalFixture()
	prefix := "retr-bench-"
	runIDs := make([]string, 0, len(docs))
	for _, d := range docs {
		runIDs = append(runIDs, prefix+d.RunID)
	}
	testdb.CleanupMemory(t, sqlDB, runIDs...)
	_, _ = sqlDB.ExecContext(ctx, `DELETE FROM memory_docs WHERE run_id LIKE $1`, prefix+"%")
	idx := New(sqlDB)
	for _, d := range docs {
		raw, _ := json.Marshal(map[string]string{"text": d.Text})
		if err := idx.Index(ctx, prefix+d.RunID, d.Seq, event.TypeMessageUser, raw); err != nil {
			t.Fatal(err)
		}
	}

	eval := func(name string, search func(string) ([]Hit, error)) metrics {
		t.Helper()
		var all []float64
		var mrr, ndcg, recall float64
		n := 0.0
		for _, q := range queries {
			hits, err := search(q.Q)
			if err != nil {
				t.Fatalf("%s %s: %v", name, q.Name, err)
			}
			rel := map[string]bool{}
			for _, g := range q.Gold {
				rel[prefix+g] = true
			}
			rank := -1
			for i, h := range hits {
				if rel[hitID(h)] {
					rank = i + 1
					break
				}
			}
			n++
			if rank > 0 && rank <= 8 {
				recall++
				mrr += 1 / float64(rank)
			}
			ndcg += ndcgAt(hits, rel, 8)
			all = append(all, float64(rank))
			t.Logf("%s %s rank=%d", name, q.Name, rank)
		}
		return metrics{Name: name, Recall: recall / n, MRR: mrr / n, NDCG: ndcg / n, Ranks: all}
	}

	fts := eval("fts", func(q string) ([]Hit, error) { return idx.searchFTS(ctx, q, 8) })
	bm25 := eval("bm25", func(q string) ([]Hit, error) { return idx.searchBM25(ctx, q, 8) })
	results := []metrics{fts, bm25}

	var hasEmb bool
	_ = sqlDB.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_attribute a
			JOIN pg_class c ON c.oid = a.attrelid
			WHERE c.relname='memory_docs' AND a.attname='embedding' AND a.attnum>0 AND NOT a.attisdropped
		)`).Scan(&hasEmb)
	if hasEmb {
		idx.Embedder = bagEmbed{dim: 1024}
		for _, d := range docs {
			if err := idx.writeEmbedding(ctx, prefix+d.RunID, d.Seq, d.Text); err != nil {
				t.Logf("skip vector: %v", err)
				hasEmb = false
				idx.Embedder = nil
				break
			}
		}
	}
	if hasEmb {
		idx.Embedder = bagEmbed{dim: 1024}
		for _, d := range docs {
			if err := idx.writeEmbedding(ctx, prefix+d.RunID, d.Seq, d.Text); err != nil {
				t.Fatal(err)
			}
		}
		vecOnly := eval("vector", func(q string) ([]Hit, error) {
			v, err := idx.Embedder.Embed(ctx, q)
			if err != nil {
				return nil, err
			}
			return idx.searchVec(ctx, v, 8)
		})
		rrf := eval("bm25+vec+rrf", func(q string) ([]Hit, error) {
			idx.Reranker = nil
			return idx.Search(ctx, q, 8)
		})
		idx.Reranker = overlapRerank{}
		reranked := eval("rrf+overlap-rerank", func(q string) ([]Hit, error) {
			return idx.Search(ctx, q, 8)
		})
		idx.Reranker = nil
		results = append(results, vecOnly, rrf, reranked)
	}

	for _, m := range results {
		t.Logf("metric %s recall@8=%.3f mrr=%.3f ndcg@8=%.3f", m.Name, m.Recall, m.MRR, m.NDCG)
	}
	if bm25.MRR+1e-9 < fts.MRR && bm25.Recall+1e-9 < fts.Recall {
		t.Fatalf("bm25 should not lose both MRR and Recall to fts: bm25=%+v fts=%+v", bm25, fts)
	}
}

type metrics struct {
	Name   string
	Recall float64
	MRR    float64
	NDCG   float64
	Ranks  []float64
}

func ndcgAt(hits []Hit, rel map[string]bool, k int) float64 {
	var dcg, idcg float64
	for i, h := range hits {
		if i >= k {
			break
		}
		if rel[hitID(h)] {
			dcg += 1 / math.Log2(float64(i+2))
		}
	}
	nRel := len(rel)
	for i := 0; i < nRel && i < k; i++ {
		idcg += 1 / math.Log2(float64(i+2))
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

type bagEmbed struct{ dim int }

func (b bagEmbed) Embed(_ context.Context, text string) ([]float32, error) {
	vec := make([]float32, b.dim)
	if b.dim <= 0 {
		return nil, fmt.Errorf("bad dim")
	}
	for _, tok := range tokenize(text) {
		h := uint32(2166136261)
		for _, c := range tok {
			h ^= uint32(c)
			h *= 16777619
		}
		vec[int(h)%b.dim] += 1
	}
	var n float32
	for _, x := range vec {
		n += x * x
	}
	if n > 0 {
		s := float32(1 / math.Sqrt(float64(n)))
		for i := range vec {
			vec[i] *= s
		}
	}
	return vec, nil
}

type overlapRerank struct{}

func (overlapRerank) Rerank(_ context.Context, query string, docs []Hit, topN int) ([]Hit, error) {
	q := uniqueTokens(tokenize(query))
	out := append([]Hit(nil), docs...)
	score := func(h Hit) float64 {
		dt := uniqueTokens(tokenize(h.Text))
		seen := map[string]bool{}
		for _, tok := range dt {
			seen[tok] = true
		}
		n := 0.0
		for _, tok := range q {
			if seen[tok] {
				n++
			}
		}
		if len(q) == 0 {
			return 0
		}
		return n / float64(len(q))
	}
	for i := range out {
		out[i].Score = score(out[i])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return clipHits(out, topN), nil
}

func TestILIKEExpandsCandidateNotScore(t *testing.T) {
	docs := []Hit{
		{RunID: "rare", Seq: 1, Text: "event-index.md only here"},
		{RunID: "common", Seq: 2, Text: "event event event event event"},
	}
	ranked := bm25Rank(docs, "event-index.md")
	if len(ranked) == 0 || ranked[0].RunID != "rare" {
		t.Fatalf("bm25 should rank containment term, got %+v", ranked)
	}
	if !containsFold(docs[0].Text, "event-index.md") {
		t.Fatal("containment")
	}
}

func TestLikeContainmentEscapesWildcards(t *testing.T) {
	if likeContainment(`a%b_c\d`) != `a\%b\_c\\d` {
		t.Fatalf("%q", likeContainment(`a%b_c\d`))
	}
}
