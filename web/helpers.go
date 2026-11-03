package web

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/catalog"
)

func formatPriceKes(kes int) string {
	s := fmt.Sprintf("%d", kes)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

func uniqueGenders(products []catalog.Product) []string {
	seen := make(map[string]bool)
	var list []string
	for _, p := range products {
		g := p.Gender
		if g == "" {
			g = "Unisex"
		}
		if !seen[g] {
			seen[g] = true
			list = append(list, g)
		}
	}
	sort.Strings(list)
	return list
}

func uniqueCategories(products []catalog.Product) []string {
	seen := make(map[string]bool)
	var list []string
	for _, p := range products {
		if p.Category != "" && !seen[p.Category] {
			seen[p.Category] = true
			list = append(list, p.Category)
		}
	}
	sort.Strings(list)
	return list
}

func whatsAppOrderURL(p catalog.Product) string {
	msg := fmt.Sprintf("Hello Bliss Atelier,\n\nI would like to order:\n- Item: %s (%s)\n- Price: KES %d\n\nPlease confirm availability and payment details.", p.Name, p.Size, p.PriceKes)
	return "https://wa.me/254796382024?text=" + url.QueryEscape(msg)
}

func catalogJSON(products []catalog.Product) string {
	if len(products) == 0 {
		return "[]"
	}
	b, err := json.Marshal(products)
	if err != nil {
		return "[]"
	}
	return string(b)
}
