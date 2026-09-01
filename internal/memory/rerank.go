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

// Reranker 对候选精排；失败时 Search 退回融合序。
type Reranker interface {
	Rerank(ctx context.Context, query string, docs []Hit, topN int) ([]Hit, error)
}

// HTTPReranker POST 配置的 BASE_URL 原样；body 是百炼原生 rerank 格式。
type HTTPReranker struct {
	URL    string
	Key    string
	Model  string
	Client *http.Client
}

// NewHTTPReranker 只去掉 URL 末尾斜杠，不拼接 path。
func NewHTTPReranker(baseURL, key, model string, timeout time.Duration) *HTTPReranker {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &HTTPReranker{
		URL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		Key:    key,
		Model:  model,
		Client: &http.Client{Timeout: timeout},
	}
}

// Rerank 按模型返回的 index 重排；空结果视为失败。
func (h *HTTPReranker) Rerank(ctx context.Context, query string, docs []Hit, topN int) ([]Hit, error) {
	if h == nil || h.URL == "" {
		return nil, fmt.Errorf("reranker_unconfigured")
	}
	if len(docs) == 0 {
		return []Hit{}, nil
	}
	if topN <= 0 || topN > len(docs) {
		topN = len(docs)
	}
	texts := make([]string, len(docs))
	for i, d := range docs {
		texts[i] = d.Text
	}
	body, err := json.Marshal(map[string]any{
		"model": h.Model,
		"input": map[string]any{
			"query":     query,
			"documents": texts,
		},
		"parameters": map[string]any{"top_n": topN},
	})
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
		return nil, fmt.Errorf("rerank_http_%d: %s", resp.StatusCode, strings.TrimSpace(string(raw[:min(200, len(raw))])))
	}
	var out struct {
		Output struct {
			Results []struct {
				Index          int     `json:"index"`
				RelevanceScore float64 `json:"relevance_score"`
			} `json:"results"`
		} `json:"output"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if len(out.Output.Results) == 0 {
		return nil, fmt.Errorf("rerank_empty")
	}
	ranked := make([]Hit, 0, len(out.Output.Results))
	seen := map[int]bool{}
	for _, r := range out.Output.Results {
		if r.Index < 0 || r.Index >= len(docs) || seen[r.Index] {
			continue
		}
		seen[r.Index] = true
		hit := docs[r.Index]
		hit.Score = r.RelevanceScore
		ranked = append(ranked, hit)
		if len(ranked) >= topN {
			break
		}
	}
	if len(ranked) == 0 {
		return nil, fmt.Errorf("rerank_empty")
	}
	return ranked, nil
}
