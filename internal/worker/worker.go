// Package worker 每 Run 一个 Python 子进程，JSON line 协议；不碰 DB 和 Workspace。
package worker

// Worker 是 Drive 对模型会话的接口。
type Worker interface {
	Handle(in In, emit func(Out) error) (*Out, error)
	Done(runID string)
}
