package orders

import "encoding/json"

// UnmarshalJSON accepts the shopper wire contract and the established nested API.
// UserID is intentionally never decoded from browser input.
func (r *CreateOrderRequest) UnmarshalJSON(data []byte) error {
	var wire struct {
		Customer         *CustomerInput `json:"customer"`
		CustomerName     string         `json:"customerName"`
		CustomerPhone    string         `json:"customerPhone"`
		DeliveryOption   string         `json:"deliveryOption"`
		DeliveryLocation string         `json:"deliveryLocation"`
		PaymentMethod    string         `json:"paymentMethod"`
		Notes            string         `json:"notes"`
		Items            []struct {
			ID       string `json:"id"`
			Qty      *int   `json:"qty"`
			Quantity *int   `json:"quantity"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*r = CreateOrderRequest{}
	if wire.Customer != nil {
		r.Customer = *wire.Customer
	} else {
		r.Customer = CustomerInput{Name: wire.CustomerName, Phone: wire.CustomerPhone, Delivery: wire.DeliveryOption, Location: wire.DeliveryLocation, Payment: wire.PaymentMethod, Notes: wire.Notes}
	}
	for _, item := range wire.Items {
		qty := 0
		if item.Qty != nil {
			qty = *item.Qty
		} else if item.Quantity != nil {
			qty = *item.Quantity
		}
		if item.Qty != nil && item.Quantity != nil && *item.Qty != *item.Quantity {
			return ErrInvalidQuantity
		}
		r.Items = append(r.Items, ItemInput{ID: item.ID, Qty: qty})
	}
	return nil
}
