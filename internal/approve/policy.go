// Package approve 按工具 risk 决定 allow / ask / deny；无持久状态。
package approve

const (
	// Allow 直接执行。
	Allow = "allow"
	// Deny 拒绝，不执行。
	Deny = "deny"
	// Ask 停在 waiting_approval。
	Ask = "ask"
)

// Decide：write → ask，其它 risk → allow。
func Decide(risk string) string {
	if risk == "write" {
		return Ask
	}
	return Allow
}
