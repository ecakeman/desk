package run

import (
	"testing"

	"desk/internal/ctxmgr"
)

func TestSplitPendingToolBoundaryKeepsToolCallLastInReplace(t *testing.T) {
	assembly := ctxmgr.Assembly{
		Layers: ctxmgr.InspectLayers{
			Large: []map[string]any{{"role": "user", "content": "[CONTEXT: LARGE]"}},
			Smalls: []map[string]any{
				{"role": "user", "content": "[CONTEXT: SMALL]"},
			},
			Facts:   []map[string]any{{"role": "user", "content": "[CONTEXT: FACTS]"}},
			Evicted: []map[string]any{{"role": "user", "content": "evicted-old"}},
			Window: []map[string]any{
				{"role": "user", "content": "please continue"},
				{
					"role": "assistant",
					"tool_calls": []map[string]any{{
						"id":   "call-1",
						"type": "function",
						"function": map[string]any{
							"name":      "fs_read",
							"arguments": `{"path":"a.md"}`,
						},
					}},
				},
			},
			Skill:     []map[string]any{{"role": "user", "content": "[CONTEXT: SKILL]\ndemo"}},
			Retrieval: []map[string]any{{"role": "user", "content": "[CONTEXT: MEMORY]\nhit"}},
			Runtime:   []map[string]any{{"role": "user", "content": "[RUNTIME: PHASE]\nreview"}},
		},
	}
	// 旧错误路径：整份 Messages 把 skill/retrieval 插在 tool_calls 后。
	assembly.Messages = append(assembly.Messages, assembly.Layers.Large...)
	assembly.Messages = append(assembly.Messages, assembly.Layers.Smalls...)
	assembly.Messages = append(assembly.Messages, assembly.Layers.Facts...)
	assembly.Messages = append(assembly.Messages, assembly.Layers.Evicted...)
	assembly.Messages = append(assembly.Messages, assembly.Layers.Window...)
	assembly.Messages = append(assembly.Messages, assembly.Layers.Skill...)
	assembly.Messages = append(assembly.Messages, assembly.Layers.Retrieval...)
	if endsWithPendingToolCall(assembly.Messages) {
		t.Fatal("buggy full assembly must NOT end with pending tool_calls")
	}

	boundary := splitPendingToolBoundary(assembly)
	if !endsWithPendingToolCall(boundary.Replace) {
		t.Fatalf("replace must end with pending assistant(tool_calls): %#v", boundary.Replace)
	}
	for _, msg := range boundary.Replace {
		content, _ := msg["content"].(string)
		if content == "[CONTEXT: SKILL]\ndemo" || content == "[CONTEXT: MEMORY]\nhit" {
			t.Fatalf("dynamic suffix leaked into replace: %v", msg)
		}
		if content == "[RUNTIME: PHASE]\nreview" {
			t.Fatal("runtime must not be in replace; tool.result appends it after tool")
		}
	}
	if len(boundary.Deferred) != 2 {
		t.Fatalf("deferred=%d want skill+retrieval", len(boundary.Deferred))
	}
	// 模拟 Worker：replace → tool → deferred → runtime
	provider := append([]map[string]any{}, boundary.Replace...)
	provider = append(provider, map[string]any{
		"role":         "tool",
		"tool_call_id": "call-1",
		"content":      `{"ok":true}`,
	})
	provider = append(provider, boundary.Deferred...)
	provider = append(provider, map[string]any{"role": "user", "content": "[RUNTIME: PHASE]\nreview"})

	asstIdx, toolIdx := -1, -1
	for i, msg := range provider {
		role, _ := msg["role"].(string)
		if role == "assistant" && hasToolCalls(msg) {
			asstIdx = i
		}
		if role == "tool" {
			toolIdx = i
		}
	}
	if asstIdx < 0 || toolIdx != asstIdx+1 {
		t.Fatalf("tool must immediately follow tool_calls: asst=%d tool=%d", asstIdx, toolIdx)
	}
	for i := toolIdx + 1; i < len(provider); i++ {
		if hasToolCalls(provider[i]) {
			t.Fatal("no tool_calls after tool result")
		}
	}
}

func TestSplitPendingToolBoundaryEmptyLayers(t *testing.T) {
	boundary := splitPendingToolBoundary(ctxmgr.Assembly{})
	if boundary.Replace == nil || boundary.Deferred == nil {
		t.Fatal("nil slices")
	}
	if len(boundary.Replace) != 0 || len(boundary.Deferred) != 0 {
		t.Fatalf("want empty got replace=%d deferred=%d", len(boundary.Replace), len(boundary.Deferred))
	}
}
