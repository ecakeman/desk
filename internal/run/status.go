package run

import "errors"

const(
	StatusRunning = "running"
	StatusWaitingApproval = "waiting_approval"
	StatusCompleted = "completed"
	StatusFailed = "failed"
	StatusInterrupted = "interrupted"
)

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

func Can(from,to string) bool{
	return allowed[from][to]
}