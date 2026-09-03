package ctxmgr

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// EstimateTokens 是保守估算，不是供应商 tokenizer。
// 公式：max(rune 数, 字节数/2)，对短 ASCII 偏高、对中文接近字数。
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	runes := utf8.RuneCountInString(s)
	bytes := len(s)
	half := bytes / 2
	if runes > half {
		return runes
	}
	if half < 1 {
		return 1
	}
	return half
}

// EstimateMessages 把 role+content（及 tool 字段）拼成一段再估算。
func EstimateMessages(msgs []map[string]any) int {
	n := 0
	for _, m := range msgs {
		n += EstimateTokens(messageText(m))
	}
	return n
}

func messageText(m map[string]any) string {
	if m == nil {
		return ""
	}
	role, _ := m["role"].(string)
	content, _ := m["content"].(string)
	return role + "\n" + content
}

func systemMessage(system string) []map[string]any {
	if system == "" {
		return nil
	}
	return []map[string]any{{"role": "system", "content": system}}
}

// EstimateSystem 按 Worker 插入 messages[0] 的 system 估算。
func EstimateSystem(system string) int {
	return EstimateMessages(systemMessage(system))
}

// EstimateTools 按 Python openai_tools 最终 JSON 估算，含 name/description/parameters。
func EstimateTools(tools []ToolSpec) int {
	if len(tools) == 0 {
		return 0
	}
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		if strings.TrimSpace(t.Name) == "" {
			continue
		}
		params := any(map[string]any{"type": "object", "properties": map[string]any{}})
		if len(t.Parameters) > 0 {
			var parsed any
			if json.Unmarshal(t.Parameters, &parsed) == nil && parsed != nil {
				params = parsed
			}
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        strings.ReplaceAll(t.Name, ".", "_"),
				"description": t.Description,
				"parameters":  params,
			},
		})
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return 0
	}
	return EstimateTokens(string(raw))
}

// EstimateLLMInput 对应一次真实请求：system 消息 + tools JSON + assembly + runtime 预留。
func EstimateLLMInput(system string, tools []ToolSpec, messages []map[string]any, runtime string) int {
	return EstimateSystem(system) + EstimateTools(tools) + EstimateMessages(messages) + EstimateTokens(runtime)
}

func reservedTokens(in PrepareIn) int {
	return EstimateSystem(in.System) + EstimateTools(in.Tools)
}
