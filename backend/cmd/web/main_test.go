package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/catalog"
)

type mockCatalog struct {
	products []catalog.Product
	err      error
}

func (m *mockCatalog) Products(ctx context.Context) ([]catalog.Product, error) {
	return m.products, m.err
}

func TestProxyFailureIsSafe(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/orders", nil)
	response := httptest.NewRecorder()
	handler(nil).ServeHTTP(response, request)
	if response.Code != 502 {
		t.Fatalf("expected 502, got %d", response.Code)
	}
	body := response.Body.String()
	if body != `{"error":"API unavailable"}` && body != `{"error":"API unavailable"}`+"\n" {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestWebRoutes(t *testing.T) {
	h := handler(nil)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantBody   string
	}{
		{"root", http.MethodGet, "/", 200, "Bliss Atelier Perfumes"},
		{"index html alias", http.MethodGet, "/index.html", 200, "Bliss Atelier Perfumes"},
		{"cart", http.MethodGet, "/cart", 200, "Your Cart"},
		{"cart html alias", http.MethodGet, "/cart.html", 200, "Your Cart"},
		{"checkout", http.MethodGet, "/checkout", 200, "Checkout"},
		{"checkout html alias", http.MethodGet, "/checkout.html", 200, `meta name="api-base-url"`},
		{"static asset", http.MethodGet, "/static/style.css", 200, ""},
		{"legacy root asset", http.MethodGet, "/checkout.js", 200, ""},
		{"live health check", http.MethodGet, "/health/live", 200, `{"status":"alive"}`},
		{"api live health check", http.MethodGet, "/api/health/live", 200, `{"status":"alive"}`},
		{"ready health check unavailable", http.MethodGet, "/health/ready", 503, `{"status":"unavailable"}`},
		{"api ready health check unavailable", http.MethodGet, "/api/health/ready", 503, `{"status":"unavailable"}`},
		{"api catalog unavailable", http.MethodGet, "/api/catalog", 503, `{"error":"catalog unavailable"}`},
		{"not found page", http.MethodGet, "/unknown-page-not-found", 404, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("path %s: expected %d, got %d", tt.path, tt.wantStatus, rec.Code)
			}
			if tt.wantBody != "" && !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("path %s: expected body containing %q, got %q", tt.path, tt.wantBody, rec.Body.String())
			}
		})
	}
}

func TestReadyWithStore(t *testing.T) {
	store := &mockCatalog{
		products: []catalog.Product{
			{ID: "p1", Name: "Perfume One", PriceKes: 1000, Available: true},
		},
	}
	h := handler(store)

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	reqCatalog := httptest.NewRequest(http.MethodGet, "/api/catalog", nil)
	recCatalog := httptest.NewRecorder()
	h.ServeHTTP(recCatalog, reqCatalog)
	if recCatalog.Code != 200 {
		t.Fatalf("expected 200, got %d", recCatalog.Code)
	}
	if !strings.Contains(recCatalog.Body.String(), "Perfume One") {
		t.Fatalf("expected catalog body to contain product name, got %s", recCatalog.Body.String())
	}
}
