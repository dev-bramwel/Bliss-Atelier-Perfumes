package orders

import (
	"strings"
	"testing"
)

func TestTokenGenerationAndValidation(t *testing.T) {
	raw, hash, err := GenerateGuestToken()
	if err != nil {
		t.Fatalf("GenerateGuestToken failed: %v", err)
	}
	if len(raw) != GuestTokenTotalLen {
		t.Fatalf("expected raw token length %d, got %d (%s)", GuestTokenTotalLen, len(raw), raw)
	}
	if !strings.HasPrefix(raw, "gt_") {
		t.Fatalf("expected raw token to start with gt_, got %s", raw)
	}
	if len(hash) != 64 {
		t.Fatalf("expected 64 hex char hash, got %d (%s)", len(hash), hash)
	}
	if !ValidateTokenFormat(raw) {
		t.Fatalf("expected raw token to pass ValidateTokenFormat")
	}

	// Format checks
	if ValidateTokenFormat("short") {
		t.Errorf("expected short token to fail format validation")
	}
	if ValidateTokenFormat("gt_" + strings.Repeat("z", 64)) {
		t.Errorf("expected non-hex characters to fail format validation")
	}
	if ValidateTokenFormat("xx_" + strings.Repeat("a", 64)) {
		t.Errorf("expected wrong prefix to fail format validation")
	}

	// Hash reproducibility
	recomputed := HashToken(raw)
	if recomputed != hash {
		t.Errorf("recomputed hash %s != original hash %s", recomputed, hash)
	}

	// Distinct tokens
	raw2, hash2, _ := GenerateGuestToken()
	if raw == raw2 || hash == hash2 {
		t.Errorf("successive token generations produced identical values")
	}
}
