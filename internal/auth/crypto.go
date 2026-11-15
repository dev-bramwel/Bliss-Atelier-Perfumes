package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// RandomString returns a cryptographically secure URL-safe base64 string of requested raw byte entropy.
func RandomString(byteLen int) (string, error) {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto rand failed: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// GenerateSessionToken returns a high-entropy 64-character session token.
func GenerateSessionToken() (string, error) {
	// 48 bytes -> 64 chars in base64 URL-safe
	return RandomString(48)
}

// HashToken computes the SHA-256 hex digest of a token (e.g. session token or state).
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// GeneratePKCEVerifier generates a high-entropy code verifier string for RFC 7636 PKCE.
func GeneratePKCEVerifier() (string, error) {
	// 64 bytes -> 86 base64 characters, satisfying RFC 7636 (43..128 chars)
	return RandomString(64)
}

// ComputePKCEChallenge computes the code challenge using SHA-256 (S256 method) per RFC 7636.
func ComputePKCEChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// BrowserBindingHash computes a hash of client attributes to prevent transaction hijacking.
func BrowserBindingHash(ip string, userAgent string) string {
	h := sha256.Sum256([]byte(ip + "|" + userAgent))
	return hex.EncodeToString(h[:])
}

// GenerateCSRFToken generates an HMAC-signed CSRF token bound to the session's csrfSecret.
// Format: "<unix_timestamp>.<hmac_hex>"
func GenerateCSRFToken(csrfSecret string) string {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(csrfSecret))
	mac.Write([]byte(ts))
	sig := hex.EncodeToString(mac.Sum(nil))
	return ts + "." + sig
}

// ValidateCSRFToken verifies that a CSRF token has a valid HMAC signature and has not expired.
func ValidateCSRFToken(csrfSecret string, token string, maxAge time.Duration) bool {
	if csrfSecret == "" || token == "" {
		return false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return false
	}
	tsStr, sigHex := parts[0], parts[1]
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return false
	}

	tokenTime := time.Unix(ts, 0)
	if maxAge > 0 {
		now := time.Now()
		if now.Sub(tokenTime) > maxAge || tokenTime.After(now.Add(5*time.Minute)) {
			// Expired or from far future
			return false
		}
	}

	mac := hmac.New(sha256.New, []byte(csrfSecret))
	mac.Write([]byte(tsStr))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	return subtle.ConstantTimeCompare([]byte(sigHex), []byte(expectedSig)) == 1
}
