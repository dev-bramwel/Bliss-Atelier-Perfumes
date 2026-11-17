package telemetry

import (
	"net/http"
)

// Handler returns an http.Handler that serves Prometheus metrics in text format.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(DefaultRegistry.Gather()))
	})
}
