package travel

import "strings"

// TicketReceipt is the structured booking receipt persisted to
// travel_orders.receipt for audit and re-render. It is provider-agnostic and
// deliberately omits PII beyond the passenger name.
type TicketReceipt struct {
	Provider      string `json:"provider"`
	Route         string `json:"route"`
	TripDate      string `json:"trip_date"`
	OrderRef      string `json:"order_ref"`
	BookingRef    string `json:"booking_ref"`
	IntentID      string `json:"intent_id"`
	AmountUSD     string `json:"amount_usd"`
	Status        string `json:"status"`
	PassengerName string `json:"passenger_name"`
}

// buildTicketMessage renders the booking confirmation sent to the user's
// messaging thread.
func buildTicketMessage(order *orderRow, pnr string) string {
	route := strings.TrimSpace(order.Route)
	if route == "" {
		route = "your flight"
	}
	ref := strings.TrimSpace(order.OrderRef)
	var b strings.Builder
	b.WriteString("Your flight is confirmed: ")
	b.WriteString(route)
	if strings.TrimSpace(order.TripDate) != "" {
		b.WriteString(" on ")
		b.WriteString(dateOnly(order.TripDate))
	}
	b.WriteString(".")
	if pnr != "" {
		b.WriteString("\nPNR: ")
		b.WriteString(pnr)
	}
	if ref != "" {
		b.WriteString("\nRef: ")
		b.WriteString(ref)
	}
	b.WriteString("\nFor changes or cancellations, message Miriam in the RAIL app.")
	return b.String()
}
