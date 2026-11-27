package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/auth"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/exchanges"
)

type ExchangeService interface {
	List(ctx context.Context, statusFilter string, limit, offset int) (*exchanges.ExchangePage, error)
	CreateRequest(ctx context.Context, actor *auth.User, in exchanges.CreateRequestInput, idempotencyKey, ip string) (*exchanges.ExchangeRequest, error)
	Inspect(ctx context.Context, actor *auth.User, exchangeID string, passed bool, notes, ip string) (*exchanges.ExchangeRequest, error)
	Decide(ctx context.Context, actor *auth.User, exchangeID string, approve bool, reason, ip string) (*exchanges.ExchangeRequest, error)
	Complete(ctx context.Context, actor *auth.User, exchangeID, topupReceipt, topupEvidence, ip string) (*exchanges.ExchangeRequest, error)
}

func registerExchangeRoutes(mux *http.ServeMux, store ExchangeService, checker *TrustedProxyChecker) {
	handleList := func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			respond(w, 503, map[string]string{"error": "Exchanges unavailable"})
			return
		}
		u := UserFromContext(r.Context())
		if u == nil || !auth.IsStaff(u.Role) {
			respond(w, 403, map[string]string{"error": "Your role does not permit this action."})
			return
		}
		limit, offset := pageBounds(r)
		page, err := store.List(r.Context(), r.URL.Query().Get("status"), limit, offset)
		if err != nil {
			respond(w, 503, map[string]string{"error": "Exchanges unavailable"})
			return
		}
		respond(w, 200, page)
	}

	handleCreate := func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			respond(w, 503, map[string]string{"error": "Exchanges unavailable"})
			return
		}
		u := UserFromContext(r.Context())
		var req struct {
			exchanges.CreateRequestInput
			RequestID string `json:"request_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respond(w, 400, map[string]string{"error": "Invalid request payload"})
			return
		}
		idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if idemKey == "" {
			idemKey = strings.TrimSpace(req.RequestID)
		}
		res, err := store.CreateRequest(r.Context(), u, req.CreateRequestInput, idemKey, SafeClientIP(r, checker))
		if err != nil {
			respondExchangeError(w, err)
			return
		}
		respond(w, 201, res)
	}

	handleInspect := func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			respond(w, 503, map[string]string{"error": "Exchanges unavailable"})
			return
		}
		u := UserFromContext(r.Context())
		var req struct {
			Passed bool   `json:"passed"`
			Notes  string `json:"notes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respond(w, 400, map[string]string{"error": "Invalid request payload"})
			return
		}
		res, err := store.Inspect(r.Context(), u, r.PathValue("id"), req.Passed, req.Notes, SafeClientIP(r, checker))
		if err != nil {
			respondExchangeError(w, err)
			return
		}
		respond(w, 200, res)
	}

	handleDecide := func(approve bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if store == nil {
				respond(w, 503, map[string]string{"error": "Exchanges unavailable"})
				return
			}
			u := UserFromContext(r.Context())
			var req struct {
				Reason string `json:"reason"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				respond(w, 400, map[string]string{"error": "Invalid request payload"})
				return
			}
			res, err := store.Decide(r.Context(), u, r.PathValue("id"), approve, req.Reason, SafeClientIP(r, checker))
			if err != nil {
				respondExchangeError(w, err)
				return
			}
			respond(w, 200, res)
		}
	}

	handleComplete := func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			respond(w, 503, map[string]string{"error": "Exchanges unavailable"})
			return
		}
		u := UserFromContext(r.Context())
		var req struct {
			TopupReceipt  string `json:"topup_receipt"`
			TopupEvidence string `json:"topup_evidence"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respond(w, 400, map[string]string{"error": "Invalid request payload"})
			return
		}
		res, err := store.Complete(r.Context(), u, r.PathValue("id"), req.TopupReceipt, req.TopupEvidence, SafeClientIP(r, checker))
		if err != nil {
			respondExchangeError(w, err)
			return
		}
		respond(w, 200, res)
	}

	for _, prefix := range []string{"", "/api"} {
		mux.HandleFunc("GET "+prefix+"/operations/exchanges", handleList)
		mux.HandleFunc("POST "+prefix+"/operations/exchanges", handleCreate)
		mux.HandleFunc("POST "+prefix+"/operations/exchanges/{id}/inspect", handleInspect)
		mux.HandleFunc("POST "+prefix+"/operations/exchanges/{id}/approve", handleDecide(true))
		mux.HandleFunc("POST "+prefix+"/operations/exchanges/{id}/reject", handleDecide(false))
		mux.HandleFunc("POST "+prefix+"/operations/exchanges/{id}/complete", handleComplete)
	}
}

func respondExchangeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrPermissionDenied):
		respond(w, 403, map[string]string{"error": "Your role does not permit this action."})
	case errors.Is(err, exchanges.ErrNotFound), errors.Is(err, exchanges.ErrOrderNotFound):
		respond(w, 404, map[string]string{"error": err.Error()})
	case errors.Is(err, exchanges.ErrInvalidTransition),
		errors.Is(err, exchanges.ErrIdempotencyConflict),
		errors.Is(err, exchanges.ErrQuantityExceeded),
		errors.Is(err, exchanges.ErrInspectionFailed):
		respond(w, 409, map[string]string{"error": err.Error()})
	case errors.Is(err, exchanges.ErrOrderNotPaid),
		errors.Is(err, exchanges.ErrWindowExpired),
		errors.Is(err, exchanges.ErrInvalidQuantity),
		errors.Is(err, exchanges.ErrInvalidProduct),
		errors.Is(err, exchanges.ErrItemNotInOrder),
		errors.Is(err, exchanges.ErrLowerValueExcluded),
		errors.Is(err, exchanges.ErrReasonRequired),
		errors.Is(err, exchanges.ErrInspectionNotesReq),
		errors.Is(err, exchanges.ErrTopupRequired),
		errors.Is(err, exchanges.ErrAuthorityRequired),
		errors.Is(err, exchanges.ErrUnopenedProduct),
		errors.Is(err, exchanges.ErrInsufficientStock),
		errors.Is(err, exchanges.ErrInsufficientUnreserved):
		respond(w, 400, map[string]string{"error": err.Error()})
	default:
		log.Printf("ERROR: exchange operation failure: %v", err)
		respond(w, 500, map[string]string{"error": "Exchange operation failed due to an internal server error."})
	}
}
