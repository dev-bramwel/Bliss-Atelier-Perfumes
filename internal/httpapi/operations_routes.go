package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/auth"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/operations"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
)

type OperationsService interface {
	CanAccess(context.Context, string, string) (bool, error)
	Inventory(context.Context, int, int) (*operations.InventoryPage, error)
	Orders(context.Context, string, int, int) (*operations.OrderPage, error)
	Assign(context.Context, string, string, *auth.User, string) error
	Fulfill(context.Context, string, *auth.User, string) error
	Restock(context.Context, *auth.User, string, string, int, string, string) error
}

func pageBounds(r *http.Request) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit < 1 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// Administrative routes never accept the historical shared manager secret. A
// revoked cookie cannot fall back to another credential supplied in a header.
func administrativeSessions(next http.Handler, service *auth.Service, checker *TrustedProxyChecker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		path := strings.TrimPrefix(r.URL.Path, "/api")
		admin := path == "/admin" || strings.HasPrefix(path, "/admin/")
		parts := strings.Split(strings.Trim(path, "/"), "/")
		userStatus := r.Method == http.MethodPost && len(parts) == 4 && parts[0] == "admin" && parts[1] == "users" && parts[3] == "status" && parts[2] != ""
		ops := strings.HasPrefix(path, "/operations/")
		quote := r.Method == http.MethodPost && strings.HasPrefix(path, "/orders/") && strings.HasSuffix(path, "/delivery-quote")
		fulfill := r.Method == http.MethodPost && strings.HasPrefix(path, "/orders/") && (strings.HasSuffix(path, "/fulfill") || strings.HasSuffix(path, "/assign"))
		invalidate := path == "/catalog/invalidate"
		if !admin && !ops && !quote && !fulfill && !invalidate {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie("bliss_session")
		if err != nil || cookie.Value == "" {
			respond(w, 401, map[string]string{"error": "Please sign in with your staff account."})
			return
		}
		user, ses, err := AuthenticateRequest(r.Context(), r, service, checker)
		if err != nil {
			respond(w, 401, map[string]string{"error": "Your session has ended. Please sign in again."})
			return
		}
		owner := user.Role == auth.RoleOwner
		if (admin && !owner && !userStatus) || ((ops || fulfill || userStatus) && !auth.IsStaff(user.Role)) {
			respond(w, 403, map[string]string{"error": "Your role does not permit this action."})
			return
		}
		if quote && !owner {
			respond(w, 401, map[string]string{"error": "Owner access is required."})
			return
		}
		if invalidate && !auth.CanManageInventory(*user) {
			respond(w, 403, map[string]string{"error": "Your role does not permit this action."})
			return
		}
		if !RequireCSRF(r, ses) {
			respond(w, 403, map[string]string{"error": "Invalid CSRF token. Refresh your sign-in context and retry."})
			return
		}
		RefreshSessionCookie(w, r, ses)
		next.ServeHTTP(w, r.WithContext(ContextWithAuth(r.Context(), user, ses)))
	})
}

