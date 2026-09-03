package ctxmgr

import (
	"testing"
	"unicode/utf8"
)

func TestEstimateTokensConservative(t *testing.T) {
	if EstimateTokens("") != 0 {
		t.Fatal("empty")
	}
	ascii := "hello world"
	if got := EstimateTokens(ascii); got < 5 {
		t.Fatalf("ascii=%d", got)
	}
	zh := "压缩上下文窗口"
	got := EstimateTokens(zh)
	if got < utf8.RuneCountInString(zh) {
		t.Fatalf("zh=%d", got)
	}
	if EstimateMessages([]map[string]any{{"role": "user", "content": "ab"}}) < 1 {
		t.Fatal("messages")
	}
}
