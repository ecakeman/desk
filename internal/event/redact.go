package event

import "regexp"

var (
	reSK     = regexp.MustCompile(`sk-[A-Za-z0-9_-]+`)
	reSecret = regexp.MustCompile(`(?i)\b(api[_-]?key|key|token|secret)\s*[:=]\s*["']?[^\s"',}]+`)
	reBearer = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/-]+`)
)

// Redact 打码出站文本里的密钥形态；不改 events 表里的事实。
func Redact(s string) string {
	s = reSK.ReplaceAllString(s, "sk-***")
	s = reSecret.ReplaceAllString(s, "${1}=***")
	s = reBearer.ReplaceAllString(s, "Bearer ***")
	return s
}
