package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPReranker(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		input, _ := in["input"].(map[string]any)
		if input["query"] != "q" {
			t.Fatalf("query %v", input["query"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": map[string]any{
				"results": []map[string]any{
					{"index": 1, "relevance_score": 0.9},
					{"index": 0, "relevance_score": 0.1},
				},
			},
		})
	}))
	defer s.Close()
	out, err := NewHTTPReranker(s.URL, "k", "qwen3.7-text-rerank", time.Second).Rerank(
		context.Background(), "q",
		[]Hit{{RunID: "a", Text: "first"}, {RunID: "b", Text: "second"}},
		8,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].RunID != "b" || out[0].Score != 0.9 {
		t.Fatalf("%+v", out)
	}
}

func TestHTTPRerankerError(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("boom"))
	}))
	defer s.Close()
	_, err := NewHTTPReranker(s.URL, "", "m", time.Second).Rerank(context.Background(), "q", []Hit{{Text: "x"}}, 1)
	if err == nil {
		t.Fatal("want error")
	}
}

type errRerank struct{}

func (errRerank) Rerank(context.Context, string, []Hit, int) ([]Hit, error) {
	return nil, context.DeadlineExceeded
}

type reverseRerank struct{}

func (reverseRerank) Rerank(_ context.Context, _ string, docs []Hit, topN int) ([]Hit, error) {
	out := make([]Hit, len(docs))
	for i := range docs {
		h := docs[len(docs)-1-i]
		h.Score = float64(len(docs) - i)
		out[i] = h
	}
	return clipHits(out, topN), nil
}
