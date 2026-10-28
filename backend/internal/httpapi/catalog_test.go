package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/catalog"
)

type catalogFixture struct{ fail bool }

func (f catalogFixture) Products(ctx context.Context) ([]catalog.Product, error) {
	if f.fail {
		return nil, errors.New("private database connection data")
	}
	return []catalog.Product{{ID: "p1", PriceKes: 2500, Images: []string{"img/arabian%20wood.jpeg"}}}, nil
}
func TestCatalogHTTP(t *testing.T) {
	for _, fail := range []bool{false, true} {
		a := New(":0", time.Second, nil, catalogFixture{fail: fail})
		w := httptest.NewRecorder()
		a.server.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/catalog", nil))
		want := 200
		if fail {
			want = 503
		}
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "private") {
			t.Fatal(w.Code, w.Body.String())
		}
		if !fail && !strings.Contains(w.Body.String(), `"priceKes":2500`) {
			t.Fatal(w.Body.String())
		}
		w = httptest.NewRecorder()
		a.server.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/catalog", strings.NewReader(`{}`)))
		if w.Code != 405 {
			t.Fatal("public catalog write accepted", w.Code)
		}
	}
	a := New(":0", time.Second, nil)
	w := httptest.NewRecorder()
	a.server.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/catalog", nil))
	if w.Code != 503 {
		t.Fatal("catalog without DB did not fail closed")
	}
}
