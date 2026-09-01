package memory

import "context"

// Embedder 把文本变成向量；Search 会对查询再调一次。
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}
