package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/catalog"
)

type mockCatalog struct {
	products []catalog.Product
}

func (m *mockCatalog) Products(ctx context.Context) ([]catalog.Product, error) {
	return m.products, nil
}

func TestWebHandler_ScriptOrder(t *testing.T) {
	mockProducts := []catalog.Product{
		{
			ID:          "p1",
			Name:        "Arabian Wood",
			Category:    "Woody",
			Gender:      "Unisex",
			Size:        "100ml",
			Notes:       "Sandalwood, Cedar",
			Description: "Rich woody aroma",
			PriceKes:    2500,
			Available:   true,
			Images:      []string{"img/arabian%20wood.jpeg"},
		},
	}
	h := Handler(&mockCatalog{products: mockProducts})

	tests := []struct {
		name       string
		path       string
		pageScript string
		hasCatalog bool
	}{
		{"index collection", "/", "/static/app.js", true},
		{"cart page", "/cart", "/static/cart.js", true},
		{"checkout page", "/checkout", "/static/checkout.js", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rec.Code)
			}
			body := rec.Body.String()

			sharedIdx := strings.Index(body, `<script src="/static/shared.js"></script>`)
			if sharedIdx == -1 {
				t.Fatalf("expected shared.js in response body")
			}

			pageScriptIdx := strings.Index(body, `<script src="`+tc.pageScript+`"></script>`)
			if pageScriptIdx == -1 {
				t.Fatalf("expected page script %s in response body", tc.pageScript)
			}

			if sharedIdx >= pageScriptIdx {
				t.Fatalf("script ordering violation: shared.js (idx %d) must appear before %s (idx %d)",
					sharedIdx, tc.pageScript, pageScriptIdx)
			}

			if tc.hasCatalog {
				catalogIdx := strings.Index(body, `<script id="catalogData" type="application/json">`)
				if catalogIdx == -1 {
					t.Fatalf("expected catalogData in response body")
				}
				if catalogIdx >= sharedIdx {
					t.Fatalf("script ordering violation: catalogData (idx %d) must appear before shared.js (idx %d)",
						catalogIdx, sharedIdx)
				}
			}
		})
	}
}
