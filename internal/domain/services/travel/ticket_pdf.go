package travel

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/go-pdf/fpdf"
)

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

// RenderTicketPDF builds a printable PDF ticket from a confirmed booking
// receipt. mode is "bus" or "flight".
func RenderTicketPDF(mode string, r *TicketReceipt) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("nil receipt")
	}
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetTitle("RAIL Travel Ticket", false)
	pdf.SetMargins(15, 18, 15)
	pdf.AddPage()

	// Header band.
	pdf.SetFillColor(17, 24, 39)
	pdf.Rect(0, 0, 210, 30, "F")
	pdf.SetY(10)
	pdf.SetTextColor(255, 255, 255)
	pdf.SetFont("Helvetica", "B", 20)
	pdf.CellFormat(0, 10, "RAIL", "", 1, "L", false, 0, "")
	pdf.SetY(10)
	pdf.SetFont("Helvetica", "", 12)
	label := "Bus Ticket"
	if mode == ModeFlight {
		label = "Flight Ticket"
	}
	pdf.CellFormat(0, 10, label, "", 1, "R", false, 0, "")

	pdf.SetTextColor(17, 24, 39)
	pdf.SetY(40)

	// Route headline.
	pdf.SetFont("Helvetica", "B", 16)
	route := strings.TrimSpace(r.Route)
	if route == "" {
		route = "Your booking"
	}
	pdf.MultiCell(0, 8, route, "", "L", false)
	pdf.Ln(2)

	// Status pill.
	pdf.SetFont("Helvetica", "B", 10)
	pdf.SetTextColor(5, 150, 105)
	pdf.CellFormat(0, 6, "STATUS: "+strings.ToUpper(r.Status), "", 1, "L", false, 0, "")
	pdf.SetTextColor(17, 24, 39)
	pdf.Ln(2)

	// Booking summary grid.
	row := func(k, v string) {
		if strings.TrimSpace(v) == "" {
			return
		}
		pdf.SetFont("Helvetica", "B", 10)
		pdf.CellFormat(55, 7, k, "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 10)
		pdf.MultiCell(0, 7, v, "", "L", false)
	}

	row("Booking Ref", r.BookingRef)
	row("Order Ref", r.OrderRef)
	row("Intent ID", r.IntentID)
	row("Operator", r.Provider)
	row("Travel Date", dateOnly(r.TripDate))
	row("Route", route)
	row("Total Fare", "USDC "+r.AmountUSD)

	pdf.Ln(4)

	// Passenger.
	if strings.TrimSpace(r.PassengerName) != "" {
		pdf.SetFont("Helvetica", "B", 12)
		pdf.CellFormat(0, 8, "Passenger", "", 1, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 10)
		pdf.CellFormat(0, 7, truncateCell(r.PassengerName, 60), "", 1, "L", false, 0, "")
	}

	// Footer note.
	pdf.SetY(-25)
	pdf.SetFont("Helvetica", "I", 8)
	pdf.SetTextColor(107, 114, 128)
	pdf.MultiCell(0, 4, "Booked via RAIL. Present this ticket and a valid ID at the terminal. Arrive at least 45 minutes before departure. For changes or cancellations, message Miriam in the RAIL app.", "", "C", false)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("render pdf: %w", err)
	}
	return buf.Bytes(), nil
}

func truncateCell(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}