func registerOperationsRoutes(mux *http.ServeMux, store OperationsService, checker *TrustedProxyChecker) {
	handleOrders := func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			respond(w, 503, map[string]string{"error": "Operations unavailable"})
			return
		}
		u := UserFromContext(r.Context())
		scope := ""
		if u.Role == auth.RoleEmployee {
			scope = u.ID
		}
		limit, offset := pageBounds(r)
		p, err := store.Orders(r.Context(), scope, limit, offset)
		if err != nil {
			respond(w, 503, map[string]string{"error": "Orders unavailable"})
			return
		}
		respond(w, 200, p)
	}
	handleInventory := func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			respond(w, 503, map[string]string{"error": "Inventory unavailable"})
			return
		}
		u := UserFromContext(r.Context())
		if u != nil && !auth.CanManageInventory(*u) {
			respond(w, 403, map[string]string{"error": "Your role does not permit this action."})
			return
		}
		limit, offset := pageBounds(r)
		p, err := store.Inventory(r.Context(), limit, offset)
		if err != nil {
			respond(w, 503, map[string]string{"error": "Inventory unavailable"})
			return
		}
		respond(w, 200, p)
	}
	handleRestock := func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			respond(w, 503, map[string]string{"error": "Operations unavailable"})
			return
		}
		u := UserFromContext(r.Context())
		if u == nil || !auth.CanManageInventory(*u) {
			respond(w, 403, map[string]string{"error": "Your role does not permit this action."})
			return
		}
		var req struct {
			BatchID   string `json:"batch_id"`
			ProductID string `json:"product_id"`
			Quantity  int    `json:"quantity"`
			Reference string `json:"reference"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respond(w, 400, map[string]string{"error": "Invalid request payload"})
			return
		}
		if req.ProductID == "" || req.Quantity == 0 || strings.TrimSpace(req.Reference) == "" {
			respond(w, 400, map[string]string{"error": "Product ID, non-zero quantity, and reference note are required."})
			return
		}
		err := store.Restock(r.Context(), u, req.BatchID, req.ProductID, req.Quantity, req.Reference, SafeClientIP(r, checker))
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrPermissionDenied):
				respond(w, 403, map[string]string{"error": "Your role does not permit this action."})
			case errors.Is(err, operations.ErrProductNotFound):
				respond(w, 404, map[string]string{"error": err.Error()})
			case errors.Is(err, operations.ErrBatchDigestMismatch):
				respond(w, 409, map[string]string{"error": err.Error()})
			case errors.Is(err, operations.ErrUnopenedProduct),
				errors.Is(err, operations.ErrAuthorityRequired),
				errors.Is(err, operations.ErrInvalidQuantity),
				errors.Is(err, operations.ErrInsufficientStock),
				errors.Is(err, operations.ErrInsufficientUnreserved),
				errors.Is(err, operations.ErrStockLimitExceeded):
				respond(w, 400, map[string]string{"error": err.Error()})
			default:
				log.Printf("ERROR: restock storage failure: %v", err)
				respond(w, 500, map[string]string{"error": "Failed to update inventory due to an internal server error."})
			}
			return
		}
		status := "restocked"
		if req.Quantity < 0 {
			status = "adjusted"
		}
		respond(w, 200, map[string]any{"status": status, "product_id": req.ProductID, "quantity": req.Quantity})
	}
	handleMutation := func(assign bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			u := UserFromContext(r.Context())
			if assign && u.Role != auth.RoleOwner {
				respond(w, 403, map[string]string{"error": "Only the owner can assign orders."})
				return
			}
			if store == nil {
				respond(w, 503, map[string]string{"error": "Operations unavailable"})
				return
			}
			var err error
			if assign {
				var body struct {
					UserID string `json:"user_id"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.UserID == "" {
					respond(w, 400, map[string]string{"error": "An employee user ID is required."})
					return
				}
				err = store.Assign(r.Context(), r.PathValue("id"), body.UserID, u, SafeClientIP(r, checker))
			} else {
				err = store.Fulfill(r.Context(), r.PathValue("id"), u, SafeClientIP(r, checker))
			}
			switch {
			case errors.Is(err, auth.ErrPermissionDenied):
				respond(w, 403, map[string]string{"error": "This order is not assigned to you."})
			case errors.Is(err, orders.ErrOrderNotFound):
				respond(w, 404, map[string]string{"error": "Order not found"})
			case errors.Is(err, operations.ErrConflict):
				respond(w, 409, map[string]string{"error": "The order must be paid and awaiting dispatch, or the selected employee is inactive."})
			case err != nil:
				respond(w, 503, map[string]string{"error": "Operation could not be confirmed. Refresh the order before retrying."})
			default:
				respond(w, 200, map[string]string{"status": "updated"})
			}
		}
	}
	for _, prefix := range []string{"", "/api"} {
		mux.HandleFunc("GET "+prefix+"/operations/orders", handleOrders)
		mux.HandleFunc("GET "+prefix+"/operations/inventory", handleInventory)
		mux.HandleFunc("POST "+prefix+"/operations/inventory/restock", handleRestock)
		mux.HandleFunc("POST "+prefix+"/operations/inventory/adjust", handleRestock)
		mux.HandleFunc("POST "+prefix+"/orders/{id}/assign", handleMutation(true))
		mux.HandleFunc("POST "+prefix+"/orders/{id}/fulfill", handleMutation(false))
	}
}
