package httpapi

import (
	"strings"
	"testing"
)

func TestCheckoutRecoveryScope(t *testing.T) {
	token := "ct_" + strings.Repeat("ab", 32)
	first := checkoutScope(token, "192.0.2.1")
	if first != checkoutScope(token, "198.51.100.1") {
		t.Fatal("guest recovery changes with network address")
	}
	if strings.Contains(first, token) {
		t.Fatal("raw checkout secret stored in scope")
	}
	if first == checkoutScope("ct_"+strings.Repeat("cd", 32), "192.0.2.1") {
		t.Fatal("distinct browsers share a scope")
	}
	for _, bad := range []string{"", "ct_short", "ct_" + strings.Repeat("z", 64), "gt_" + strings.Repeat("ab", 32)} {
		if checkoutScope(bad, "192.0.2.1") != "guest:192.0.2.1" {
			t.Fatal("invalid recovery token accepted")
		}
	}
}
