// Package travel orchestrates bus and flight bookings through the Travu
// aggregator (interstate/intra-city bus + domestic/international flights).
//
// Payment model differs from Airbills: Travu debits Rail's prefunded NGN float
// on the Travu dashboard, so there is no per-booking crypto transfer. Instead,
// Rail charges the user in USDC via a double-entry ledger hold at the live FX
// rate, calls Travu to make the booking (which draws down the NGN float), and
// reconciles Rail's USDC revenue against the Travu wallet balance out of band.
//
// On a confirmed booking Rail renders a PDF ticket and delivers it to the user
// over their messaging thread (iMessage) via the bridge dispatcher. A recovery
// worker reverses abandoned holds and retries undelivered tickets.
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
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/travu"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Booking modes.
const (
	ModeBus    = "bus"
	ModeFlight = "flight"
)

// LedgerService for balance checks, holds, and reversals on the spend balance.
type LedgerService interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
	CreateTransaction(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, txType entities.TransactionType, amount decimal.Decimal, metadata map[string]interface{}) error
	ReverseTransaction(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, originalTxID string, amount decimal.Decimal, metadata map[string]interface{}) error
}

// CurrencyRateProvider returns the live USD/NGN rate for pricing and pre-checks.
type CurrencyRateProvider interface {
	GetLatestRate(ctx context.Context, from, to string) (decimal.Decimal, error)
}

// TicketDeliverer delivers a rendered PDF ticket to the user's messaging thread.
// Implemented by the platform bridge dispatcher in the DI layer.
type TicketDeliverer interface {
	DeliverTicket(ctx context.Context, userID uuid.UUID, caption, fileName string, pdf []byte) error
}

// Config carries the tunables sourced from TravuConfig.
type Config struct {
	DeveloperFeePercent float64
	MaxAmountNGN        float64
	FlightSearchEnabled bool
}

// Service is the travel-booking orchestrator.
type Service struct {
	db            *sqlx.DB
	client        *travu.Client
	ledger        LedgerService
	currencyRates CurrencyRateProvider
	deliverer     TicketDeliverer
	cfg           Config
	logger        *zap.Logger
}

// NewService builds the travel-booking service. client is required.
func NewService(db *sqlx.DB, client *travu.Client, cfg Config, logger *zap.Logger) *Service {
	return &Service{db: db, client: client, cfg: cfg, logger: logger}
}

func (s *Service) SetLedger(l LedgerService)               { s.ledger = l }
func (s *Service) SetCurrencyRates(c CurrencyRateProvider) { s.currencyRates = c }
func (s *Service) SetTicketDeliverer(d TicketDeliverer)    { s.deliverer = d }

// --- Search ---

// SearchBusTrips returns available bus trips for a route and date.
func (s *Service) SearchBusTrips(ctx context.Context, departureState, destinationState, tripDate string) ([]travu.Trip, error) {
	return s.client.CheckTrip(ctx, travu.CheckTripRequest{
		DepartureState:   strings.ToUpper(strings.TrimSpace(departureState)),
		DestinationState: strings.ToUpper(strings.TrimSpace(destinationState)),
		TripDate:         strings.TrimSpace(tripDate),
		Sort:             "date",
	})
}

// SearchFlights returns available flight options for an itinerary. Gated behind
// the flight-search feature flag until Travu enables the endpoint.
func (s *Service) SearchFlights(ctx context.Context, req travu.SearchFlightRequest) ([]travu.Trip, error) {
	if !s.cfg.FlightSearchEnabled {
		return nil, fmt.Errorf("flight search is coming soon — bus booking is available now")
	}
	return s.client.SearchFlight(ctx, req)
}

// ListStates returns the supported bus states.
func (s *Service) ListStates(ctx context.Context) ([]travu.State, error) {
	return s.client.GetStates(ctx)
}

// ListAirports returns the supported flight airports.
func (s *Service) ListAirports(ctx context.Context) ([]travu.Airport, error) {
	return s.client.GetAirports(ctx)
}

// --- Booking result ---

// BookingResult summarizes a completed booking for receipts.
type BookingResult struct {
	OrderID       uuid.UUID `json:"order_id"`
	Mode          string    `json:"mode"`
	Provider      string    `json:"provider"`
	TravuOrderID  string    `json:"travu_order_id"`
	OrderNumber   string    `json:"order_number"`
	PNR           string    `json:"pnr,omitempty"`
	Route         string    `json:"route"`
	TripDate      string    `json:"trip_date"`
	Seats         string    `json:"seats,omitempty"`
	AmountNGN     float64   `json:"amount_ngn"`
	AmountUSDC    string    `json:"amount_usdc"`
	Status        string    `json:"status"`
	TicketSent    bool      `json:"ticket_sent"`
}

