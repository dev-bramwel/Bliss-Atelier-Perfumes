package orders

import (
	"regexp"
	"strings"
)

var phoneRegex = regexp.MustCompile(`^\+?[0-9]{9,15}$`)

// ValidateCustomer validates customer input against required fields and approved business policies.
func ValidateCustomer(c *CustomerInput) error {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" || len(c.Name) > 100 {
		return ErrInvalidCustomer
	}

	c.Phone = strings.TrimSpace(c.Phone)
	cleanedPhone := strings.ReplaceAll(strings.ReplaceAll(c.Phone, "-", ""), " ", "")
	if !phoneRegex.MatchString(cleanedPhone) {
		return ErrInvalidCustomer
	}
	c.Phone = cleanedPhone

	c.Location = strings.TrimSpace(c.Location)
	if c.Location == "" || len(c.Location) > 200 {
		return ErrInvalidCustomer
	}

	c.Delivery = strings.ToLower(strings.TrimSpace(c.Delivery))
	if c.Delivery == "" {
		c.Delivery = "delivery"
	}
	if c.Delivery != "delivery" && c.Delivery != "pickup" {
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
	if len(c.Notes) > 500 {
		c.Notes = c.Notes[:500]
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

	seen := make(map[string]struct{}, len(items))
	for _, it := range items {
		id := strings.TrimSpace(it.ID)
		if id == "" {
			return ErrMissingProductID
		}
		if _, exists := seen[id]; exists {
			return ErrDuplicateProduct
		}
		seen[id] = struct{}{}

		if it.Qty < 1 || it.Qty > 100 {
			return ErrInvalidQuantity
		}
	}
	return nil
}
