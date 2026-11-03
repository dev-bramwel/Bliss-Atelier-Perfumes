package catalog

import (
	"encoding/json"
	"testing"
)

func TestRejectMalformedBatches(t *testing.T) {
	for _, b := range []Batch{
		{},
		{ID: "b", Kind: "opening", Actor: "owner", Evidence: "count", Lines: []Line{{ProductID: "p1", Quantity: -1}}},
		{ID: "b", Kind: "opening", Actor: "owner", Evidence: "count", Lines: []Line{{ProductID: "p1"}, {ProductID: "p1"}}},
		{ID: "b", Kind: "local_sale", Actor: "operator", Evidence: "verified", Lines: []Line{{ProductID: "p1", Quantity: 0}}},
		{ID: "b", Kind: "adjustment", Actor: "owner", Evidence: "correction", Lines: []Line{{ProductID: "p1", Quantity: 1}}},
	} {
		if b.Validate() == nil {
			t.Fatalf("accepted malformed batch: %+v", b)
		}
	}
}

func TestImportRejectsAbsentOrInvalidQuantity(t *testing.T) {
	for _, raw := range []string{`{"productId":"p1"}`, `{"productId":"p1","quantity":null}`, `{"productId":"p1","quantity":1.5}`, `{"productId":"p1","quantity":1,"surprise":true}`} {
		var l Line
		if json.Unmarshal([]byte(raw), &l) == nil {
			t.Fatal("invalid line accepted", raw)
		}
	}
	var l Line
	if err := json.Unmarshal([]byte(`{"productId":"p1","quantity":0}`), &l); err != nil {
		t.Fatal("explicit zero count rejected", err)
	}
}
