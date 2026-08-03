// Package travel orchestrates flight bookings through BRIJ Travel
// (travel.brij.fi), Rail's flight provider.
//
// Payment model: BRIJ is settled per call with x402 micropayments in USDC on
// Solana mainnet from Rail's funding wallet (search/intents/book/refunds). Rail
// charges the user in USDC via a double-entry ledger hold on their spend
// balance, so the user is never exposed to the wallet. BRIJ records a
// customer_support_code at intent creation that is returned exactly once —
// the service persists it immediately because it is required to read the
// airline order and to file refunds.
//
// Booking is escrow-backed and asynchronous: select a flight (creates a paid
// intent and holds the user's funds at book time), book it (pays the intent's
// escrow + one passenger), then poll GET /air/intents/{id} until the intent is
// booked or refunded. A recovery worker (RunRecovery) polls stuck bookings,
// reverses abandoned holds, and re-delivers tickets.
package travel

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/brij"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Booking modes.
const (
	ModeBus    = "bus"
	ModeFlight = "flight"
)

// Order lifecycle states in travel_orders.
const (
	StatusHeld      = "held"
	StatusBooked    = "booked"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusRefunded  = "refunded"
	StatusReversed  = "reversed"
)

// LedgerService for balance checks, holds, and reversals on the spend balance.
type LedgerService interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
	CreateTransaction(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, txType entities.TransactionType, amount decimal.Decimal, metadata map[string]interface{}) error
	ReverseTransaction(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, originalTxID string, amount decimal.Decimal, metadata map[string]interface{}) error
}

// TicketMessenger sends a booking confirmation to the user's messaging thread.
// Implemented in the DI layer over the platform bridge dispatcher.
type TicketMessenger interface {
	SendMessage(ctx context.Context, userID uuid.UUID, text string) error
}

// Config carries the tunables sourced from the BRIJ config block.
type Config struct {
	DeveloperFeePercent float64
	MaxEscrowUSD        float64
}

// Service is the travel-booking orchestrator.
type Service struct {
	db        *sqlx.DB
	client    *brij.Client
	ledger    LedgerService
	deliverer TicketMessenger
	cfg       Config
	logger    *zap.Logger
}

// NewService builds the travel-booking service. client is required.
func NewService(db *sqlx.DB, client *brij.Client, cfg Config, logger *zap.Logger) *Service {
	return &Service{db: db, client: client, cfg: cfg, logger: logger}
}

func (s *Service) SetLedger(l LedgerService)            { s.ledger = l }
func (s *Service) SetTicketMessenger(d TicketMessenger) { s.deliverer = d }

// SearchFlights returns live flight offers for a one-way route.
func (s *Service) SearchFlights(ctx context.Context, origin, destination, departDate string, adults int) ([]brij.OfferSummary, error) {
	if s.client == nil {
		return nil, fmt.Errorf("flight booking is not configured")
	}
	if adults <= 0 {
		adults = 1
	}
	result, err := s.client.Search(ctx, brij.SearchRequest{
		OriginIATA:      strings.ToUpper(strings.TrimSpace(origin)),
		DestinationIATA: strings.ToUpper(strings.TrimSpace(destination)),
		DepartDate:      strings.TrimSpace(departDate),
		Adults:          adults,
	})
	if err != nil {
		return nil, err
	}
	return result.Offers, nil
}

// CreateIntentRequest resolves a chosen offer into a lock request.
type CreateIntentRequest struct {
	OfferID     string
	Airline     string
	Origin      string
	Destination string
	DepartingAt string
	ArrivingAt  string
	AmountUSD   string
}

// BookingIntentResult summarizes a freshly created intent.
type BookingIntentResult struct {
	IntentID    string `json:"intent_id"`
	OfferID     string `json:"offer_id"`
	Airline     string `json:"airline"`
	Origin      string `json:"origin"`
	Destination string `json:"destination"`
	DepartingAt string `json:"departing_at"`
	AmountUSD   string `json:"amount_usd"`
	ExpiresAt   string `json:"expires_at"`
	Status      string `json:"status"`
}

