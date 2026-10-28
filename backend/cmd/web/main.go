package main

import (
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/localhttp"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

func handler() http.Handler {
	target, _ := url.Parse("http://api:5001")
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &http.Transport{Proxy: nil, ResponseHeaderTimeout: 15 * time.Second, MaxConnsPerHost: 16, IdleConnTimeout: 30 * time.Second}
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
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.Handle("/api/", http.StripPrefix("/api", proxy))
	mux.Handle("/", http.FileServer(http.Dir("/site")))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != "GET" && r.Method != "HEAD" && len(r.URL.Path) >= 5 && r.URL.Path[:5] != "/api/" {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		mux.ServeHTTP(w, r)
	})
}
func main() { localhttp.Serve(":8080", handler()) }
