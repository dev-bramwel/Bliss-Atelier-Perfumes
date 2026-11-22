package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
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

	// 1. GET /auth/google & /api/auth/google - Initiate OAuth2 Authorization Code flow with PKCE
	handleGoogleLogin := func(w http.ResponseWriter, r *http.Request) {
		returnURL := r.URL.Query().Get("return_url")
		clientIP := SafeClientIP(r, checker)

		authURL, bindingCookie, err := authService.BeginLogin(r.Context(), returnURL, clientIP, r.UserAgent())
		if err != nil {
			http.Error(w, `{"error":"failed to initiate google login"}`, http.StatusInternalServerError)
			return
		}

		if mockRole := r.URL.Query().Get("mock_role"); mockRole != "" {
			if strings.Contains(authURL, "?") {
				authURL += "&mock_role=" + url.QueryEscape(mockRole)
			} else {
				authURL += "?mock_role=" + url.QueryEscape(mockRole)
			}
		}

		http.SetCookie(w, &http.Cookie{
			Name:     "bliss_oauth_tx",
			Value:    bindingCookie,
			Path:     "/",
			MaxAge:   600, // 10 minutes
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		})

		http.Redirect(w, r, authURL, http.StatusFound)
	}
	mux.HandleFunc("GET /auth/google", handleGoogleLogin)
	mux.HandleFunc("GET /api/auth/google", handleGoogleLogin)

	// 2. GET /auth/google/callback & /api/auth/google/callback - Process Google OIDC callback
	handleCallback := func(w http.ResponseWriter, r *http.Request) {
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		if code == "" && r.URL.Query().Get("mock") != "" {
			mockRole := r.URL.Query().Get("mock_role")
			if mockRole == "" {
				mockRole = "owner"
			}
			code = "mock-" + mockRole
		}
		state := strings.TrimSpace(r.URL.Query().Get("state"))
		clientIP := SafeClientIP(r, checker)

		txCookie, err := r.Cookie("bliss_oauth_tx")
		if err != nil || txCookie.Value == "" {
			http.Error(w, `{"error":"missing oauth transaction cookie"}`, http.StatusBadRequest)
			return
		}

		user, ses, rawToken, csrfToken, returnURL, err := authService.CompleteLogin(r.Context(), state, code, txCookie.Value, clientIP, r.UserAgent())
		if err != nil {
			if errors.Is(err, auth.ErrAccountDeactivated) {
				w.Header().Set("Cache-Control", "no-store")
				if strings.Contains(r.Header.Get("Accept"), "application/json") || r.URL.Query().Get("format") == "json" {
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.WriteHeader(http.StatusForbidden)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"error":   "account is deactivated",
						"message": "Your account has been deactivated. Please contact support to reactivate your account.",
					})
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(deactivatedHTMLNotice))
				return
			}
			http.Error(w, `{"error":"authentication failed: `+err.Error()+`"}`, http.StatusUnauthorized)
			return
		}

		// Clear the transient oauth transaction cookie
		http.SetCookie(w, &http.Cookie{
			Name:     "bliss_oauth_tx",
			Value:    "",
			Path:     "/",
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

		// Ensure returnURL is safe relative path
		if !auth.IsSafeReturnURL(returnURL) || returnURL == "" {
			returnURL = "/"
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
	}
	mux.HandleFunc("GET /auth/google/callback", handleCallback)
	mux.HandleFunc("GET /api/auth/google/callback", handleCallback)

	// 3. GET /auth/me & /api/auth/me - Current user profile and fresh CSRF token
	handleMe := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
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
	}
	mux.HandleFunc("GET /auth/me", handleMe)
	mux.HandleFunc("GET /api/auth/me", handleMe)

	// 4. POST /auth/logout & /api/auth/logout - Revoke current session
	handleLogout := func(w http.ResponseWriter, r *http.Request) {
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
		if err := authService.RevokeSession(r.Context(), rawToken); err != nil {
			http.Error(w, `{"error":"failed to revoke session"}`, http.StatusInternalServerError)
			return
		}

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
	}
	mux.HandleFunc("POST /auth/logout", handleLogout)
	mux.HandleFunc("POST /api/auth/logout", handleLogout)

	// 5. POST /auth/revoke-all & /api/auth/revoke-all - Revoke all active sessions for current user across all devices
	handleRevokeAll := func(w http.ResponseWriter, r *http.Request) {
		user, ses, err := AuthenticateRequest(r.Context(), r, authService, checker)
		if err != nil {
			http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
			return
		}

		if !RequireCSRF(r, ses) {
			http.Error(w, `{"error":"invalid csrf token"}`, http.StatusForbidden)
			return
		}

		if err := authService.RevokeAllUserSessions(r.Context(), user.ID); err != nil {
			http.Error(w, `{"error":"failed to revoke user sessions"}`, http.StatusInternalServerError)
			return
		}

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
	}
	mux.HandleFunc("POST /auth/revoke-all", handleRevokeAll)
	mux.HandleFunc("POST /api/auth/revoke-all", handleRevokeAll)

	// 6. POST /auth/deactivate & /api/auth/deactivate - Deactivate own account, purge sessions, and write audit trail
	handleDeactivate := func(w http.ResponseWriter, r *http.Request) {
		user, ses, err := AuthenticateRequest(r.Context(), r, authService, checker)
		if err != nil {
			http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
			return
		}

		if !RequireCSRF(r, ses) {
			http.Error(w, `{"error":"invalid csrf token"}`, http.StatusForbidden)
			return
		}

		clientIP := SafeClientIP(r, checker)
		if err := authService.DeactivateSelf(r.Context(), user, clientIP); err != nil {
			if errors.Is(err, auth.ErrCannotDeactivateSoleOwner) {
				http.Error(w, `{"error":"cannot deactivate the sole owner of the store"}`, http.StatusConflict)
				return
			}
			http.Error(w, `{"error":"failed to deactivate account: `+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}

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
			"status":  "deactivated",
			"message": "Account successfully deactivated. All active sessions have been revoked.",
			"user_id": user.ID,
		})
	}
	mux.HandleFunc("POST /auth/deactivate", handleDeactivate)
	mux.HandleFunc("POST /api/auth/deactivate", handleDeactivate)
}

