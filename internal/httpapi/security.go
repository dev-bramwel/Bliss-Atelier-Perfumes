package httpapi

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"strings"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
)

// TrustedProxyChecker evaluates whether an IP address belongs to a trusted reverse proxy hop.
type TrustedProxyChecker struct {
	cidrs []*net.IPNet
}

// NewTrustedProxyChecker builds a checker from comma-separated CIDR or IP strings.
// Always trusts standard loopback addresses (127.0.0.1/32 and ::1/128).
func NewTrustedProxyChecker(configuredProxies string) *TrustedProxyChecker {
	checker := &TrustedProxyChecker{}

	// Default loopback CIDRs
	_, loopback4, _ := net.ParseCIDR("127.0.0.1/32")
	_, loopback6, _ := net.ParseCIDR("::1/128")
	checker.cidrs = append(checker.cidrs, loopback4, loopback6)

	if configuredProxies != "" {
		for _, part := range strings.Split(configuredProxies, ",") {
			raw := strings.TrimSpace(part)
			if raw == "" {
				continue
			}
			if !strings.Contains(raw, "/") {
				if ip := net.ParseIP(raw); ip != nil {
					if ip.To4() != nil {
						raw += "/32"
					} else {
						raw += "/128"
					}
				}
			}
			if _, cidr, err := net.ParseCIDR(raw); err == nil && cidr != nil {
				checker.cidrs = append(checker.cidrs, cidr)
			}
		}
	}
	return checker
}

// IsTrusted reports whether ipStr is within a trusted proxy CIDR range.
func (c *TrustedProxyChecker) IsTrusted(ipStr string) bool {
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return false
	}
	for _, cidr := range c.cidrs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// SafeClientIP resolves the client's genuine IP address.
// If the direct RemoteAddr is untrusted, X-Forwarded-For is strictly ignored to prevent client spoofing.
// When RemoteAddr is trusted, X-Forwarded-For is inspected right-to-left for the first untrusted upstream IP.
func SafeClientIP(r *http.Request, checker *TrustedProxyChecker) string {
	remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || remoteHost == "" {
		remoteHost = r.RemoteAddr
	}

	if checker == nil || !checker.IsTrusted(remoteHost) {
		return remoteHost
	}

	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return remoteHost
	}

	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := strings.TrimSpace(parts[i])
		if ip == "" {
			continue
		}
		if !checker.IsTrusted(ip) {
			return ip
		}
	}

	return remoteHost
}

// ExtractGuestToken retrieves the high-entropy guest token from standard headers.
func ExtractGuestToken(r *http.Request) string {
	if tok := strings.TrimSpace(r.Header.Get("X-Guest-Token")); tok != "" {
		return tok
	}
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); auth != "" {
		if strings.HasPrefix(auth, "Bearer ") {
			candidate := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
			if strings.HasPrefix(candidate, "gt_") {
				return candidate
			}
		}
	}
	return ""
}

// ExtractManagerIdentity verifies whether the request carries valid manager credentials
// matching the configured managerSecret. If verified, it returns the manager's identity
// (from X-Manager-Id if provided, or defaulting to "manager").
// If the credential is missing, invalid, or managerSecret is empty, it returns "".
func ExtractManagerIdentity(r *http.Request, managerSecret string) string {
	if managerSecret == "" {
		return ""
	}

	var candidate string
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(auth, "Manager ") {
		candidate = strings.TrimSpace(strings.TrimPrefix(auth, "Manager "))
	} else if strings.HasPrefix(auth, "Bearer ") {
		token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		// Guest tokens start with "gt_" and must never be treated as manager credentials
		if !strings.HasPrefix(token, "gt_") {
			candidate = token
		}
	}
	if candidate == "" {
		candidate = strings.TrimSpace(r.Header.Get("X-Manager-Secret"))
	}
	if candidate == "" {
		candidate = strings.TrimSpace(r.Header.Get("X-Manager-Key"))
	}

	if candidate == "" {
		return ""
	}

	if subtle.ConstantTimeCompare([]byte(candidate), []byte(managerSecret)) != 1 {
		return ""
	}

	if id := strings.TrimSpace(r.Header.Get("X-Manager-Id")); id != "" {
		return id
	}
	return "manager"
}

// AuthorizeOrderAccess checks whether a request is authorized to access orderID,
// either via authenticated manager credentials or a valid scoped guest token.
func AuthorizeOrderAccess(ctx context.Context, r *http.Request, orderID string, orderStore OrderService, managerSecret ...string) (bool, error) {
	guestToken := ExtractGuestToken(r)
	if guestToken != "" {
		if !orders.ValidateTokenFormat(guestToken) || orderStore == nil {
			return false, nil
		}
		return orderStore.ValidateGuestAccess(ctx, orderID, guestToken)
	}
	sec := ""
	if len(managerSecret) > 0 {
		sec = managerSecret[0]
	}
	if ExtractManagerIdentity(r, sec) != "" {
		return true, nil
	}
	return false, nil
}

// SecurityConfig contains CORS, trusted proxy, and manager authentication settings.
type SecurityConfig struct {
	AllowedOrigins []string
	TrustedProxies string
	ManagerSecret  string
}

// CORSHandler applies standard security headers, CORS headers, and handles OPTIONS preflights.
func CORSHandler(allowedOrigins []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")

		origin := r.Header.Get("Origin")
		if origin != "" {
			allowed := false
			if len(allowedOrigins) == 0 {
				allowed = true
			} else {
				for _, o := range allowedOrigins {
					if o == "*" || strings.EqualFold(o, origin) {
						allowed = true
						break
					}
				}
			}

			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			}
		}

		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Guest-Token, X-Manager-Id, X-Manager-Secret, X-Manager-Key, Idempotency-Key")
			w.Header().Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
