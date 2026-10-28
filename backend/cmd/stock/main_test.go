package main

import (
	"strings"
	"testing"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/catalog"
)

func TestStrictImportInput(t *testing.T) {
	for _, raw := range []string{
		`{"lines":[{"productId":"p1","quantity":null}]}`,
		`{"lines":[{"productId":"p1"}]}`,
		`{"unexpected":true}`,
		`{} {}`,
		`{}` + strings.Repeat(" ", 1<<20),
	} {
		var b catalog.Batch
		if decode(strings.NewReader(raw), &b) == nil {
			t.Fatal("malformed input accepted")
		}
	}
	var b catalog.Batch
	if err := decode(strings.NewReader(`{"id":"count","kind":"opening","actor":"owner-id","evidence":"signoff-reference","lines":[{"productId":"p1","quantity":0}]}`), &b); err != nil {
		t.Fatal(err)
	}
	if b.Validate() != nil {
		t.Fatal("explicit zero opening rejected")
	}
}
