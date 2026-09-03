package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"desk/internal/db"
	"desk/internal/event"
	"desk/internal/ids"
	"desk/internal/testdb"
)

type stubEmbed struct{ dim int }

func (s stubEmbed) Embed(context.Context, string) ([]float32, error) {
	v := make([]float32, s.dim)
	if s.dim > 0 {
		v[0] = 1
	}
	return v, nil
}

func TestRuntimeContractMemoryFallback(t *testing.T) {
	ctx := context.Background()
	sqlDB := testdb.Open(t)
	const dim = 1024
	if err := db.EnsureEmbeddingColumn(ctx, sqlDB, dim); err != nil {
		t.Skip(err)
	}
	runID := ids.New()
	testdb.CleanupMemory(t, sqlDB, runID)
	idx := New(sqlDB)
	idx.Embedder = stubEmbed{dim: dim}
	idx.Dim = dim
	raw, _ := json.Marshal(map[string]string{"text": "bookmark-lab kebab-case decision D002"})
	if err := idx.index(ctx, sqlDB, runID, 1, event.TypeMessageUser, raw, true); err != nil {
		t.Fatal(err)
	}

	hits, lexTrace, err := idx.SearchWithTrace(ctx, "kebab-case", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].RunID != runID {
		t.Fatalf("vector/bm25 miss %+v", hits)
	}
	if lexTrace.LexicalHits == 0 {
		t.Fatalf("trace=%+v", lexTrace)
	}

	idx.Reranker = reverseRerank{}
	idx.RerankPool = 8
	_, okTrace, err := idx.SearchWithTrace(ctx, "kebab-case", 8)
	if err != nil {
		t.Fatal(err)
	}
	if !okTrace.RerankTried || !okTrace.RerankOK {
		t.Fatalf("rerank available failed trace=%+v", okTrace)
	}

	idx.Reranker = errRerank{}
	hits, failTrace, err := idx.SearchWithTrace(ctx, "kebab-case", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("fallback search empty")
	}
	if !failTrace.RerankTried || !failTrace.RerankFellBack || failTrace.RerankOK {
		t.Fatalf("want rerank fallback, got %+v", failTrace)
	}
	primary := "lexical"
	if lexTrace.EmbeddingOK {
		primary = "lexical+vector"
	}
	top := ""
	if len(hits) > 0 {
		top = hits[0].RunID
		if len(top) > 12 {
			top = top[:12]
		}
	}
	fmt.Printf("::evidence::[PASS] memory fallback\n")
	fmt.Printf("::evidence::       primary_search: %s\n", primary)
	fmt.Printf("::evidence::       rerank_available: %t\n", okTrace.RerankOK)
	fmt.Printf("::evidence::       fallback_used: %t\n", failTrace.RerankFellBack)
	fmt.Printf("::evidence::       result_count: %d\n", len(hits))
	fmt.Printf("::evidence::       top_result: %s\n", top)
}
