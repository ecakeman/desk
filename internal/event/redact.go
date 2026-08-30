package event

import "regexp"

var (
	reSK  = regexp.MustCompile(`sk-[A-Za-z0-9]+`)
	reKEY = regexp.MustCompile(`KEY=[^\s]+`)
)

func Redact(s string) string {
	s = reSK.ReplaceAllString(s, "sk-***")
	s = reKEY.ReplaceAllString(s, "KEY=***")
	return s
}