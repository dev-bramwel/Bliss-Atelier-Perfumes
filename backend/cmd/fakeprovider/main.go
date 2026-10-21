// This is an isolated development fixture, never a live payment provider.
package main

import (
	"encoding/json"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/localhttp"
	"net/http"
)

func handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("POST /payments", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var body struct {
			Amount int `json:"amount"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || body.Amount <= 0 {
			http.Error(w, "invalid fixture request", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Header.Get("X-Fixture-Mode") {
		case "reject":
			w.WriteHeader(422)
			_, _ = w.Write([]byte(`{"status":"rejected"}`))
		case "malformed":
			_, _ = w.Write([]byte(`{"status":`))
		default:
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "accepted", "transactionId": "synthetic-fixture-only"})
		}
	})
	return mux
}
func main() { localhttp.Serve(":8090", handler()) }
