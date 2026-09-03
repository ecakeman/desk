package ctxmgr

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

// Compactor 用独立模型做 Small/Large 压缩；不经对话 Worker。
type Compactor interface {
	Compact(ctx context.Context, kind, system, user string) ([]byte, error)
}

// HTTPCompactor POST chat/completions，取 assistant content 当 JSON。
type HTTPCompactor struct {
	URL    string
	Key    string
	Model  string
	Client *http.Client
}

func NewHTTPCompactor(baseURL, key, model string) *HTTPCompactor {
	return &HTTPCompactor{
		URL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		Key:    key,
		Model:  model,
		Client: &http.Client{Timeout: 60 * time.Second},
	}
}

func (h *HTTPCompactor) Compact(ctx context.Context, kind, system, user string) ([]byte, error) {
	if h == nil || h.URL == "" || h.Model == "" {
		return nil, fmt.Errorf("compact_unconfigured")
	}
	_ = kind
	body, err := json.Marshal(map[string]any{
		"model": h.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": 0,
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
		return nil, fmt.Errorf("compact_http_%d", resp.StatusCode)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("compact_decode: %w", err)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("compact_empty_choice")
	}
	return []byte(stripFence(out.Choices[0].Message.Content)), nil
}

func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// StubCompactor 测试用；Err 非空则失败。
type StubCompactor struct {
	Raw []byte
	Err error
	N   int
}

func (s *StubCompactor) Compact(_ context.Context, _, _, user string) ([]byte, error) {
	s.N++
	if s.Err != nil {
		return nil, s.Err
	}
	raw := s.Raw
	var in struct {
		Allowed []int `json:"allowed_seqs"`
	}
	_ = json.Unmarshal([]byte(user), &in)
	if len(in.Allowed) > 0 && len(raw) > 0 {
		var obj map[string]any
		if json.Unmarshal(raw, &obj) == nil {
			if facts, ok := obj["facts"].([]any); ok && len(facts) > 0 {
				if f, ok := facts[0].(map[string]any); ok {
					f["source_event_seqs"] = []int{in.Allowed[0]}
				}
			}
			if b, err := json.Marshal(obj); err == nil {
				raw = b
			}
		}
	}
	return raw, nil
}
