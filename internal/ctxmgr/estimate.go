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
func EstimateMessages(chatMessages []map[string]any) int {
	n := 0
	for _, chatMessage := range chatMessages {
		n += EstimateTokens(messageText(chatMessage))
	}
	return n
}

func messageText(chatMessage map[string]any) string {
	if chatMessage == nil {
		return ""
	}
	role, _ := chatMessage["role"].(string)
	content, _ := chatMessage["content"].(string)
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
	openaiTools := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) == "" {
			continue
		}
		parameters := any(map[string]any{"type": "object", "properties": map[string]any{}})
		if len(tool.Parameters) > 0 {
			var parsed any
			if json.Unmarshal(tool.Parameters, &parsed) == nil && parsed != nil {
				parameters = parsed
			}
		}
		openaiTools = append(openaiTools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        strings.ReplaceAll(tool.Name, ".", "_"),
				"description": tool.Description,
				"parameters":  parameters,
			},
		})
	}
	raw, err := json.Marshal(openaiTools)
	if err != nil {
		return 0
	}
	return EstimateTokens(string(raw))
}

// EstimateLLMInput 对应一次真实请求：system 消息 + tools JSON + assembly + runtime 预留。
func EstimateLLMInput(system string, tools []ToolSpec, messages []map[string]any, runtime string) int {
	return EstimateSystem(system) + EstimateTools(tools) + EstimateMessages(messages) + EstimateTokens(runtime)
}

func reservedTokens(prepareInput PrepareIn) int {
	return EstimateSystem(prepareInput.System) + EstimateTools(prepareInput.Tools)
}
