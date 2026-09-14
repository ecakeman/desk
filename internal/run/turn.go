package run

import (
	"fmt"

	"desk/internal/worker"
)

// validateTurnFinish 是 turn.finish 提交到 finish() 之前的 Out 闸门。
// 只检查 Worker 协议形状：必须是完整 turn.finish，且不得夹带 tool.request 字段。
// 不解析 Text 为 JSON，不调用 ctxmgr.ValidateResult，不在 message.delta 路径执行。
func validateTurnFinish(out *worker.Out) error {
	if out == nil {
		return fmt.Errorf("turn_finish_missing")
	}
	if out.T != "turn.finish" {
		return fmt.Errorf("turn_finish_bad_t")
	}
	if out.Name != "" || out.ID != "" || len(out.Args) > 0 {
		return fmt.Errorf("turn_finish_mixed_tool")
	}
	return nil
}
