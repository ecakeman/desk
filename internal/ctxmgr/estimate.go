package ctxmgr

import "unicode/utf8"

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
