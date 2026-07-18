package travel

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/go-pdf/fpdf"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/travu"
)

// RenderTicketPDF builds a printable PDF ticket from a confirmed Travu booking
// receipt. mode is "bus" or "flight".
func RenderTicketPDF(mode string, r *travu.OrderReceipt) ([]byte, error) {
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
	route := strings.TrimSpace(r.Narration)
	if route == "" {
		route = fmt.Sprintf("%s to %s", r.DepartureTerminal, r.DestinationTerminal)
	}
	pdf.MultiCell(0, 8, route, "", "L", false)
	pdf.Ln(2)

	// Status pill.
	pdf.SetFont("Helvetica", "B", 10)
	pdf.SetTextColor(5, 150, 105)
	pdf.CellFormat(0, 6, "STATUS: "+strings.ToUpper(r.OrderStatus), "", 1, "L", false, 0, "")
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

	row("Booking Ref", r.OrderNumber.String())
	row("Order ID", r.OrderID.String())
	if mode == ModeFlight {
		row("PNR", r.PNRNumber.String())
		row("Booking ID", r.BookingID.String())
	}
	row("Operator", r.Provider)
	row("Travel Date", r.OrderTicketDate)
	row("Departure", r.DepartureTerminal)
	row("Destination", r.DestinationTerminal)
	if mode == ModeBus {
		row("Vehicle", r.VehicleNo)
		row("Seats", r.OrderSeats.String())
	}
	row("Total Fare", "NGN "+r.OrderAmount.String())

	pdf.Ln(4)

	// Passenger table.
	if len(r.SeatDetails) > 0 {
		pdf.SetFont("Helvetica", "B", 12)
		pdf.CellFormat(0, 8, "Passengers", "", 1, "L", false, 0, "")
		pdf.SetFont("Helvetica", "B", 9)
		pdf.SetFillColor(243, 244, 246)
		pdf.CellFormat(70, 7, "Name", "1", 0, "L", true, 0, "")
		pdf.CellFormat(25, 7, "Seat", "1", 0, "C", true, 0, "")
		pdf.CellFormat(20, 7, "Sex", "1", 0, "C", true, 0, "")
		pdf.CellFormat(75, 7, "Fare (NGN)", "1", 1, "R", true, 0, "")
		pdf.SetFont("Helvetica", "", 9)
		for _, sd := range r.SeatDetails {
			pdf.CellFormat(70, 7, truncateCell(sd.Name, 40), "1", 0, "L", false, 0, "")
			pdf.CellFormat(25, 7, sd.SeatNumber.String(), "1", 0, "C", false, 0, "")
			pdf.CellFormat(20, 7, sd.Sex, "1", 0, "C", false, 0, "")
			pdf.CellFormat(75, 7, sd.Fare.String(), "1", 1, "R", false, 0, "")
		}
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

// ticketCaption builds the short message that accompanies the PDF attachment.
func ticketCaption(mode string, r *travu.OrderReceipt) string {
	kind := "bus"
	if mode == ModeFlight {
		kind = "flight"
	}
	route := strings.TrimSpace(r.Narration)
	if route == "" {
		route = fmt.Sprintf("%s to %s", r.DepartureTerminal, r.DestinationTerminal)
	}
	extra := ""
	if mode == ModeFlight && r.PNRNumber.String() != "" {
		extra = fmt.Sprintf(" PNR %s.", r.PNRNumber.String())
	} else if mode == ModeBus && r.OrderSeats.String() != "" {
		extra = fmt.Sprintf(" Seat %s.", r.OrderSeats.String())
	}
	return fmt.Sprintf("Your %s ticket is confirmed: %s on %s. Ref %s.%s Here's your ticket.",
		kind, route, r.OrderTicketDate, r.OrderNumber.String(), extra)
}

func truncateCell(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}
