package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPEmbedder(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		input, _ := in["input"].(map[string]any)
		texts, _ := input["texts"].([]any)
		if len(texts) != 1 || texts[0] != "hello" {
			t.Fatalf("texts %v", texts)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": map[string]any{
				"embeddings": []map[string]any{{"embedding": []float32{0.1, 0.2, 0.3, 0.4}}},
			},
		})
	}))
	defer s.Close()
	h := NewHTTPEmbedder(s.URL, "k", "qwen3.7-text-embedding", 4)
	vec, err := h.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 4 {
		t.Fatalf("len %d", len(vec))
	}
}

func TestHTTPEmbedderError(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("boom"))
	}))
	defer s.Close()
	_, err := NewHTTPEmbedder(s.URL, "", "m", 2).Embed(context.Background(), "x")
	if err == nil {
		t.Fatal("want error")
	}
}
