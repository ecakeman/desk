package approve

const (
	Allow = "allow"
	Deny  = "deny"
	Ask   = "ask"
)

func Decide(risk string) string {
	if risk == "write" {
		return Ask
	}
	return Allow
}