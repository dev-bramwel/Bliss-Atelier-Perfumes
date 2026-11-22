package telemetry

import (
	"strings"
)

// NormalizeRoute maps any arbitrary URL path into a bounded route template to prevent
// metric label cardinality explosions. Dynamic identifiers (e.g. ord_..., user_...) are
// collapsed into standard template tokens like {id}.
func NormalizeRoute(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "/" {
		return "/"
	}

	// Normalize multiple slashes and trailing slashes
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 {
		return "/"
	}

	// Exact top-level routes
	if len(parts) == 1 {
		switch parts[0] {
		case "metrics":
			return "/metrics"
		case "catalog":
			return "/catalog"
		case "orders":
			return "/orders"
		case "cart":
			return "/cart"
		case "checkout":
			return "/checkout"
		case "dashboard":
			return "/dashboard"
		default:
			return "/unmapped"
		}
	}

	// Two-level routes
	if len(parts) == 2 {
		if parts[0] == "health" {
			switch parts[1] {
			case "live":
				return "/health/live"
			case "ready":
				return "/health/ready"
			}
		}
		if parts[0] == "static" {
			return "/static/{file}"
		}
		if parts[0] == "img" {
			return "/img/{file}"
		}
		if parts[0] == "orders" {
			return "/orders/{id}"
		}
		if parts[0] == "api" {
			switch parts[1] {
			case "catalog":
				return "/api/catalog"
			case "orders":
				return "/api/orders"
			}
		}
	}

	// API prefixes (/api/...)
	if parts[0] == "api" {
		switch parts[1] {
		case "health":
			if len(parts) == 3 {
				switch parts[2] {
				case "live":
					return "/api/health/live"
				case "ready":
					return "/api/health/ready"
				}
			}
		case "catalog":
			if len(parts) == 3 && parts[2] == "invalidate" {
				return "/api/catalog/invalidate"
			}
		case "auth":
			if len(parts) == 3 {
				switch parts[2] {
				case "google":
					return "/api/auth/google"
				case "me":
					return "/api/auth/me"
				case "logout":
					return "/api/auth/logout"
				}
			}
			if len(parts) == 4 && parts[2] == "google" && parts[3] == "callback" {
				return "/api/auth/google/callback"
			}
		case "orders":
			if len(parts) == 3 {
				return "/api/orders/{id}"
			}
			if len(parts) == 4 {
				switch parts[3] {
				case "payment-status":
					return "/api/orders/{id}/payment-status"
				case "retry-payment":
					return "/api/orders/{id}/retry-payment"
				case "attempts":
					return "/api/orders/{id}/attempts"
				case "delivery-quote":
					return "/api/orders/{id}/delivery-quote"
				}
			}
			if len(parts) == 5 && parts[3] == "delivery-quote" {
				switch parts[4] {
				case "accept":
					return "/api/orders/{id}/delivery-quote/accept"
				case "reject":
					return "/api/orders/{id}/delivery-quote/reject"
				}
			}
		case "admin":
			if len(parts) == 3 {
				switch parts[2] {
				case "users":
					return "/api/admin/users"
				case "audit-logs":
					return "/api/admin/audit-logs"
				}
			}
			if len(parts) == 4 && parts[2] == "users" {
				return "/api/admin/users/{id}"
			}
			if len(parts) == 5 && parts[2] == "users" {
				switch parts[4] {
				case "role":
					return "/api/admin/users/{id}/role"
				case "status":
					return "/api/admin/users/{id}/status"
				}
			}
			if len(parts) >= 3 && parts[2] == "payments" {
				if len(parts) == 4 {
					switch parts[3] {
					case "unresolved":
						return "/api/admin/payments/unresolved"
					case "reconcile":
						return "/api/admin/payments/reconcile"
					case "exceptions":
						return "/api/admin/payments/exceptions"
					case "manual-settlement":
						return "/api/admin/payments/manual-settlement"
					case "audit-ledger":
						return "/api/admin/payments/audit-ledger"
					}
				}
				if len(parts) == 5 && parts[3] == "exceptions" {
					return "/api/admin/payments/exceptions/{id}"
				}
				if len(parts) == 6 && parts[3] == "exceptions" && parts[5] == "resolve" {
					return "/api/admin/payments/exceptions/{id}/resolve"
				}
			}
		case "payments":
			if len(parts) == 4 {
				if parts[2] == "mpesa" && parts[3] == "callback" {
					return "/api/payments/mpesa/callback"
				}
				if parts[2] == "ncba" && parts[3] == "ipn" {
					return "/api/payments/ncba/ipn"
				}
			}
		}
	}

	// Non-api orders prefix
	if parts[0] == "orders" {
		if len(parts) == 3 {
			switch parts[2] {
			case "payment-status":
				return "/orders/{id}/payment-status"
			case "retry-payment":
				return "/orders/{id}/retry-payment"
			case "attempts":
				return "/orders/{id}/attempts"
			case "delivery-quote":
				return "/orders/{id}/delivery-quote"
			}
		}
		if len(parts) == 4 && parts[2] == "delivery-quote" {
			switch parts[3] {
			case "accept":
				return "/orders/{id}/delivery-quote/accept"
			case "reject":
				return "/orders/{id}/delivery-quote/reject"
			}
		}
	}

	return "/unmapped"
}

// NormalizeMethod normalizes an HTTP method into a canonical, bounded label.
// Standard HTTP methods (GET, POST, PUT, DELETE, PATCH, HEAD, OPTIONS) are recognized
// and returned uppercase. Any custom, unrecognized, or arbitrary method is collapsed to
// "OTHER" to eliminate cardinality exhaustion vectors.
func NormalizeMethod(method string) string {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "GET":
		return "GET"
	case "POST":
		return "POST"
	case "PUT":
		return "PUT"
	case "DELETE":
		return "DELETE"
	case "PATCH":
		return "PATCH"
	case "HEAD":
		return "HEAD"
	case "OPTIONS":
		return "OPTIONS"
	default:
		return "OTHER"
	}
}
