package orders

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestValidateCustomer(t *testing.T) {
	tests := []struct {
		name    string
		input   CustomerInput
		wantErr error
	}{
		{
			name: "valid customer",
			input: CustomerInput{
				Name:     "Jane Doe",
				Phone:    "254712345678",
				Location: "Mombasa, Old Town",
				Delivery: "delivery",
				Payment:  "pay_now",
			},
			wantErr: nil,
		},
		{
			name: "valid with defaults",
			input: CustomerInput{
				Name:     "Jane Doe",
				Phone:    "+254712345678",
				Location: "Nyali",
			},
			wantErr: nil,
		},
		{
			name: "missing name",
			input: CustomerInput{
				Phone:    "254712345678",
				Location: "Nyali",
			},
			wantErr: ErrInvalidCustomer,
		},
		{
			name: "invalid phone",
			input: CustomerInput{
				Name:     "Jane Doe",
				Phone:    "invalid",
				Location: "Nyali",
			},
			wantErr: ErrInvalidCustomer,
		},
		{
			name: "missing location",
			input: CustomerInput{
				Name:  "Jane Doe",
				Phone: "254712345678",
			},
			wantErr: ErrInvalidCustomer,
		},
		{
			name: "invalid delivery option",
			input: CustomerInput{
				Name:     "Jane Doe",
				Phone:    "254712345678",
				Location: "Nyali",
				Delivery: "drone",
			},
			wantErr: ErrInvalidDeliveryOption,
		},
		{
			name: "prohibited pay on delivery",
			input: CustomerInput{
				Name:     "Jane Doe",
				Phone:    "254712345678",
				Location: "Nyali",
				Payment:  "on_delivery",
			},
			wantErr: ErrPayOnDeliveryProhib,
		},
		{
			name: "unsupported payment method",
			input: CustomerInput{
				Name:     "Jane Doe",
				Phone:    "254712345678",
				Location: "Nyali",
				Payment:  "crypto",
			},
			wantErr: ErrInvalidPaymentMethod,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := tt.input
			err := ValidateCustomer(&input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateCustomer() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil {
				if input.Delivery == "" || input.Payment == "" {
					t.Fatalf("expected defaults to be filled: %+v", input)
				}
			}
		})
	}
}

func TestValidateItems(t *testing.T) {
	tests := []struct {
		name    string
		items   []ItemInput
		wantErr error
	}{
		{
			name:    "empty items",
			items:   nil,
			wantErr: ErrEmptyOrder,
		},
		{
			name: "valid single item",
			items: []ItemInput{
				{ID: "p1", Qty: 2},
			},
			wantErr: nil,
		},
		{
			name: "missing product ID",
			items: []ItemInput{
				{ID: "   ", Qty: 1},
			},
			wantErr: ErrMissingProductID,
		},
		{
			name: "duplicate product ID",
			items: []ItemInput{
				{ID: "p1", Qty: 1},
				{ID: "p1", Qty: 2},
			},
			wantErr: ErrDuplicateProduct,
		},
		{
			name: "zero quantity",
			items: []ItemInput{
				{ID: "p1", Qty: 0},
			},
			wantErr: ErrInvalidQuantity,
		},
		{
			name: "negative quantity",
			items: []ItemInput{
				{ID: "p1", Qty: -5},
			},
			wantErr: ErrInvalidQuantity,
		},
		{
			name: "excessive quantity",
			items: []ItemInput{
				{ID: "p1", Qty: 101},
			},
			wantErr: ErrInvalidQuantity,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateItems(tt.items)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateItems() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateCustomerNotesUTF8Truncation(t *testing.T) {
	// Multibyte characters (e.g. Swahili greeting with accents / 3-byte characters)
	longNotes := strings.Repeat("Habari ya asubuhi! Karibu sana ✨. ", 50) // ~1700 bytes
	input := CustomerInput{
		Name:     "Fatuma Hassan",
		Phone:    "254712345678",
		Location: "Mombasa Old Town",
		Notes:    longNotes,
	}
	if err := ValidateCustomer(&input); err != nil {
		t.Fatalf("expected valid customer, got err: %v", err)
	}
	if !utf8.ValidString(input.Notes) {
		t.Fatalf("truncated notes produced invalid UTF-8 string: %q", input.Notes)
	}
	if runes := []rune(input.Notes); len(runes) > 500 {
		t.Fatalf("expected <= 500 runes, got %d", len(runes))
	}
}
