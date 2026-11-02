package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/catalog"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/config"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/database"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/httpapi"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/web"
)

func handler(apiTarget string, catalogStore httpapi.Catalog) http.Handler {
	if apiTarget == "" {
		apiTarget = "http://api:5001"
	}
	target, _ := url.Parse(apiTarget)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &http.Transport{
		Proxy:                 nil,
		ResponseHeaderTimeout: 15 * time.Second,
		MaxConnsPerHost:       16,
		IdleConnTimeout:       30 * time.Second,
	}
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		r.Header.Del("Forwarded")
		r.Header.Del("X-Forwarded-For")
		r.Header.Del("X-Forwarded-Host")
		r.Header.Del("X-Forwarded-Proto")
		director(r)
		r.Host = target.Host
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "API unavailable", http.StatusBadGateway)
	}

	mux := http.NewServeMux()

	// Direct web process liveness
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"alive"}`))
	})

	// Reverse proxy all /api/ traffic to upstream api:5001
	// Handles /api/health/live, /api/health/ready, /api/catalog, /api/orders, etc.
	mux.Handle("/api/", http.StripPrefix("/api", proxy))

	// Frontend pages (Templ SSR) and static assets
	mux.Handle("/", web.Handler(catalogStore))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		mux.ServeHTTP(w, r)
	})
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		os.Stderr.WriteString("config error: " + err.Error() + "\n")
		os.Exit(1)
	}

	var catalogStore httpapi.Catalog
	if cfg.DatabaseURL != "" {
		pool, err := database.Open(ctx, cfg.DatabaseURL, cfg.MaxConnections, cfg.IOTimeout)
		if err != nil {
			os.Stderr.WriteString("database error: " + err.Error() + "\n")
			os.Exit(1)
		}
		defer pool.Close()
		catalogStore = &catalog.Store{Pool: pool}
	}

	listener, err := net.Listen("tcp", cfg.Address)
	if err != nil {
		os.Stderr.WriteString("listener error: " + err.Error() + "\n")
		os.Exit(1)
	}
	defer listener.Close()

	apiTarget := os.Getenv("BLISS_API_URL")
	server := &http.Server{
		Handler:           handler(apiTarget, catalogStore),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		os.Stderr.WriteString("server error: " + err.Error() + "\n")
		os.Exit(1)
	}
}
