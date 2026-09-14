package run

import "desk/internal/ctxmgr"

// pendingToolBoundary 把 Context Rebuild 拆成：
//   replaceMessages — durable 前缀，以 pending assistant(tool_calls) 结尾
//   deferredMessages — skill / retrieval 等动态 suffix，须在 tool.result 之后注入
//
// pending assistant(tool_calls) 是不可被 Assembly 插入打断的协议边界。
type pendingToolBoundary struct {
	Replace  []map[string]any
	Deferred []map[string]any
}

// splitPendingToolBoundary 按 Layers 切分，不改 Window/Buffer/skipSet。
// Window 在 PendingTool 时以 assistant(tool_calls) 收尾；Skill/Retrieval 属于动态 suffix。
func splitPendingToolBoundary(assembly ctxmgr.Assembly) pendingToolBoundary {
	layers := assembly.Layers
	var replace []map[string]any
	replace = append(replace, layers.Large...)
	replace = append(replace, layers.Smalls...)
	replace = append(replace, layers.Facts...)
	replace = append(replace, layers.Evicted...)
	replace = append(replace, layers.Window...)
	if replace == nil {
		replace = []map[string]any{}
	}
	var deferred []map[string]any
	deferred = append(deferred, layers.Skill...)
	deferred = append(deferred, layers.Retrieval...)
	if deferred == nil {
		deferred = []map[string]any{}
	}
	return pendingToolBoundary{Replace: replace, Deferred: deferred}
}

func hasToolCalls(msg map[string]any) bool {
	if msg == nil {
		return false
	}
	_, ok := msg["tool_calls"]
	return ok
}

func endsWithPendingToolCall(messages []map[string]any) bool {
	if len(messages) == 0 {
		return false
	}
	last := messages[len(messages)-1]
	if role, _ := last["role"].(string); role != "assistant" {
		return false
	}
	return hasToolCalls(last)
}
