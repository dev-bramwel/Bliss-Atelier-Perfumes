package orders

import (
	"regexp"
	"strings"
)

var phoneRegex = regexp.MustCompile(`^\+?[0-9]{9,15}$`)

func containsDisallowedChars(s string, disallowNewlines bool) bool {
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b < 32 {
			if !disallowNewlines && (b == '\n' || b == '\r' || b == '\t') {
				continue
			}
			return true
		}
		if b == 127 || b == '<' || b == '>' {
			return true
		}
	}
	return false
}

// ValidatePhone validates and cleans a customer telephone number according to approved Kenyan/E.164 patterns.
func ValidatePhone(phone string) (string, error) {
	phone = strings.TrimSpace(phone)
	cleaned := strings.ReplaceAll(strings.ReplaceAll(phone, "-", ""), " ", "")
	if !phoneRegex.MatchString(cleaned) {
		return "", ErrInvalidCustomer
	}
	return cleaned, nil
}

// ValidateCustomer validates customer input against required fields and approved business policies.
func ValidateCustomer(c *CustomerInput) error {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" || len(c.Name) > 100 || containsDisallowedChars(c.Name, true) {
		return ErrInvalidCustomer
	}

	c.Phone = strings.TrimSpace(c.Phone)
	cleanedPhone := strings.ReplaceAll(strings.ReplaceAll(c.Phone, "-", ""), " ", "")
	if !phoneRegex.MatchString(cleanedPhone) {
		return ErrInvalidCustomer
	}
	c.Phone = cleanedPhone

	c.Location = strings.TrimSpace(c.Location)
	if c.Location == "" || len(c.Location) > 200 || containsDisallowedChars(c.Location, true) {
		return ErrInvalidCustomer
	}

	c.Delivery = strings.ToLower(strings.TrimSpace(c.Delivery))
	if c.Delivery == "" {
		c.Delivery = "delivery"
	}
	if c.Delivery != "delivery" && c.Delivery != "pickup" && c.Delivery != "mombasa_delivery" && c.Delivery != "showroom_pickup" {
		return ErrInvalidDeliveryOption
	}

	c.Payment = strings.ToLower(strings.TrimSpace(c.Payment))
	if c.Payment == "" {
		c.Payment = "pay_now"
	}
	if c.Payment == "on_delivery" {
		return ErrPayOnDeliveryProhib
	}
	if c.Payment != "pay_now" {
		return ErrInvalidPaymentMethod
	}

	c.Notes = strings.TrimSpace(c.Notes)
	if containsDisallowedChars(c.Notes, false) {
		return ErrInvalidCustomer
	}
	runes := []rune(c.Notes)
	if len(runes) > 500 {
		c.Notes = string(runes[:500])
	}
	return nil
}

// ValidateItems verifies order items bounds, non-zero quantities, and uniqueness.
func ValidateItems(items []ItemInput) error {
	if len(items) == 0 {
		return ErrEmptyOrder
	}
	if len(items) > 50 {
		return ErrTooManyItems
	}

	totalQty := 0
	seen := make(map[string]struct{}, len(items))
	for _, it := range items {
		id := strings.TrimSpace(it.ID)
		if id == "" || len(id) > 50 || containsDisallowedChars(id, true) {
			return ErrMissingProductID
		}
		if _, exists := seen[id]; exists {
			return ErrDuplicateProduct
		}
		seen[id] = struct{}{}

		if it.Qty < 1 || it.Qty > 100 {
			return ErrInvalidQuantity
		}
		totalQty += it.Qty
	}
	if totalQty > 500 {
		return ErrInvalidQuantity
	}
	return nil
}