// CreateIntent locks an offer with BRIJ (x402-paid) and persists the intent.
// The customer support code is returned only once, so the order row is written
// immediately. No user funds are held yet — the hold happens at BookFlight.
func (s *Service) CreateIntent(ctx context.Context, userID uuid.UUID, req CreateIntentRequest) (*BookingIntentResult, error) {
	if s.client == nil {
		return nil, fmt.Errorf("flight booking is not configured")
	}
	offerID := strings.TrimSpace(req.OfferID)
	if offerID == "" {
		return nil, fmt.Errorf("offer id is required — search for a flight first")
	}
	intent, err := s.client.CreateIntent(ctx, brij.CreateIntentRequest{OfferID: offerID})
	if err != nil {
		return nil, fmt.Errorf("could not lock this flight: %w", err)
	}
	if strings.TrimSpace(intent.ID) == "" || strings.TrimSpace(intent.CustomerSupportCode) == "" {
		return nil, fmt.Errorf("BRIJ returned an incomplete intent")
	}
	if _, err := s.persistIntent(ctx, userID, req, intent); err != nil {
		// The BRIJ intent is already created and x402-paid at this point, but it
		// is not tracked in travel_orders, so recovery cannot reach it. Surface
		// the intent id so support can cancel it if needed.
		s.logger.Error("BRIJ intent created but not persisted",
			zap.Error(err),
			zap.String("intent_id", intent.ID),
			zap.String("offer_id", offerID),
			zap.String("user_id", userID.String()))
		return nil, fmt.Errorf("failed to record this flight — please try again (intent %s was created but not stored)", intent.ID)
	}
	s.logger.Info("BRIJ intent created",
		zap.String("intent_id", intent.ID),
		zap.String("offer_id", offerID),
		zap.String("user_id", userID.String()))
	return &BookingIntentResult{
		IntentID:    intent.ID,
		OfferID:     offerID,
		Airline:     req.Airline,
		Origin:      req.Origin,
		Destination: req.Destination,
		DepartingAt: req.DepartingAt,
		AmountUSD:   intent.EscrowAmountDecimal(),
		ExpiresAt:   intent.ExpiresAt,
		Status:      StatusHeld,
	}, nil
}

// persistIntent writes the intent row. hold_amount starts at zero; BookFlight
// populates it when the user's funds are reserved.
func (s *Service) persistIntent(ctx context.Context, userID uuid.UUID, req CreateIntentRequest, intent *brij.BookingIntent) (uuid.UUID, error) {
	orderID := uuid.New()
	escrow := intentEscrowDecimal(intent)
	if !escrow.IsPositive() {
		return uuid.Nil, fmt.Errorf("BRIJ returned an invalid escrow amount (%s) for offer %q", escrow.String(), intent.OfferID)
	}
	route := fmt.Sprintf("%s to %s", strings.TrimSpace(req.Origin), strings.TrimSpace(req.Destination))
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO travel_orders (id, user_id, mode, provider, status, route, departure_terminal, destination_terminal, trip_date,
			intent_id, offer_id, customer_support_code, expected_escrow_amount, escrow_mint, escrow_address, amount_ngn, amount_usdc, hold_amount)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,0,$16,0)`,
		orderID, userID, ModeFlight, nullStr(req.Airline), StatusHeld, nullStr(route), nullStr(req.Origin), nullStr(req.Destination),
		nullStr(dateOnly(req.DepartingAt)), intent.ID, intent.OfferID, intent.CustomerSupportCode,
		escrow, nullStr(intent.ExpectedEscrowMint), nullStr(intent.EscrowAddress), escrow); err != nil {
		return uuid.Nil, err
	}
	return orderID, nil
}

// holdFunds validates the balance and debits the spend balance by totalHold
// (escrow + Rail service fee). On completion the debit stands; on failure or
// refund the recovery path reverses it.
func (s *Service) holdFunds(ctx context.Context, userID uuid.UUID, totalHold decimal.Decimal) error {
	if s.ledger == nil {
		return fmt.Errorf("payment infrastructure not available")
	}
	balance, err := s.ledger.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("failed to check balance: %w", err)
	}
	if balance.LessThan(totalHold) {
		return fmt.Errorf("insufficient balance: have %s USDC, need ~%s USDC",
			balance.StringFixed(2), totalHold.StringFixed(2))
	}
	if err := s.ledger.CreateTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
		entities.TransactionTypeWithdrawal, totalHold, map[string]interface{}{
			"provider": "brij", "type": "travel_hold", "mode": ModeFlight,
			"hold_amount": totalHold.String(),
		}); err != nil {
		return fmt.Errorf("failed to reserve funds: %w", err)
	}
	return nil
}

// validateAmount enforces the per-booking USDC ceiling.
func (s *Service) validateAmount(amountUSD decimal.Decimal) error {
	if !amountUSD.IsPositive() {
		return fmt.Errorf("fare must be positive")
	}
	if s.cfg.MaxEscrowUSD > 0 && amountUSD.GreaterThan(decimal.NewFromFloat(s.cfg.MaxEscrowUSD)) {
		return fmt.Errorf("this fare exceeds the per-booking limit of $%.2f", s.cfg.MaxEscrowUSD)
	}
	return nil
}

// reverseHold reverses the full ledger hold for a booking that never completed
// and marks the order terminal. deposit_id is claimed to prevent double
// reversal by the recovery worker.
func (s *Service) reverseHold(ctx context.Context, userID, orderID uuid.UUID, amount decimal.Decimal, reason string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE travel_orders SET status='reversed', deposit_id=gen_random_uuid(), failure_reason=$2, updated_at=NOW()
		WHERE id=$1 AND status IN ('held','booked')`, orderID, reason)
	if err != nil {
		s.logger.Error("failed to mark travel order reversed", zap.Error(err), zap.String("order_id", orderID.String()))
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // already claimed or terminal
	}
	if s.ledger == nil {
		return nil
	}
	if err := s.ledger.ReverseTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
		uuid.New().String(), amount, map[string]interface{}{
			"provider": "brij", "type": "travel_" + reason + "_reversal", "order_id": orderID.String(),
		}); err != nil {
		s.logger.Error("CRITICAL: failed to reverse travel hold", zap.Error(err), zap.String("order_id", orderID.String()))
		if _, unclaimErr := s.db.ExecContext(ctx,
			`UPDATE travel_orders SET deposit_id=NULL, status='held', updated_at=NOW() WHERE id=$1 AND status='reversed'`, orderID); unclaimErr != nil {
			return fmt.Errorf("reversal failed: %w; unclaim also failed: %v", err, unclaimErr)
		}
		return err
	}
	return nil
}

