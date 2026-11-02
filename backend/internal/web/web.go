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

	render := func(w http.ResponseWriter, r *http.Request, title string, page templ.Component) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := Layout(title, page).Render(r.Context(), w); err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
		}
	}

	// Serve static assets
	staticSub, err := fs.Sub(staticFS, "static")
	if err == nil {
		fileServer := http.FileServer(http.FS(staticSub))
		staticPrefixHandler := http.StripPrefix("/static/", fileServer)

		mux.Handle("GET /static/", staticPrefixHandler)
		mux.Handle("HEAD /static/", staticPrefixHandler)

		for _, name := range []string{"checkout.js", "shared.js", "style.css", "tailwind.css", "app.js", "cart.js"} {
			mux.Handle("GET /"+name, fileServer)
			mux.Handle("HEAD /"+name, fileServer)
		}
	}

	// Pages (both canonical paths and legacy .html aliases)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		render(w, r, "Collection", IndexPage())
	})
	mux.HandleFunc("HEAD /{$}", func(w http.ResponseWriter, r *http.Request) {
		render(w, r, "Collection", IndexPage())
	})
	mux.HandleFunc("GET /index.html", func(w http.ResponseWriter, r *http.Request) {
		render(w, r, "Collection", IndexPage())
	})
	mux.HandleFunc("HEAD /index.html", func(w http.ResponseWriter, r *http.Request) {
		render(w, r, "Collection", IndexPage())
	})

	mux.HandleFunc("GET /cart", func(w http.ResponseWriter, r *http.Request) {
		render(w, r, "Cart", CartPage())
	})
	mux.HandleFunc("HEAD /cart", func(w http.ResponseWriter, r *http.Request) {
		render(w, r, "Cart", CartPage())
	})
	mux.HandleFunc("GET /cart.html", func(w http.ResponseWriter, r *http.Request) {
		render(w, r, "Cart", CartPage())
	})
	mux.HandleFunc("HEAD /cart.html", func(w http.ResponseWriter, r *http.Request) {
		render(w, r, "Cart", CartPage())
	})

	mux.HandleFunc("GET /checkout", func(w http.ResponseWriter, r *http.Request) {
		render(w, r, "Checkout", CheckoutPage())
	})
	mux.HandleFunc("HEAD /checkout", func(w http.ResponseWriter, r *http.Request) {
		render(w, r, "Checkout", CheckoutPage())
	})
	mux.HandleFunc("GET /checkout.html", func(w http.ResponseWriter, r *http.Request) {
		render(w, r, "Checkout", CheckoutPage())
	})
	mux.HandleFunc("HEAD /checkout.html", func(w http.ResponseWriter, r *http.Request) {
		render(w, r, "Checkout", CheckoutPage())
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

// Ensure templ is used
var _ templ.Component
