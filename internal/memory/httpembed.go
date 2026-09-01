package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPEmbedder POST 配置的 BASE_URL 原样；body 是百炼原生 embeddings 格式。
type HTTPEmbedder struct {
	URL    string
	Key    string
	Model  string
	Dim    int
	Client *http.Client
}

// NewHTTPEmbedder 只去掉 URL 末尾斜杠，不拼接 path。
func NewHTTPEmbedder(baseURL, key, model string, dim int) *HTTPEmbedder {
	return &HTTPEmbedder{
		URL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		Key:    key,
		Model:  model,
		Dim:    dim,
		Client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Embed 请求一条文本的向量。
func (h *HTTPEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if h == nil || h.URL == "" {
		return nil, fmt.Errorf("embedder_unconfigured")
	}
	payload := map[string]any{
		"model": h.Model,
		"input": map[string]any{"texts": []string{text}},
	}
	if h.Dim > 0 {
		payload["parameters"] = map[string]any{"dimension": h.Dim}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if h.Key != "" {
		req.Header.Set("Authorization", "Bearer "+h.Key)
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("embed_http_%d: %s", resp.StatusCode, strings.TrimSpace(string(raw[:min(200, len(raw))])))
	}
	var out struct {
		Output struct {
			Embeddings []struct {
				Embedding []float32 `json:"embedding"`
			} `json:"embeddings"`
		} `json:"output"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if len(out.Output.Embeddings) == 0 || len(out.Output.Embeddings[0].Embedding) == 0 {
		return nil, fmt.Errorf("embed_empty")
	}
	vec := out.Output.Embeddings[0].Embedding
	if h.Dim > 0 && len(vec) != h.Dim {
		return nil, fmt.Errorf("embed_dim: got %d want %d", len(vec), h.Dim)
	}
	return vec, nil
}
