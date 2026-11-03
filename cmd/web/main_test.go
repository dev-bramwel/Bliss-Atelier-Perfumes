package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/catalog"
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
	handler("http://127.0.0.1:59999", nil).ServeHTTP(response, request)
	if response.Code != 502 {
		t.Fatalf("expected 502, got %d", response.Code)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected Cache-Control: no-store on 502 proxy error, got %q", response.Header().Get("Cache-Control"))
	}
	body := strings.TrimSpace(response.Body.String())
	if body != "API unavailable" {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestWebRoutes(t *testing.T) {
	h := handler("", nil)

	tests := []struct {
		name             string
		method           string
		path             string
		wantStatus       int
		wantBody         string
		wantCacheControl string
	}{
		{"root", http.MethodGet, "/", 200, "Bliss Atelier Perfumes", "no-store"},
		{"index html alias", http.MethodGet, "/index.html", 200, "Bliss Atelier Perfumes", "no-store"},
		{"cart", http.MethodGet, "/cart", 200, "Your Cart", "no-store"},
		{"cart html alias", http.MethodGet, "/cart.html", 200, "Your Cart", "no-store"},
		{"checkout", http.MethodGet, "/checkout", 200, "Checkout", "no-store"},
		{"checkout html alias", http.MethodGet, "/checkout.html", 200, `meta name="api-base-url"`, "no-store"},
		{"static asset", http.MethodGet, "/static/style.css", 200, "", ""},
		{"legacy root asset", http.MethodGet, "/checkout.js", 200, "", ""},
		{"live health check", http.MethodGet, "/health/live", 200, `{"status":"alive"}`, "no-store"},
		{"ready health check", http.MethodGet, "/health/ready", 200, `{"status":"ready"}`, "no-store"},
		{"not found page", http.MethodGet, "/unknown-page-not-found", 404, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("path %s: expected %d, got %d", tt.path, tt.wantStatus, rec.Code)
			}
			if tt.wantCacheControl != "" {
				if got := rec.Header().Get("Cache-Control"); got != tt.wantCacheControl {
					t.Fatalf("path %s: expected Cache-Control %q, got %q", tt.path, tt.wantCacheControl, got)
				}
			}
			if tt.wantBody != "" && !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("path %s: expected body containing %q, got %q", tt.path, tt.wantBody, rec.Body.String())
			}
		})
	}
}

func TestApiProxy(t *testing.T) {
	apiMux := http.NewServeMux()
	var readyCode int = http.StatusOK
	apiMux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(readyCode)
		if readyCode == http.StatusOK {
			_, _ = w.Write([]byte(`{"status":"ready"}`))
		} else {
			_, _ = w.Write([]byte(`{"status":"unavailable"}`))
		}
	})
	apiMux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"alive"}`))
	})
	apiMux.HandleFunc("GET /catalog", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"products": []catalog.Product{
				{ID: "p1", Name: "Perfume One", PriceKes: 1000, Available: true},
			},
		})
	})

	apiServer := httptest.NewServer(apiMux)
	defer apiServer.Close()

	h := handler(apiServer.URL, nil)

	// Test ready proxy 200
	req := httptest.NewRequest(http.MethodGet, "/api/health/ready", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"ready"`) {
		t.Fatalf("expected 200 ready, got %d %s", rec.Code, rec.Body.String())
	}

	// Test live proxy 200
	reqLive := httptest.NewRequest(http.MethodGet, "/api/health/live", nil)
	recLive := httptest.NewRecorder()
	h.ServeHTTP(recLive, reqLive)
	if recLive.Code != 200 || !strings.Contains(recLive.Body.String(), `"status":"alive"`) {
		t.Fatalf("expected 200 live, got %d %s", recLive.Code, recLive.Body.String())
	}

	// Test catalog proxy
	reqCat := httptest.NewRequest(http.MethodGet, "/api/catalog", nil)
	recCat := httptest.NewRecorder()
	h.ServeHTTP(recCat, reqCat)
	if recCat.Code != 200 || !strings.Contains(recCat.Body.String(), "Perfume One") {
		t.Fatalf("expected 200 catalog, got %d %s", recCat.Code, recCat.Body.String())
	}

	// Test ready proxy 503 when backend unready
	readyCode = http.StatusServiceUnavailable
	reqUnready := httptest.NewRequest(http.MethodGet, "/api/health/ready", nil)
	recUnready := httptest.NewRecorder()
	h.ServeHTTP(recUnready, reqUnready)
	if recUnready.Code != 503 {
		t.Fatalf("expected 503 unready, got %d", recUnready.Code)
	}
}

func TestIndexSSR(t *testing.T) {
	store := &mockCatalog{
		products: []catalog.Product{
			{ID: "p1", Name: "SSR Perfume", PriceKes: 2500, Available: true, Images: []string{"img/test.jpeg"}},
		},
	}
	h := handler("", store)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "SSR Perfume") {
		t.Fatalf("expected SSR page to contain product name, got %s", rec.Body.String())
	}
}
