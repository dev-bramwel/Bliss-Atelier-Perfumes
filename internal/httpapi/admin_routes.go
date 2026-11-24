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

	// 1. GET /admin/users & /api/admin/users - List users respecting Upward Visibility & Downward Isolation
	handleListUsers := func(w http.ResponseWriter, r *http.Request) {
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
		limit, offset := pageBounds(r)
		users, err := authService.ListUsersPage(r.Context(), user, filterRole, limit, offset)
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
	}
	mux.HandleFunc("GET /admin/users", handleListUsers)
	mux.HandleFunc("GET /api/admin/users", handleListUsers)

	// 2. POST /admin/users/{id}/role & /api/admin/users/{id}/role - Modify user role
	handleUpdateRole := func(w http.ResponseWriter, r *http.Request) {
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
			if errors.Is(err, auth.ErrCannotDemoteSoleOwner) || errors.Is(err, auth.ErrCannotDeactivateSoleOwner) {
				http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusConflict)
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
	}
	mux.HandleFunc("POST /admin/users/{id}/role", handleUpdateRole)
	mux.HandleFunc("POST /api/admin/users/{id}/role", handleUpdateRole)

	// 3. POST /admin/users/{id}/status & /api/admin/users/{id}/status - Activate or deactivate user account
	handleUpdateStatus := func(w http.ResponseWriter, r *http.Request) {
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
			if errors.Is(err, auth.ErrCannotDeactivateSoleOwner) {
				http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusConflict)
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
	}
	mux.HandleFunc("POST /admin/users/{id}/status", handleUpdateStatus)
	mux.HandleFunc("POST /api/admin/users/{id}/status", handleUpdateStatus)

	// POST /admin/users/{id}/permissions & /api/admin/users/{id}/permissions - Update employee inventory permissions
	handleUpdatePermissions := func(w http.ResponseWriter, r *http.Request) {
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
			CanManageInventory *bool `json:"can_manage_inventory"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CanManageInventory == nil {
			http.Error(w, `{"error":"invalid request payload; can_manage_inventory is required"}`, http.StatusBadRequest)
			return
		}

		clientIP := SafeClientIP(r, checker)
		if err := authService.UpdateUserPermissions(r.Context(), user, targetUserID, *req.CanManageInventory, clientIP); err != nil {
			if errors.Is(err, auth.ErrPermissionDenied) {
				http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusForbidden)
				return
			}
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":               "updated",
			"user_id":              targetUserID,
			"can_manage_inventory": *req.CanManageInventory,
		})
	}
	mux.HandleFunc("POST /admin/users/{id}/permissions", handleUpdatePermissions)
	mux.HandleFunc("POST /api/admin/users/{id}/permissions", handleUpdatePermissions)

	// 4. GET /admin/audit-logs & /api/admin/audit-logs - Inspect audit logs with Downward Isolation
	handleListAudit := func(w http.ResponseWriter, r *http.Request) {
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
	}
	mux.HandleFunc("GET /admin/audit-logs", handleListAudit)
	mux.HandleFunc("GET /api/admin/audit-logs", handleListAudit)
}
