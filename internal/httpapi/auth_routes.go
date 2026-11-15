package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/auth"
)

type contextKey string

const (
	userContextKey    contextKey = "bliss_user"
	sessionContextKey contextKey = "bliss_session"
)

// ExtractSessionToken retrieves the session token from cookie or Authorization header.
func ExtractSessionToken(r *http.Request) string {
	if c, err := r.Cookie("bliss_session"); err == nil && c.Value != "" {
		return strings.TrimSpace(c.Value)
	}
	authHdr := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(authHdr, "Bearer ") {
		candidate := strings.TrimSpace(strings.TrimPrefix(authHdr, "Bearer "))
		// Guest tokens start with "gt_" and are not session tokens
		if !strings.HasPrefix(candidate, "gt_") {
			return candidate
		}
	}
	return ""
}

// AuthenticateRequest validates session credentials and returns the active user and session.
func AuthenticateRequest(ctx context.Context, r *http.Request, authService *auth.Service, checker *TrustedProxyChecker) (*auth.User, *auth.UserSession, error) {
	if authService == nil {
		return nil, nil, auth.ErrSessionNotFound
	}
	rawToken := ExtractSessionToken(r)
	if rawToken == "" {
		return nil, nil, auth.ErrSessionNotFound
	}
	clientIP := SafeClientIP(r, checker)
	ses, user, err := authService.ValidateSession(ctx, rawToken, clientIP)
	if err != nil {
		return nil, nil, err
	}
	return user, ses, nil
}

// ContextWithAuth stores authenticated user and session into context.
func ContextWithAuth(ctx context.Context, u *auth.User, s *auth.UserSession) context.Context {
	ctx = context.WithValue(ctx, userContextKey, u)
	return context.WithValue(ctx, sessionContextKey, s)
}

// UserFromContext retrieves the authenticated user from context, if present.
func UserFromContext(ctx context.Context) *auth.User {
	if v := ctx.Value(userContextKey); v != nil {
		if u, ok := v.(*auth.User); ok {
			return u
		}
	}
	return nil
}

// SessionFromContext retrieves the active session from context, if present.
func SessionFromContext(ctx context.Context) *auth.UserSession {
	if v := ctx.Value(sessionContextKey); v != nil {
		if s, ok := v.(*auth.UserSession); ok {
			return s
		}
	}
	return nil
}

// RefreshSessionCookie reissues the session cookie and CSRF cookie with renewed MaxAge matching sliding inactivity.
func RefreshSessionCookie(w http.ResponseWriter, r *http.Request, ses *auth.UserSession) {
	if w == nil || r == nil || ses == nil {
		return
	}
	cookie, err := r.Cookie("bliss_session")
	if err != nil || cookie == nil || cookie.Value == "" {
		return
	}
	remaining := int(time.Until(ses.ExpiresAt).Seconds())
	if remaining <= 0 {
		return
	}
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name:     "bliss_session",
		Value:    cookie.Value,
		Path:     "/",
		MaxAge:   remaining,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
	if ses.CSRFSecret != "" {
		csrfToken := auth.GenerateCSRFToken(ses.CSRFSecret)
		http.SetCookie(w, &http.Cookie{
			Name:     "bliss_csrf",
			Value:    csrfToken,
			Path:     "/",
			MaxAge:   remaining,
			HttpOnly: false,
			SameSite: http.SameSiteLaxMode,
			Secure:   secure,
		})
	}
}

// RequireCSRF verifies that state-modifying requests authenticated via cookie carry a valid CSRF header.
func RequireCSRF(r *http.Request, ses *auth.UserSession) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}
	// If authenticated via cookie, require X-CSRF-Token
	if _, err := r.Cookie("bliss_session"); err == nil {
		token := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
		if token == "" {
			token = strings.TrimSpace(r.Header.Get("X-Csrf-Token"))
		}
		if token == "" {
			return false
		}
		return auth.ValidateCSRFToken(ses.CSRFSecret, token, 24*time.Hour)
	}
	return true
}

