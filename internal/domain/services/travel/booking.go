package travel

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/travu"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// BookBusRequest is a resolved, ready-to-charge bus booking. The caller (AI tool
// or REST handler) resolves the trip fields from a prior search before invoking.
type BookBusRequest struct {
	Provider            string
	TripID              string
	OrderID             string // Travu order_id from the trip
	OriginID            string
	DestinationID       string
	BoardingAt          string
	TripDate            string
	SeatNumbers         string // comma-separated, e.g. "1,2"
	AmountPerSeat       float64
	Route               string
	DepartureTerminal   string
	DestinationTerminal string
	Passengers          []travu.Passenger
}

// BookBus holds funds, books the bus with Travu, and delivers the ticket. Total
// fare is amount_per_seat * number of seats.
func (s *Service) BookBus(ctx context.Context, userID uuid.UUID, req BookBusRequest) (*BookingResult, error) {
	seatCount := countSeats(req.SeatNumbers)
	if seatCount == 0 {
		return nil, fmt.Errorf("select at least one seat")
	}
	if len(req.Passengers) != seatCount {
		return nil, fmt.Errorf("passenger count (%d) must match seat count (%d)", len(req.Passengers), seatCount)
	}
	totalNGN := req.AmountPerSeat * float64(seatCount)
	if err := s.validateAmount(totalNGN); err != nil {
		return nil, err
	}

	amountUSDC, railFee, totalHold, rate, err := s.quote(ctx, totalNGN)
	if err != nil {
		return nil, err
	}
	if err := s.holdFunds(ctx, userID, ModeBus, totalNGN, totalHold, railFee); err != nil {
		return nil, err
	}

	orderID, err := s.persistOrder(ctx, &orderRow{
		UserID: userID, Mode: ModeBus, Provider: req.Provider, Route: req.Route,
		DepartureTerminal: req.DepartureTerminal, DestinationTerminal: req.DestinationTerminal,
		TripDate: req.TripDate, Seats: req.SeatNumbers, Passengers: req.Passengers,
		AmountNGN: totalNGN, AmountUSDC: amountUSDC, RailFee: railFee, HoldAmount: totalHold, Rate: rate,
	})
	if err != nil {
		s.logger.Error("failed to persist travel order — reversing hold", zap.Error(err))
		_ = s.reverseHoldByLedgerOnly(ctx, userID, totalHold, railFee, "db_fail")
		return nil, fmt.Errorf("failed to record booking")
	}

	receipt, err := s.client.BookTrip(ctx, travu.BookTripRequest{
		SeatNumbers:   req.SeatNumbers,
		AmountPerSeat: fmt.Sprintf("%.2f", req.AmountPerSeat),
		AgentEmail:    s.client.AgentEmail(),
		Passengers:    req.Passengers,
		OriginID:      req.OriginID,
		DestinationID: req.DestinationID,
		BoardingAt:    req.BoardingAt,
		TripID:        req.TripID,
		TripDate:      req.TripDate,
		OrderID:       req.OrderID,
		Provider:      req.Provider,
	})
	if err != nil {
		s.logger.Error("bus booking failed — reversing hold", zap.Error(err), zap.String("order_id", orderID.String()))
		_ = s.reverseHold(ctx, userID, orderID, totalHold, railFee, "book_failed")
		return nil, fmt.Errorf("the operator could not confirm this booking: %w", err)
	}

	res := s.finalizeBooking(ctx, userID, orderID, ModeBus, receipt)
	res.AmountUSDC = amountUSDC.StringFixed(2)
	return &res, nil
}

// BookFlightRequest is a resolved, ready-to-charge flight booking. Passengers
// must carry passport-level detail (resolved from the stored travel profile
// plus in-chat top-ups). ItineraryID is the id returned from flight-select.
type BookFlightRequest struct {
	ItineraryID   string
	Currency      string
	AmountNGN     float64
	Route         string
	TripDate      string
	Passengers    []travu.FlightPassenger
}