// --- helpers shared by bus + flight booking ---

// quote converts an NGN fare into a USDC hold at the live FX rate, applying the
// Rail service fee. Returns (amountUSDC, railFee, totalHold, rate).
func (s *Service) quote(ctx context.Context, amountNGN float64) (amountUSDC, railFee, totalHold, rate decimal.Decimal, err error) {
	if s.currencyRates == nil {
		return z, z, z, z, fmt.Errorf("currency rates unavailable")
	}
	rate, err = s.currencyRates.GetLatestRate(ctx, "USD", "NGN")
	if err != nil {
		return z, z, z, z, fmt.Errorf("could not get exchange rate: %w", err)
	}
	if !rate.IsPositive() {
		return z, z, z, z, fmt.Errorf("invalid exchange rate")
	}
	amountUSDC = decimal.NewFromFloat(amountNGN).Div(rate).Round(6)
	railFee = amountUSDC.Mul(decimal.NewFromFloat(s.cfg.DeveloperFeePercent / 100)).Round(6)
	totalHold = amountUSDC.Add(railFee)
	return amountUSDC, railFee, totalHold, rate, nil
}

var z = decimal.Zero

// holdFunds validates the balance and places the USDC hold on the spend balance.
func (s *Service) holdFunds(ctx context.Context, userID uuid.UUID, mode string, amountNGN float64, totalHold, railFee decimal.Decimal) error {
	if s.ledger == nil {
		return fmt.Errorf("payment infrastructure not available")
	}
	balance, err := s.ledger.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("failed to check balance: %w", err)
	}
	if balance.LessThan(totalHold) {
		return fmt.Errorf("insufficient balance: have %s USDC, need ~%s USDC for ₦%.0f",
			balance.StringFixed(2), totalHold.StringFixed(2), amountNGN)
	}
	if err := s.ledger.CreateTransaction(ctx, userID, entities.AccountTypeSpendingBalance,
		entities.TransactionTypeWithdrawal, totalHold, map[string]interface{}{
			"provider": "travu", "type": "travel_hold", "mode": mode,
			"amount_ngn": amountNGN, "rail_fee": railFee.String(),
		}); err != nil {
		return fmt.Errorf("failed to reserve funds: %w", err)
	}
	return nil
}

// validateAmount enforces the per-booking NGN ceiling.
func (s *Service) validateAmount(amountNGN float64) error {
	if amountNGN <= 0 {
		return fmt.Errorf("fare must be positive")
	}
	if s.cfg.MaxAmountNGN > 0 && amountNGN > s.cfg.MaxAmountNGN {
		return fmt.Errorf("fare exceeds the per-booking limit of ₦%.0f", s.cfg.MaxAmountNGN)
	}
	return nil
}

// persistOrder inserts a held travel order and returns its id.
func (s *Service) persistOrder(ctx context.Context, o *orderRow) (uuid.UUID, error) {
	orderID := uuid.New()
	passengersJSON, _ := json.Marshal(o.Passengers)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO travel_orders (id, user_id, mode, provider, status, route, departure_terminal, destination_terminal, trip_date, seats, passengers, amount_ngn, amount_usdc, rail_fee_usdc, hold_amount, rate)
		VALUES ($1,$2,$3,$4,'held',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		orderID, o.UserID, o.Mode, nullStr(o.Provider), nullStr(o.Route), nullStr(o.DepartureTerminal),
		nullStr(o.DestinationTerminal), nullStr(o.TripDate), nullStr(o.Seats), passengersJSON,
		o.AmountNGN, o.AmountUSDC, o.RailFee, o.HoldAmount, o.Rate); err != nil {
		return uuid.Nil, err
	}
	return orderID, nil
}

