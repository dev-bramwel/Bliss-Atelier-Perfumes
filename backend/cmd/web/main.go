package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/catalog"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/config"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/database"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/httpapi"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/web"
)

func handler(catalogStore httpapi.Catalog) http.Handler {
	mux := http.NewServeMux()
	webHandler := web.Handler(catalogStore)

	// API routes — must be registered before catch-all
	mux.HandleFunc("GET /api/catalog", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if catalogStore == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"catalog unavailable"}`))
			return
		}
		products, err := catalogStore.Products(r.Context())
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"catalog unavailable"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"products": products})
	})
	mux.HandleFunc("POST /api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"API unavailable"}`))
	})

	// Health checks (both root and /api/ prefixes for compatibility)
	liveHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"alive"}`))
	}
	readyHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if catalogStore == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"unavailable"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	}

	mux.HandleFunc("GET /health/live", liveHandler)
	mux.HandleFunc("GET /api/health/live", liveHandler)
	mux.HandleFunc("GET /health/ready", readyHandler)
	mux.HandleFunc("GET /api/health/ready", readyHandler)

	// Frontend pages and static assets
	mux.Handle("/", webHandler)

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

	server := &http.Server{
		Handler:           handler(catalogStore),
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
