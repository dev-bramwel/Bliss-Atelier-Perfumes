package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFakeProvider(t *testing.T) {
	for _, scenario := range []string{"accepted", "rejected", "malformed", "oversized", "timeout", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch scenario {
				case "rejected":
					http.Error(w, "SENTINEL_SECRET", 502)
				case "malformed":
					_, _ = w.Write([]byte("SENTINEL_SECRET"))
				case "oversized":
					_, _ = w.Write([]byte(strings.Repeat("x", 65537)))
				case "timeout":
					<-r.Context().Done()
				case "redirect":
					http.Redirect(w, r, "/other", 302)
				default:
					_, _ = w.Write([]byte(`{"accepted":true}`))
				}
			}))
			defer server.Close()
			client, _ := New(100 * time.Millisecond)
			defer client.Close()
			request, _ := http.NewRequest("POST", server.URL+"?token=SENTINEL_SECRET", nil)
			var result struct {
				Accepted bool `json:"accepted"`
			}
			err := client.DoJSON(context.Background(), request, &result)
			if scenario == "accepted" {
				if err != nil || !result.Accepted {
					t.Fatalf("%+v %v", result, err)
				}
			} else if err == nil || strings.Contains(err.Error(), "SENTINEL_SECRET") {
				t.Fatalf("unsafe result: %v", err)
			}
		})
	}
}

func TestCancellation(t *testing.T) {
	client, _ := New(time.Second)
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request, _ := http.NewRequest("POST", "http://127.0.0.1:1", nil)
	if client.DoJSON(ctx, request, &struct{}{}) == nil {
		t.Fatal("cancellation ignored")
	}
}