// finalizeBooking records the confirmed receipt, renders + delivers the ticket,
// and marks the order completed. Funds have already been booked with Travu, so
// this never reverses the hold — delivery failures are left for recovery.
func (s *Service) finalizeBooking(ctx context.Context, userID, orderID uuid.UUID, mode string, receipt *travu.OrderReceipt) BookingResult {
	receiptJSON, _ := json.Marshal(receipt)
	if _, err := s.db.ExecContext(ctx, `
		UPDATE travel_orders SET status='completed', travu_order_id=$2, travu_order_number=$3, booking_id=$4, pnr=$5, receipt=$6, updated_at=NOW()
		WHERE id=$1 AND status IN ('held','booked')`,
		orderID, nullStr(receipt.OrderID.String()), nullStr(receipt.OrderNumber.String()),
		nullStr(receipt.BookingID.String()), nullStr(receipt.PNRNumber.String()), receiptJSON); err != nil {
		s.logger.Error("failed to mark travel order completed", zap.Error(err), zap.String("order_id", orderID.String()))
	}

	res := BookingResult{
		OrderID:      orderID,
		Mode:         mode,
		Provider:     receipt.Provider,
		TravuOrderID: receipt.OrderID.String(),
		OrderNumber:  receipt.OrderNumber.String(),
		PNR:          receipt.PNRNumber.String(),
		Route:        receipt.Narration,
		TripDate:     receipt.OrderTicketDate,
		Seats:        receipt.OrderSeats.String(),
		AmountNGN:    receipt.AmountNGN(),
		Status:       "completed",
	}

	s.deliverTicket(ctx, userID, orderID, mode, receipt, &res)
	return res
}

// deliverTicket renders the PDF and sends it to the user's thread, marking the
// order as delivered on success.
func (s *Service) deliverTicket(ctx context.Context, userID, orderID uuid.UUID, mode string, receipt *travu.OrderReceipt, res *BookingResult) {
	if s.deliverer == nil {
		s.logger.Warn("no ticket deliverer configured; ticket not sent", zap.String("order_id", orderID.String()))
		return
	}
	pdf, err := RenderTicketPDF(mode, receipt)
	if err != nil {
		s.logger.Error("failed to render travel ticket PDF", zap.Error(err), zap.String("order_id", orderID.String()))
		return
	}
	caption := ticketCaption(mode, receipt)
	fileName := fmt.Sprintf("rail-ticket-%s.pdf", strings.TrimSpace(receipt.OrderNumber.String()))
	if err := s.deliverer.DeliverTicket(ctx, userID, caption, fileName, pdf); err != nil {
		s.logger.Warn("failed to deliver travel ticket; recovery will retry", zap.Error(err), zap.String("order_id", orderID.String()))
		return
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE travel_orders SET ticket_delivered=TRUE, updated_at=NOW() WHERE id=$1`, orderID); err != nil {
		s.logger.Warn("failed to mark ticket delivered", zap.Error(err), zap.String("order_id", orderID.String()))
	}
	if res != nil {
		res.TicketSent = true
	}
}

// reverseHold reverses the full ledger hold for a booking that never completed
// and marks the order reversed. deposit_id is claimed to prevent double
// reversal by the recovery worker.
func (s *Service) reverseHold(ctx context.Context, userID, orderID uuid.UUID, amount, railFee decimal.Decimal, reason string) error {
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
			"provider": "travu", "type": "travel_" + reason + "_reversal", "order_id": orderID.String(),
			"rail_fee": railFee.String(),
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

// GetBookingHistory returns a user's recent travel bookings.
func (s *Service) GetBookingHistory(ctx context.Context, userID uuid.UUID, limit int) ([]BookingHistoryItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, mode, COALESCE(provider,''), COALESCE(route,''), COALESCE(trip_date,''), COALESCE(seats,''), COALESCE(pnr,''), amount_ngn, COALESCE(amount_usdc,0), status, ticket_delivered, created_at
		FROM travel_orders WHERE user_id=$1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BookingHistoryItem
	for rows.Next() {
		var it BookingHistoryItem
		var usdc sql.NullString
		if err := rows.Scan(&it.ID, &it.Mode, &it.Provider, &it.Route, &it.TripDate, &it.Seats, &it.PNR, &it.AmountNGN, &usdc, &it.Status, &it.TicketDelivered, &it.CreatedAt); err != nil {
			return nil, err
		}
		it.AmountUSDC = usdc.String
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
	Seats           string    `json:"seats"`
	PNR             string    `json:"pnr"`
	AmountNGN       float64   `json:"amount_ngn"`
	AmountUSDC      string    `json:"amount_usdc"`
	Status          string    `json:"status"`
	TicketDelivered bool      `json:"ticket_delivered"`
	CreatedAt       time.Time `json:"created_at"`
}

// orderRow carries the fields persisted for a new held order.
type orderRow struct {
	UserID              uuid.UUID
	Mode                string
	Provider            string
	Route               string
	DepartureTerminal   string
	DestinationTerminal string
	TripDate            string
	Seats               string
	Passengers          interface{}
	AmountNGN           float64
	AmountUSDC          decimal.Decimal
	RailFee             decimal.Decimal
	HoldAmount          decimal.Decimal
	Rate                decimal.Decimal
}

func nullStr(s string) interface{} {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
