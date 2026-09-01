package event

import (
	"strings"
	"testing"
)

func TestRedactCommonSecrets(t *testing.T) {
	input := `sk-live_123 API_KEY="abc123" token:xyz Authorization: Bearer ey.test/value`
	got := Redact(input)
	for _, secret := range []string{"live_123", "abc123", "xyz", "ey.test/value"} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret %q remains in %q", secret, got)
		}
	}
	if !strings.Contains(got, "API_KEY=***") || !strings.Contains(got, "Bearer ***") {
		t.Fatalf("markers missing: %q", got)
	}
}