// markFailed terminalizes an order without touching the ledger (used for
// expired intents where no funds were ever held).
func (s *Service) markFailed(ctx context.Context, orderID uuid.UUID, reason string) {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE travel_orders SET status='failed', failure_reason=$2, updated_at=NOW() WHERE id=$1 AND status='held'`,
		orderID, reason); err != nil {
		s.logger.Warn("failed to mark travel order failed", zap.Error(err), zap.String("order_id", orderID.String()))
	}
}

// GetBookingHistory returns a user's recent flight bookings.
func (s *Service) GetBookingHistory(ctx context.Context, userID uuid.UUID, limit int) ([]BookingHistoryItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, mode, COALESCE(provider,''), COALESCE(route,''), COALESCE(trip_date,''), COALESCE(booking_reference,''),
		       COALESCE(amount_usdc,0), status, ticket_delivered, COALESCE(intent_id,''), created_at
		FROM travel_orders WHERE user_id=$1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BookingHistoryItem
	for rows.Next() {
		var it BookingHistoryItem
		var usdc sql.NullFloat64
		if err := rows.Scan(&it.ID, &it.Mode, &it.Provider, &it.Route, &it.TripDate, &it.PNR, &usdc, &it.Status, &it.TicketDelivered, &it.IntentID, &it.CreatedAt); err != nil {
			return nil, err
		}
		it.AmountUSDC = usdc.Float64
		out = append(out, it)
	}
	return out, rows.Err()
}

// BookingHistoryItem is a single history row.
type BookingHistoryItem struct {
	ID              uuid.UUID `json:"id"`
	Mode            string    `json:"mode"`
	Provider        string    `json:"provider"`
	Route           string    `json:"route"`
	TripDate        string    `json:"trip_date"`
	PNR             string    `json:"pnr"`
	AmountUSDC      float64   `json:"amount_usdc"`
	Status          string    `json:"status"`
	TicketDelivered bool      `json:"ticket_delivered"`
	IntentID        string    `json:"intent_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// orderRow is the stored state for a BRIJ travel order.
type orderRow struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	Mode           string
	Provider       string
	Route          string
	TripDate       string
	Status         string
	IntentID       string
	OfferID        string
	SupportCode    string
	ExpectedEscrow decimal.Decimal
	EscrowAmount   decimal.Decimal
	AirlineOrderID string
	OrderRef       string
	BookingRef     string
	HoldAmount     decimal.Decimal
	Passengers     []byte
	Receipt        []byte
	TicketSent     bool
	CreatedAt      time.Time
}

