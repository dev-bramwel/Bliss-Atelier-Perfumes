package httpapi

import (
	"encoding/hex"
	"strings"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
)

// checkoutScope binds guest creation retries to a browser secret, not its IP.
// This token is not accepted by order authorization and is stored only as a hash.
func checkoutScope(token, ip string) string {
	if strings.HasPrefix(token, "ct_") && len(token) == 67 {
		if decoded, err := hex.DecodeString(token[3:]); err == nil && len(decoded) == 32 {
			return "checkout:" + orders.CanonicalRequestHash([]byte(token))
		}
	}
	return "guest:" + ip // Backward compatibility for existing API clients.
}
