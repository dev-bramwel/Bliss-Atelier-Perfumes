// Package web renders Templ pages and serves static assets.
package web

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/a-h/templ"
)

//go:embed static/*
var staticFS embed.FS

// Handler returns an HTTP handler that serves Templ-rendered pages and static assets.
func Handler() http.Handler {
	mux := http.NewServeMux()

	// Serve static assets — must use method-specific pattern to avoid conflict
	staticSub, err := fs.Sub(staticFS, "static")
	if err == nil {
		staticHandler := http.StripPrefix("/static/", http.FileServer(http.FS(staticSub)))
		mux.HandleFunc("GET /static/", func(w http.ResponseWriter, r *http.Request) {
			staticHandler.ServeHTTP(w, r)
		})
		mux.HandleFunc("HEAD /static/", func(w http.ResponseWriter, r *http.Request) {
			staticHandler.ServeHTTP(w, r)
		})
	}

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = Layout("Collection", IndexPage()).Render(r.Context(), w)
	})

	mux.HandleFunc("GET /cart", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = Layout("Cart", CartPage()).Render(r.Context(), w)
	})

	mux.HandleFunc("GET /checkout", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = Layout("Checkout", CheckoutPage()).Render(r.Context(), w)
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

// Ensure templ is used
var _ templ.Component
