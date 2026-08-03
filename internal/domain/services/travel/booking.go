package travel

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/brij"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// BookFlightRequest is a resolved, ready-to-charge flight booking: the BRIJ
// intent id from CreateIntent plus exactly one adult passenger.
type BookFlightRequest struct {
	IntentID  string
	Passenger brij.PassengerInput
}

// BookingResult summarizes a completed (or requested) booking.
type BookingResult struct {
	OrderID    uuid.UUID `json:"order_id"`
	IntentID   string    `json:"intent_id"`
	Provider   string    `json:"provider"`
	Route      string    `json:"route"`
	TripDate   string    `json:"trip_date"`
	OrderRef   string    `json:"order_ref,omitempty"`
	PNR        string    `json:"pnr,omitempty"`
	AmountUSD  string    `json:"amount_usd"`
	Status     string    `json:"status"`
	TicketSent bool      `json:"ticket_sent"`
}

// Polling window for async ticket issuance. The booking worker usually finishes
// within a minute; anything still active after this is left for RunRecovery.
const (
	pollTimeout  = 90 * time.Second
	pollInterval = 10 * time.Second
)

// BookFlight holds the user's funds, pays the intent's escrow via x402, submits
// the passenger, and waits for the airline to issue the ticket.
func (s *Service) BookFlight(ctx context.Context, userID uuid.UUID, req BookFlightRequest) (*BookingResult, error) {
	if s.client == nil {
		return nil, fmt.Errorf("flight booking is not configured")
	}
	intentID := strings.TrimSpace(req.IntentID)
	if intentID == "" {
		return nil, fmt.Errorf("intent id is required — select a flight first")
	}
	if err := validatePassenger(&req.Passenger); err != nil {
		return nil, err
	}
	order, err := s.loadOrderByIntent(ctx, userID, intentID)
	if err != nil {
		return nil, err
	}
	if order.Status != StatusHeld {
		return nil, fmt.Errorf("this flight is no longer bookable (status %s)", order.Status)
	}
	escrow := order.ExpectedEscrow
	if err := s.validateAmount(escrow); err != nil {
		return nil, err
	}
	railFee := s.railFee(escrow)
	totalHold := escrow.Add(railFee)

	passengersJSON, err := json.Marshal([]brij.PassengerInput{req.Passenger})
	if err != nil {
		s.logger.Error("failed to marshal passenger data", zap.Error(err), zap.String("user_id", userID.String()))
		return nil, fmt.Errorf("failed to prepare the booking")
	}
	if err := s.holdFunds(ctx, userID, totalHold); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE travel_orders SET hold_amount=$2, amount_usdc=$3, rail_fee_usdc=$4, passengers=$5, updated_at=NOW()
		WHERE id=$1 AND status='held'`,
		order.ID, totalHold, escrow, railFee, passengersJSON); err != nil {
		s.logger.Error("failed to persist flight hold — reversing", zap.Error(err), zap.String("order_id", order.ID.String()))
		_ = s.reverseHold(ctx, userID, order.ID, totalHold, "db_fail")
		return nil, fmt.Errorf("failed to record the booking")
	}

	// Pay the escrow + request booking. On failure the intent usually never
	// funded, but check its live state before reversing so a settled-but-lost
	// response is not double-charged.
	resp, err := s.client.Book(ctx, brij.BookRequest{
		IntentID:   intentID,
		Passengers: []brij.PassengerInput{req.Passenger},
	})
	if err != nil {
		s.logger.Error("BRIJ book failed", zap.Error(err), zap.String("order_id", order.ID.String()), zap.String("intent_id", intentID))
		s.settleAfterFailedBook(ctx, userID, order, totalHold)
		return nil, fmt.Errorf("the airline could not confirm this booking: %w", err)
	}
	if resp.Booking.OrderID != "" {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE travel_orders SET status='booked', order_ref=$2, updated_at=NOW() WHERE id=$1 AND status='held'`,
			order.ID, resp.Booking.OrderID); err != nil {
			s.logger.Warn("failed to record BRIJ order id", zap.Error(err), zap.String("order_id", order.ID.String()))
		}
		order.OrderRef = resp.Booking.OrderID
	}

	s.logger.Info("BRIJ booking requested",
		zap.String("intent_id", intentID),
		zap.String("order_ref", resp.Booking.OrderID),
		zap.String("user_id", userID.String()))

	// Poll for the terminal state within the window.
	intent, err := s.pollIntent(ctx, intentID)
	if err == nil && intent != nil {
		status, res := s.settleIntent(ctx, userID, order, intent)
		if status != StatusBooked {
			return res, nil
		}
		res.Status = StatusBooked
		return res, nil
	}

	// Still active: recovery will finalize + deliver the ticket.
	return &BookingResult{
		OrderID:   order.ID,
		IntentID:  intentID,
		Provider:  order.Provider,
		Route:     order.Route,
		TripDate:  order.TripDate,
		OrderRef:  order.OrderRef,
		AmountUSD: escrow.StringFixed(2),
		Status:    StatusBooked,
	}, nil
}

