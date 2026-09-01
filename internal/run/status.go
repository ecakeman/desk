package run

import "errors"

const (
	// StatusRunning 表示 Drive 正在推进。
	StatusRunning = "running"
	// StatusWaitingApproval 表示写工具停在 Ask，等 Decide。
	StatusWaitingApproval = "waiting_approval"
	// StatusCompleted 是正常结束。
	StatusCompleted = "completed"
	// StatusFailed 是 Drive 出错结束。
	StatusFailed = "failed"
	// StatusInterrupted 是取消或启动恢复结束。
	StatusInterrupted = "interrupted"
)

// ErrConflict 表示状态机不允许这次转移，或 Decide 已被消费。
var ErrConflict = errors.New("illegal transition")

var allowed = map[string]map[string]bool{
	StatusRunning: {
		StatusWaitingApproval: true,
		StatusCompleted:       true,
		StatusFailed:          true,
		StatusInterrupted:     true,
	},
	StatusWaitingApproval: {
		StatusRunning:     true,
		StatusFailed:      true,
		StatusInterrupted: true,
	},
}

// Can 判断 from → to 是否合法。
func Can(from, to string) bool {
	return allowed[from][to]
}

// Terminal 表示 Run 不再被 Drive 推进，也不能 Decide。
func Terminal(status string) bool {
	return status == StatusCompleted || status == StatusFailed || status == StatusInterrupted
}