// BookFlight holds funds, runs the tentative booking then the final ticketing
// with Travu, and delivers the ticket.
func (s *Service) BookFlight(ctx context.Context, userID uuid.UUID, req BookFlightRequest) (*BookingResult, error) {
	if len(req.Passengers) == 0 {
		return nil, fmt.Errorf("at least one passenger is required")
	}
	if strings.TrimSpace(req.ItineraryID) == "" {
		return nil, fmt.Errorf("itinerary id is required — select a flight first")
	}
	if err := s.validateAmount(req.AmountNGN); err != nil {
		return nil, err
	}
	currency := strings.ToUpper(strings.TrimSpace(req.Currency))
	if currency == "" {
		currency = "NGN"
	}

	amountUSDC, railFee, totalHold, rate, err := s.quote(ctx, req.AmountNGN)
	if err != nil {
		return nil, err
	}
	if err := s.holdFunds(ctx, userID, ModeFlight, req.AmountNGN, totalHold, railFee); err != nil {
		return nil, err
	}

	orderID, err := s.persistOrder(ctx, &orderRow{
		UserID: userID, Mode: ModeFlight, Route: req.Route, TripDate: req.TripDate,
		Passengers: req.Passengers, AmountNGN: req.AmountNGN, AmountUSDC: amountUSDC,
		RailFee: railFee, HoldAmount: totalHold, Rate: rate,
	})
	if err != nil {
		s.logger.Error("failed to persist flight order — reversing hold", zap.Error(err))
		_ = s.reverseHoldByLedgerOnly(ctx, userID, totalHold, railFee, "db_fail")
		return nil, fmt.Errorf("failed to record booking")
	}

	// Stamp the itinerary id + currency on each passenger for the tentative call.
	passengers := make([]travu.FlightPassenger, len(req.Passengers))
	copy(passengers, req.Passengers)
	for i := range passengers {
		passengers[i].ID = req.ItineraryID
		passengers[i].Currency = currency
	}

	tentative, err := s.client.TentativeFlightBooking(ctx, passengers)
	if err != nil {
		s.logger.Error("tentative flight booking failed — reversing hold", zap.Error(err), zap.String("order_id", orderID.String()))
		_ = s.reverseHold(ctx, userID, orderID, totalHold, railFee, "tentative_failed")
		return nil, fmt.Errorf("the airline could not hold this booking: %w", err)
	}

	bookingID := tentative.BookingID.String()
	pnr := tentative.PNRNumber.String()
	if bookingID == "" || pnr == "" {
		s.logger.Error("tentative booking missing booking_id/pnr — reversing hold", zap.String("order_id", orderID.String()))
		_ = s.reverseHold(ctx, userID, orderID, totalHold, railFee, "tentative_incomplete")
		return nil, fmt.Errorf("the airline returned an incomplete booking")
	}

	// Record the tentative state before ticketing so recovery can resume.
	if _, dbErr := s.db.ExecContext(ctx,
		`UPDATE travel_orders SET status='booked', booking_id=$2, pnr=$3, updated_at=NOW() WHERE id=$1 AND status='held'`,
		orderID, bookingID, pnr); dbErr != nil {
		s.logger.Warn("failed to record tentative booking state", zap.Error(dbErr), zap.String("order_id", orderID.String()))
	}

	ticket, err := s.client.TicketFlight(ctx, travu.TicketFlightRequest{BookingID: bookingID, PNRNumber: pnr})
	if err != nil {
		// The tentative hold exists but ticketing failed. Funds are held; do NOT
		// reverse automatically — leave for recovery to retry or reconcile.
		s.logger.Error("flight ticketing failed after tentative booking — needs recovery",
			zap.Error(err), zap.String("order_id", orderID.String()), zap.String("booking_id", bookingID))
		return nil, fmt.Errorf("your booking is held but ticketing didn't complete — I'll keep retrying")
	}

	res := s.finalizeBooking(ctx, userID, orderID, ModeFlight, ticket)
	res.AmountUSDC = amountUSDC.StringFixed(2)
	return &res, nil
}

// SelectFlight retrieves the freshest priced itinerary before booking so the
// caller can confirm the up-to-date fare.
func (s *Service) SelectFlight(ctx context.Context, itineraryID, currency string) (*travu.OrderReceipt, error) {
	if !s.cfg.FlightSearchEnabled {
		return nil, fmt.Errorf("flight booking is coming soon — bus booking is available now")
	}
	if currency == "" {
		currency = "NGN"
	}
	return s.client.SelectFlight(ctx, travu.SelectFlightRequest{ID: itineraryID, Currency: currency})
}

// reverseHoldByLedgerOnly reverses a ledger hold when no order row survived to
// claim (e.g. the INSERT itself failed).
func (s *Service) reverseHoldByLedgerOnly(ctx context.Context, userID uuid.UUID, amount, railFee decimal.Decimal, reason string) error {
	if s.ledger == nil {
		return nil
	}
	return s.ledger.ReverseTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
		uuid.New().String(), amount, map[string]interface{}{
			"provider": "travu", "type": "travel_" + reason + "_reversal", "rail_fee": railFee.String(),
		})
}

// countSeats counts comma-separated seat numbers.
func countSeats(seats string) int {
	n := 0
	for _, s := range strings.Split(seats, ",") {
		if strings.TrimSpace(s) != "" {
			n++
		}
	}
	return n
}