const deactivatedHTMLNotice = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Account Deactivated — Bliss Atelier</title>
  <style>
    :root {
      --bg: #0d0c0a;
      --gold: #cfb991;
      --gold-soft: #eadec4;
      --card-bg: rgba(26, 24, 21, 0.85);
      --border: rgba(207, 185, 145, 0.25);
      --text: #f5f2eb;
      --muted: #a8a29e;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body {
      background-color: var(--bg);
      color: var(--text);
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      min-height: 100vh;
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 1.5rem;
    }
    .panel {
      max-width: 480px;
      width: 100%;
      background: var(--card-bg);
      border: 1px solid var(--border);
      border-radius: 20px;
      padding: 2.5rem 2rem;
      text-align: center;
      box-shadow: 0 20px 40px rgba(0,0,0,0.6);
      backdrop-filter: blur(16px);
    }
    .badge {
      display: inline-block;
      font-size: 0.75rem;
      letter-spacing: 0.15em;
      text-transform: uppercase;
      color: var(--gold);
      margin-bottom: 1rem;
    }
    h1 {
      font-family: "Cinzel", "Cormorant Garamond", Georgia, serif;
      font-size: 1.75rem;
      font-weight: 500;
      color: #fff;
      margin-bottom: 1rem;
    }
    p {
      color: var(--muted);
      font-size: 0.95rem;
      line-height: 1.6;
      margin-bottom: 1.5rem;
    }
    .support-box {
      background: rgba(255,255,255,0.03);
      border: 1px solid rgba(255,255,255,0.08);
      border-radius: 12px;
      padding: 1rem;
      margin-bottom: 2rem;
      font-size: 0.875rem;
      color: var(--text);
    }
    .support-box strong { color: var(--gold-soft); }
    .btn {
      display: inline-block;
      width: 100%;
      padding: 0.85rem 1.5rem;
      border-radius: 14px;
      background: var(--gold);
      color: #12100e;
      text-decoration: none;
      font-weight: 600;
      font-size: 0.9rem;
      transition: opacity 0.2s ease;
    }
    .btn:hover { opacity: 0.9; }
  </style>
</head>
<body>
  <div class="panel">
    <span class="badge">Bliss Atelier · Access Notice</span>
    <h1>Account Deactivated</h1>
    <p>Your Bliss Atelier account is currently deactivated and all active sessions have been securely purged.</p>
    <div class="support-box">
      To restore access or request reactivation, please contact atelier support at <strong>support@blissatelier.com</strong> or call <strong>+254 700 000 000</strong>.
    </div>
    <a href="/" class="btn">Return to Collection</a>
  </div>
</body>
</html>`

