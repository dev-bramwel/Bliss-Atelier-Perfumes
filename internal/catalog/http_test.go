package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPStoreProductsSuccess(t *testing.T) {
	expected := []Product{
		{
			ID:       "p1",
			Slug:     "arabian-wood",
			Name:     "Arabian Wood",
			Category: "Eau De Parfum",
			PriceKes: 2500,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/catalog" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"products": expected})
	}))
	defer server.Close()

	store := NewHTTPStore(server.URL, 2*time.Second)
	products, err := store.Products(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(products) != 1 || products[0].ID != "p1" || products[0].Name != "Arabian Wood" {
		t.Fatalf("unexpected products returned: %+v", products)
	}
}

func TestHTTPStoreProductsErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"catalog unavailable"}`))
	}))
	defer server.Close()

	store := NewHTTPStore(server.URL, 2*time.Second)
	_, err := store.Products(context.Background())
	if err == nil {
		t.Fatal("expected error on 503 response, got nil")
	}
}

func TestHTTPStoreProductsInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{invalid-json`))
	}))
	defer server.Close()

	store := NewHTTPStore(server.URL, 2*time.Second)
	_, err := store.Products(context.Background())
	if err == nil {
		t.Fatal("expected error on invalid JSON, got nil")
	}
}

func TestHTTPStoreProductsEmptyURL(t *testing.T) {
	store := NewHTTPStore("", 2*time.Second)
	_, err := store.Products(context.Background())
	if err == nil {
		t.Fatal("expected error when BaseURL is empty, got nil")
	}
}

func TestHTTPStoreProductsContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"products": []Product{}})
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	store := NewHTTPStore(server.URL, 2*time.Second)
	_, err := store.Products(ctx)
	if err == nil {
		t.Fatal("expected context timeout/cancellation error, got nil")
	}
}