// settleAfterFailedBook reconciles an order whose /book call errored by reading
// the intent's live state. Refunded → reverse the hold; booked → finalize
// (escrow settled even though we lost the response); otherwise leave 'held'
// with the hold so RunRecovery reverses it later.
func (s *Service) settleAfterFailedBook(ctx context.Context, userID uuid.UUID, order *orderRow, totalHold decimal.Decimal) {
	intent, err := s.client.GetIntent(ctx, order.IntentID)
	if err != nil {
		return // leave for recovery
	}
	switch intent.Status {
	case brij.StatusBooked:
		s.finalizeBooking(ctx, userID, order, intent)
	case brij.StatusRefunded:
		_ = s.reverseHold(ctx, userID, order.ID, totalHold, "book_failed_refunded")
		s.logger.Warn("BRIJ booking failed and was refunded; user hold reversed",
			zap.String("order_id", order.ID.String()), zap.String("intent_id", order.IntentID))
	}
}

// pollIntent waits for the intent to reach a terminal state (booked/refunded).
func (s *Service) pollIntent(ctx context.Context, intentID string) (*brij.BookingIntent, error) {
	deadline := time.Now().Add(pollTimeout)
	var last *brij.BookingIntent
	var lastErr error
	for {
		intent, err := s.client.GetIntent(ctx, intentID)
		if err == nil {
			last = intent
			if intent.IsTerminal() {
				return intent, nil
			}
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
	if last != nil {
		return last, lastErr
	}
	return nil, lastErr
}

// settleIntent resolves a terminal intent into order state. It returns the new
// order status and, when terminal, the BookingResult for the caller.
func (s *Service) settleIntent(ctx context.Context, userID uuid.UUID, order *orderRow, intent *brij.BookingIntent) (string, *BookingResult) {
	switch intent.Status {
	case brij.StatusBooked:
		res := s.finalizeBooking(ctx, userID, order, intent)
		return StatusCompleted, &res
	case brij.StatusRefunded:
		reason := strings.TrimSpace(intent.RefundReason)
		if reason == "" {
			reason = "the airline refunded this booking"
		}
		_ = s.reverseHold(ctx, userID, order.ID, order.HoldAmount, "refunded")
		_ = s.markRefunded(ctx, order.ID, reason)
		return StatusRefunded, &BookingResult{
			OrderID:   order.ID,
			IntentID:  order.IntentID,
			Route:     order.Route,
			AmountUSD: order.ExpectedEscrow.StringFixed(2),
			Status:    StatusRefunded,
		}
	default:
		return StatusBooked, nil
	}
}

// finalizeBooking marks the order completed, records the PNR, renders and
// delivers the ticket, and returns the booking result.
func (s *Service) finalizeBooking(ctx context.Context, userID uuid.UUID, order *orderRow, intent *brij.BookingIntent) BookingResult {
	pnr := ""
	if strings.TrimSpace(intent.AirlineOrderID) != "" && strings.TrimSpace(order.SupportCode) != "" {
		if ord, err := s.client.GetOrder(ctx, intent.AirlineOrderID, order.SupportCode); err == nil {
			pnr = ord.BookingReference
		} else {
			s.logger.Warn("failed to fetch airline order for PNR", zap.Error(err), zap.String("intent_id", order.IntentID))
		}
	}
	receipt := ticketReceiptJSON(order, intent, pnr)
	finalized := true
	if _, err := s.db.ExecContext(ctx, `
		UPDATE travel_orders SET status='completed', escrow_amount_usdc=$2, airline_order_id=$3, order_ref=$4,
		       booking_reference=$5, pnr=$5, ticketed_at=NOW(), receipt=$6, updated_at=NOW()
		WHERE id=$1 AND status IN ('held','booked')`,
		order.ID, order.ExpectedEscrow, nullStr(intent.AirlineOrderID), nullStr(order.OrderRef),
		nullStr(pnr), receipt); err != nil {
		s.logger.Error("failed to mark travel order completed", zap.Error(err), zap.String("order_id", order.ID.String()))
		finalized = false
	}

	res := BookingResult{
		OrderID:   order.ID,
		IntentID:  order.IntentID,
		Provider:  order.Provider,
		Route:     order.Route,
		TripDate:  order.TripDate,
		OrderRef:  order.OrderRef,
		PNR:       pnr,
		AmountUSD: order.ExpectedEscrow.StringFixed(2),
		Status:    StatusCompleted,
	}
	if !finalized {
		// The intent is booked but the order row is not 'completed', so report
		// 'booked' and let RunRecovery finalize + deliver it later.
		res.Status = StatusBooked
	}
	s.deliverTicket(ctx, order, pnr, &res)
	return res
}

// deliverTicket sends the booking confirmation to the user's thread and marks
// the order delivered on success.
func (s *Service) deliverTicket(ctx context.Context, order *orderRow, pnr string, res *BookingResult) {
	if s.deliverer == nil {
		s.logger.Warn("no ticket messenger configured; ticket not sent", zap.String("order_id", order.ID.String()))
		return
	}
	if err := s.deliverer.SendMessage(ctx, order.UserID, buildTicketMessage(order, pnr)); err != nil {
		s.logger.Warn("failed to deliver travel ticket; recovery will retry", zap.Error(err), zap.String("order_id", order.ID.String()))
		return
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE travel_orders SET ticket_delivered=TRUE, updated_at=NOW() WHERE id=$1`, order.ID); err != nil {
		s.logger.Warn("failed to mark ticket delivered", zap.Error(err), zap.String("order_id", order.ID.String()))
	}
	if res != nil {
		res.TicketSent = true
	}
}

// markRefunded records a refunded intent terminal state.
func (s *Service) markRefunded(ctx context.Context, orderID uuid.UUID, reason string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE travel_orders SET status='refunded', refund_reason=$2, refunded_at=NOW(), updated_at=NOW() WHERE id=$1`,
		orderID, reason)
	return err
}

// GetIntentStatus fetches the live BRIJ intent state for the user's order.
func (s *Service) GetIntentStatus(ctx context.Context, userID uuid.UUID, intentID string) (*brij.BookingIntent, error) {
	if s.client == nil {
		return nil, fmt.Errorf("flight booking is not configured")
	}
	if _, err := s.loadOrderByIntent(ctx, userID, intentID); err != nil {
		return nil, err
	}
	return s.client.GetIntent(ctx, intentID)
}

// GetOrderStatus fetches the airline order (PNR) for a ticketed booking.
func (s *Service) GetOrderStatus(ctx context.Context, userID uuid.UUID, intentID string) (*brij.OrderStatus, error) {
	if s.client == nil {
		return nil, fmt.Errorf("flight booking is not configured")
	}
	order, err := s.loadOrderByIntent(ctx, userID, intentID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(order.AirlineOrderID) == "" || strings.TrimSpace(order.SupportCode) == "" {
		return nil, fmt.Errorf("this flight has not been ticketed yet")
	}
	return s.client.GetOrder(ctx, order.AirlineOrderID, order.SupportCode)
}

// RequestRefund files a manual refund request for a booked intent (x402-paid).
// Refunds are reviewed by BRIJ; this is a request, not a guarantee.
func (s *Service) RequestRefund(ctx context.Context, userID uuid.UUID, intentID, reason, contact string) (*brij.RefundResponse, error) {
	if s.client == nil {
		return nil, fmt.Errorf("flight booking is not configured")
	}
	order, err := s.loadOrderByIntent(ctx, userID, intentID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(order.SupportCode) == "" {
		return nil, fmt.Errorf("missing support code for this order")
	}
	if order.Status != StatusBooked && order.Status != StatusCompleted {
		return nil, fmt.Errorf("this flight cannot be refunded in its current state")
	}
	familyName := passengerFamilyName(order.Passengers)
	if familyName == "" {
		return nil, fmt.Errorf("passenger family name is missing for this order")
	}
	if strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("a refund reason is required")
	}
	resp, err := s.client.RequestRefund(ctx, brij.RefundRequest{
		IntentID: intentID,
		Reason:   strings.TrimSpace(reason),
		Contact:  strings.TrimSpace(contact),
	}, order.SupportCode, familyName)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// --- passenger validation ---

var (
	e164Pattern   = regexp.MustCompile(`^\+[1-9]\d{6,14}$`)
	datePattern   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	titlePattern  = regexp.MustCompile(`^(mr|mrs|ms|miss|dr)$`)
	genderPattern = regexp.MustCompile(`^(m|f)$`)
)

// validatePassenger enforces the airline contract BRIJ validates on the wire
// (title, gender, ISO date of birth, E.164 phone) before any money moves.
func validatePassenger(p *brij.PassengerInput) error {
	if p == nil {
		return fmt.Errorf("a passenger is required")
	}
	if strings.TrimSpace(p.GivenName) == "" || strings.TrimSpace(p.FamilyName) == "" {
		return fmt.Errorf("passenger given and family name are required")
	}
	if !datePattern.MatchString(strings.TrimSpace(p.BornOn)) {
		return fmt.Errorf("passenger date of birth must be YYYY-MM-DD (e.g. 1990-04-12)")
	}
	if !titlePattern.MatchString(strings.ToLower(strings.TrimSpace(p.Title))) {
		return fmt.Errorf("passenger title must be one of mr, mrs, ms, miss, dr")
	}
	if !genderPattern.MatchString(strings.ToLower(strings.TrimSpace(p.Gender))) {
		return fmt.Errorf("passenger gender must be m or f")
	}
	if strings.TrimSpace(p.Email) == "" || !strings.Contains(p.Email, "@") {
		return fmt.Errorf("a valid passenger email is required")
	}
	if !e164Pattern.MatchString(strings.TrimSpace(p.PhoneNumber)) {
		return fmt.Errorf("passenger phone must be E.164 (e.g. +447400123456)")
	}
	return nil
}
