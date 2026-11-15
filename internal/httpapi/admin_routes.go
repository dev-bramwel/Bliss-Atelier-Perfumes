package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/auth"
)

func registerAdminRoutes(mux *http.ServeMux, authService *auth.Service, checker *TrustedProxyChecker) {
	if authService == nil {
		return
	}

	// 1. GET /api/admin/users - List users respecting Upward Visibility & Downward Isolation
	mux.HandleFunc("GET /api/admin/users", func(w http.ResponseWriter, r *http.Request) {
		user, ses, err := AuthenticateRequest(r.Context(), r, authService, checker)
		if err != nil {
			http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
			return
		}
		RefreshSessionCookie(w, r, ses)

		if !auth.IsStaff(user.Role) {
			http.Error(w, `{"error":"forbidden: staff role required"}`, http.StatusForbidden)
			return
		}

		filterRole := strings.TrimSpace(r.URL.Query().Get("role"))
		users, err := authService.ListUsers(r.Context(), user, filterRole)
		if err != nil {
			if errors.Is(err, auth.ErrPermissionDenied) {
				http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusForbidden)
				return
			}
			http.Error(w, `{"error":"failed to list users"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"users": users,
		})
	})

	// 2. POST /api/admin/users/{id}/role - Modify user role
	mux.HandleFunc("POST /api/admin/users/{id}/role", func(w http.ResponseWriter, r *http.Request) {
		user, ses, err := AuthenticateRequest(r.Context(), r, authService, checker)
		if err != nil {
			http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
			return
		}
		RefreshSessionCookie(w, r, ses)

		if !RequireCSRF(r, ses) {
			http.Error(w, `{"error":"invalid csrf token"}`, http.StatusForbidden)
			return
		}

		targetUserID := r.PathValue("id")
		if targetUserID == "" {
			http.Error(w, `{"error":"target user id required"}`, http.StatusBadRequest)
			return
		}

		var req struct {
			Role string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Role == "" {
			http.Error(w, `{"error":"invalid request payload; role is required"}`, http.StatusBadRequest)
			return
		}

		clientIP := SafeClientIP(r, checker)
		if err := authService.UpdateUserRole(r.Context(), user, targetUserID, req.Role, clientIP); err != nil {
			if errors.Is(err, auth.ErrPermissionDenied) {
				http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusForbidden)
				return
			}
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "updated",
			"user_id": targetUserID,
			"role":    req.Role,
		})
	})

	// 3. POST /api/admin/users/{id}/status - Activate or deactivate user account
	mux.HandleFunc("POST /api/admin/users/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		user, ses, err := AuthenticateRequest(r.Context(), r, authService, checker)
		if err != nil {
			http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
			return
		}
		RefreshSessionCookie(w, r, ses)

		if !RequireCSRF(r, ses) {
			http.Error(w, `{"error":"invalid csrf token"}`, http.StatusForbidden)
			return
		}

		targetUserID := r.PathValue("id")
		if targetUserID == "" {
			http.Error(w, `{"error":"target user id required"}`, http.StatusBadRequest)
			return
		}

		var req struct {
			IsActive *bool `json:"is_active"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.IsActive == nil {
			http.Error(w, `{"error":"invalid request payload; is_active is required"}`, http.StatusBadRequest)
			return
		}

		clientIP := SafeClientIP(r, checker)
		if err := authService.UpdateUserStatus(r.Context(), user, targetUserID, *req.IsActive, clientIP); err != nil {
			if errors.Is(err, auth.ErrPermissionDenied) {
				http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusForbidden)
				return
			}
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "updated",
			"user_id":   targetUserID,
			"is_active": *req.IsActive,
		})
	})

	// 4. GET /api/admin/audit-logs - Inspect audit logs with Downward Isolation
	mux.HandleFunc("GET /api/admin/audit-logs", func(w http.ResponseWriter, r *http.Request) {
		user, ses, err := AuthenticateRequest(r.Context(), r, authService, checker)
		if err != nil {
			http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
			return
		}
		RefreshSessionCookie(w, r, ses)

		if !auth.IsStaff(user.Role) {
			http.Error(w, `{"error":"forbidden: staff role required"}`, http.StatusForbidden)
			return
		}

		limit := 50
		if lStr := r.URL.Query().Get("limit"); lStr != "" {
			if l, err := strconv.Atoi(lStr); err == nil && l > 0 && l <= 100 {
				limit = l
			}
		}

		offset := 0
		if oStr := r.URL.Query().Get("offset"); oStr != "" {
			if o, err := strconv.Atoi(oStr); err == nil && o >= 0 {
				offset = o
			}
		}

		logs, err := authService.InspectAuditLogs(r.Context(), user, limit, offset)
		if err != nil {
			if errors.Is(err, auth.ErrPermissionDenied) {
				http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusForbidden)
				return
			}
			http.Error(w, `{"error":"failed to inspect audit logs"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"audit_logs": logs,
		})
	})
}