func registerAuthRoutes(mux *http.ServeMux, authService *auth.Service, checker *TrustedProxyChecker) {
	if authService == nil {
		return
	}

	// 1. GET /api/auth/google - Initiate OAuth2 Authorization Code flow with PKCE
	mux.HandleFunc("GET /api/auth/google", func(w http.ResponseWriter, r *http.Request) {
		returnURL := r.URL.Query().Get("return_url")
		clientIP := SafeClientIP(r, checker)

		authURL, bindingCookie, err := authService.BeginLogin(r.Context(), returnURL, clientIP, r.UserAgent())
		if err != nil {
			http.Error(w, `{"error":"failed to initiate google login"}`, http.StatusInternalServerError)
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     "bliss_oauth_tx",
			Value:    bindingCookie,
			Path:     "/api/auth",
			MaxAge:   600, // 10 minutes
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		})

		http.Redirect(w, r, authURL, http.StatusFound)
	})

	// 2. GET /api/auth/google/callback - Process Google OIDC callback
	mux.HandleFunc("GET /api/auth/google/callback", func(w http.ResponseWriter, r *http.Request) {
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		state := strings.TrimSpace(r.URL.Query().Get("state"))
		clientIP := SafeClientIP(r, checker)

		txCookie, err := r.Cookie("bliss_oauth_tx")
		if err != nil || txCookie.Value == "" {
			http.Error(w, `{"error":"missing oauth transaction cookie"}`, http.StatusBadRequest)
			return
		}

		user, ses, rawToken, csrfToken, err := authService.CompleteLogin(r.Context(), state, code, txCookie.Value, clientIP, r.UserAgent())
		if err != nil {
			http.Error(w, `{"error":"authentication failed: `+err.Error()+`"}`, http.StatusUnauthorized)
			return
		}

		// Clear the transient oauth transaction cookie
		http.SetCookie(w, &http.Cookie{
			Name:     "bliss_oauth_tx",
			Value:    "",
			Path:     "/api/auth",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})

		// Set the authoritative session cookie
		maxAgeSeconds := int(time.Until(ses.ExpiresAt).Seconds())
		if maxAgeSeconds <= 0 {
			maxAgeSeconds = 3600
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "bliss_session",
			Value:    rawToken,
			Path:     "/",
			MaxAge:   maxAgeSeconds,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		})

		// Set CSRF token cookie for browser javascript consumption
		http.SetCookie(w, &http.Cookie{
			Name:     "bliss_csrf",
			Value:    csrfToken,
			Path:     "/",
			MaxAge:   maxAgeSeconds,
			HttpOnly: false, // Accessible to client JavaScript for X-CSRF-Token header
			SameSite: http.SameSiteLaxMode,
			Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		})

		returnURL := "/"
		if r.URL.Query().Get("return_url") != "" && auth.IsSafeReturnURL(r.URL.Query().Get("return_url")) {
			returnURL = r.URL.Query().Get("return_url")
		}

		if strings.Contains(r.Header.Get("Accept"), "application/json") || r.URL.Query().Get("format") == "json" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-CSRF-Token", csrfToken)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":     "authenticated",
				"user":       user,
				"csrf_token": csrfToken,
				"return_url": returnURL,
			})
			return
		}

		http.Redirect(w, r, returnURL, http.StatusFound)
	})

	// 3. GET /api/auth/me - Current user profile and fresh CSRF token
	mux.HandleFunc("GET /api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		user, ses, err := AuthenticateRequest(r.Context(), r, authService, checker)
		if err != nil {
			http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
			return
		}
		RefreshSessionCookie(w, r, ses)

		csrfToken := auth.GenerateCSRFToken(ses.CSRFSecret)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-CSRF-Token", csrfToken)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user":       user,
			"csrf_token": csrfToken,
		})
	})

	// 4. POST /api/auth/logout - Revoke current session
	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		user, ses, err := AuthenticateRequest(r.Context(), r, authService, checker)
		if err != nil {
			http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
			return
		}

		if !RequireCSRF(r, ses) {
			http.Error(w, `{"error":"invalid csrf token"}`, http.StatusForbidden)
			return
		}

		rawToken := ExtractSessionToken(r)
		_ = authService.RevokeSession(r.Context(), rawToken)

		// Clear cookies
		http.SetCookie(w, &http.Cookie{
			Name:     "bliss_session",
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		http.SetCookie(w, &http.Cookie{
			Name:     "bliss_csrf",
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: false,
			SameSite: http.SameSiteLaxMode,
		})

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "logged_out",
			"user_id": user.ID,
		})
	})

	// 5. POST /api/auth/revoke-all - Revoke all active sessions for current user across all devices
	mux.HandleFunc("POST /api/auth/revoke-all", func(w http.ResponseWriter, r *http.Request) {
		user, ses, err := AuthenticateRequest(r.Context(), r, authService, checker)
		if err != nil {
			http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
			return
		}

		if !RequireCSRF(r, ses) {
			http.Error(w, `{"error":"invalid csrf token"}`, http.StatusForbidden)
			return
		}

		_ = authService.RevokeAllUserSessions(r.Context(), user.ID)

		http.SetCookie(w, &http.Cookie{
			Name:     "bliss_session",
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		http.SetCookie(w, &http.Cookie{
			Name:     "bliss_csrf",
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: false,
			SameSite: http.SameSiteLaxMode,
		})

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "all_sessions_revoked",
			"user_id": user.ID,
		})
	})
}
