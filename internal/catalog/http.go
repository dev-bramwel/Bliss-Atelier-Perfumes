package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// HTTPStore fetches products from an upstream HTTP catalog endpoint.
type HTTPStore struct {
	Client  *http.Client
	BaseURL string
}

// NewHTTPStore creates an HTTPStore targeting the given base URL with a default timeout.
func NewHTTPStore(baseURL string, timeout time.Duration) *HTTPStore {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &HTTPStore{
		Client: &http.Client{
			Timeout: timeout,
		},
		BaseURL: strings.TrimRight(baseURL, "/"),
	}
}

// Products implements httpapi.Catalog by querying GET /catalog on the upstream API.
func (h *HTTPStore) Products(ctx context.Context) ([]Product, error) {
	if h.BaseURL == "" {
		return nil, errors.New("upstream API URL not configured")
	}
	targetURL := h.BaseURL + "/catalog"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create catalog request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("catalog request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalog request returned status %d", resp.StatusCode)
	}

	var payload struct {
		Products []Product `json:"products"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode catalog response: %w", err)
	}

	return payload.Products, nil
}
