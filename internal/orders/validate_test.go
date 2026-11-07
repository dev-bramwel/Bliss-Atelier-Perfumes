package orders

import (
	"errors"
	"testing"
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
