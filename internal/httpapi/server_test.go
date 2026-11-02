package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestHealth(t *testing.T) {
	var failing atomic.Bool
	a := New("127.0.0.1:0", 50*time.Millisecond, func(ctx context.Context) error {
		if failing.Load() {
			return errors.New("private DB details")
		}
		return nil
	})
	for _, tc := range []struct {
		path        string
		fail, drain bool
		status      int
	}{{"/", false, false, 200}, {"/health/live", true, false, 200}, {"/health/ready", false, false, 200}, {"/health/ready", true, false, 503}, {"/health/ready", false, true, 503}, {"/api/orders", false, false, 404}} {
		failing.Store(tc.fail)
		a.draining.Store(tc.drain)
		w := httptest.NewRecorder()
		a.server.Handler.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.status {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
	}
}

func TestReadinessDeadline(t *testing.T) {
	a := New("127.0.0.1:0", 10*time.Millisecond, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	w := httptest.NewRecorder()
	a.server.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/health/ready", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}

func TestShutdown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := New(listener.Addr().String(), time.Second, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx, listener, time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil))) }()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + listener.Addr().String() + "/health/ready")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown stuck")
	}
	if !a.draining.Load() {
		t.Fatal("readiness not disabled")
	}
}

func TestShutdownDeadlineClosesConnections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := New(listener.Addr().String(), time.Second, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	a.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- a.Serve(ctx, listener, 20*time.Millisecond, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	}()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		client := &http.Client{Timeout: time.Second}
		response, _ := client.Get("http://" + listener.Addr().String())
		if response != nil {
			response.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected drain deadline failure")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown exceeded bound")
	}
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("connection stayed open")
	}
}