// loadOrderByIntent loads an order owned by the user by its BRIJ intent id.
func (s *Service) loadOrderByIntent(ctx context.Context, userID uuid.UUID, intentID string) (*orderRow, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, mode, COALESCE(provider,''), COALESCE(route,''), COALESCE(trip_date,''), status,
		       COALESCE(intent_id,''), COALESCE(offer_id,''), COALESCE(customer_support_code,''),
		       COALESCE(expected_escrow_amount,0), COALESCE(escrow_amount_usdc,0),
		       COALESCE(airline_order_id,''), COALESCE(order_ref,''), COALESCE(booking_reference,''),
		       COALESCE(hold_amount,0), passengers, receipt, ticket_delivered, created_at
		FROM travel_orders WHERE intent_id=$1 AND user_id=$2`, intentID, userID)
	var o orderRow
	err := row.Scan(&o.ID, &o.UserID, &o.Mode, &o.Provider, &o.Route, &o.TripDate, &o.Status,
		&o.IntentID, &o.OfferID, &o.SupportCode, &o.ExpectedEscrow, &o.EscrowAmount,
		&o.AirlineOrderID, &o.OrderRef, &o.BookingRef, &o.HoldAmount, &o.Passengers, &o.Receipt, &o.TicketSent, &o.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no flight intent %q found for this user", intentID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load flight intent: %w", err)
	}
	return &o, nil
}

// loadOrderByID loads an order by id (recovery path — no user scoping).
func (s *Service) loadOrderByID(ctx context.Context, id uuid.UUID) (*orderRow, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, mode, COALESCE(provider,''), COALESCE(route,''), COALESCE(trip_date,''), status,
		       COALESCE(intent_id,''), COALESCE(offer_id,''), COALESCE(customer_support_code,''),
		       COALESCE(expected_escrow_amount,0), COALESCE(escrow_amount_usdc,0),
		       COALESCE(airline_order_id,''), COALESCE(order_ref,''), COALESCE(booking_reference,''),
		       COALESCE(hold_amount,0), passengers, receipt, ticket_delivered, created_at
		FROM travel_orders WHERE id=$1`, id)
	var o orderRow
	err := row.Scan(&o.ID, &o.UserID, &o.Mode, &o.Provider, &o.Route, &o.TripDate, &o.Status,
		&o.IntentID, &o.OfferID, &o.SupportCode, &o.ExpectedEscrow, &o.EscrowAmount,
		&o.AirlineOrderID, &o.OrderRef, &o.BookingRef, &o.HoldAmount, &o.Passengers, &o.Receipt, &o.TicketSent, &o.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("travel order %s not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load travel order: %w", err)
	}
	return &o, nil
}

// intentEscrowDecimal converts an intent's atomic escrow amount to USDC.
func intentEscrowDecimal(i *brij.BookingIntent) decimal.Decimal {
	if i == nil {
		return decimal.Zero
	}
	return decimal.NewFromInt(i.ExpectedEscrowAmount).Div(decimal.NewFromInt(1_000_000)).Round(6)
}

// dateOnly strips the time portion from an ISO-8601 timestamp.
func dateOnly(iso string) string {
	iso = strings.TrimSpace(iso)
	if len(iso) >= 10 {
		return iso[:10]
	}
	return iso
}

// railFee computes Rail's service fee on an escrow amount.
func (s *Service) railFee(escrow decimal.Decimal) decimal.Decimal {
	return escrow.Mul(decimal.NewFromFloat(s.cfg.DeveloperFeePercent / 100)).Round(6)
}

// ticketReceiptJSON persists a structured receipt for audit / re-render.
func ticketReceiptJSON(order *orderRow, intent *brij.BookingIntent, pnr string) []byte {
	r := TicketReceipt{
		Provider:      order.Provider,
		Route:         order.Route,
		TripDate:      order.TripDate,
		OrderRef:      order.OrderRef,
		BookingRef:    pnr,
		IntentID:      order.IntentID,
		AmountUSD:     order.ExpectedEscrow.StringFixed(2),
		Status:        StatusCompleted,
		PassengerName: passengerFullName(order.Passengers),
	}
	raw, _ := json.Marshal(r)
	return raw
}

func nullStr(s string) interface{} {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
