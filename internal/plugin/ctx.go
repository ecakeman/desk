package plugin

import "context"

type runIDKey struct{}
type phaseKey struct{}

// WithRunID 把当前 Run 放进 context，给 task.update 这类内建插件用。
func WithRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runIDKey{}, runID)
}

// RunID 取出当前 Run；没有则空串。
func RunID(ctx context.Context) string {
	s, _ := ctx.Value(runIDKey{}).(string)
	return s
}

// WithPhase 把当前 Drive 阶段放进 context。
func WithPhase(ctx context.Context, phase string) context.Context {
	return context.WithValue(ctx, phaseKey{}, phase)
}

// Phase 取出当前阶段。
func Phase(ctx context.Context) string {
	value, _ := ctx.Value(phaseKey{}).(string)
	return value
}
