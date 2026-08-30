package plugin

import "context"

type runIDKey struct{}

func WithRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runIDKey{}, runID)
}

func RunID(ctx context.Context) string {
	s, _ := ctx.Value(runIDKey{}).(string)
	return s
}
