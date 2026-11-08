package orders

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

const (
	guestTokenPrefix       = "gt_"
	guestTokenEntropyBytes = 32
	guestTokenHexLen       = guestTokenEntropyBytes * 2
	GuestTokenTotalLen     = len(guestTokenPrefix) + guestTokenHexLen
)

// GenerateGuestToken generates a high-entropy guest token and its SHA-256 hash.
// The raw token is returned once to the caller; only the hash is persisted.
func GenerateGuestToken() (rawToken string, tokenHash string, err error) {
	b := make([]byte, guestTokenEntropyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", errors.New("failed to generate secure random token")
	}
	rawToken = guestTokenPrefix + hex.EncodeToString(b)
	tokenHash = HashToken(rawToken)
	return rawToken, tokenHash, nil
}

// HashToken computes the SHA-256 hex digest of a raw token.
func HashToken(rawToken string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(rawToken)))
	return hex.EncodeToString(h[:])
}

// ValidateTokenFormat verifies that a token conforms to the expected high-entropy format.
func ValidateTokenFormat(token string) bool {
	token = strings.TrimSpace(token)
	if len(token) != GuestTokenTotalLen || !strings.HasPrefix(token, guestTokenPrefix) {
		return false
	}
	hexPart := token[len(guestTokenPrefix):]
	for i := 0; i < len(hexPart); i++ {
		c := hexPart[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
