package travel

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/travu"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// RunRecovery reconciles stuck travel orders. Safe to call repeatedly from a
// background worker:
//   - 'held' orders past abandonAge with no Travu order id are reversed (Travu
//     was never asked to book, so the NGN float was not drawn down).
//   - 'booked' flight orders (tentative placed, ticketing incomplete) past
//     ticketRetryAge are re-ticketed; on success they complete and deliver.
//   - 'completed' orders whose ticket never reached the user are re-delivered.
func (s *Service) RunRecovery(ctx context.Context) {
	const (
		abandonAge     = 15 * time.Minute
		ticketRetryAge = 3 * time.Minute
	)
	s.reverseAbandoned(ctx, abandonAge)
	s.retryTicketing(ctx, ticketRetryAge)
	s.redeliverTickets(ctx)
}

func (s *Service) reverseAbandoned(ctx context.Context, age time.Duration) {
	if s.ledger == nil {
		return
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, COALESCE(hold_amount,0), COALESCE(rail_fee_usdc,0)
		FROM travel_orders
		WHERE status='held' AND travu_order_id IS NULL AND deposit_id IS NULL
		  AND created_at < NOW() - make_interval(secs => $1) LIMIT 25`, int(age.Seconds()))
	if err != nil {
		s.logger.Error("travu recovery: abandoned query failed", zap.Error(err))
		return
	}
	defer rows.Close()
	type item struct {
		orderID   uuid.UUID
		userID    uuid.UUID
		hold, fee decimal.Decimal
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.orderID, &it.userID, &it.hold, &it.fee); err != nil {
			continue
		}
		items = append(items, it)
	}
	for _, it := range items {
		if it.hold.IsPositive() {
			_ = s.reverseHold(ctx, it.userID, it.orderID, it.hold, it.fee, "abandoned_recovery")
		}
	}
}

func (s *Service) retryTicketing(ctx context.Context, age time.Duration) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, COALESCE(booking_id,''), COALESCE(pnr,'')
		FROM travel_orders
		WHERE status='booked' AND mode='flight' AND booking_id IS NOT NULL AND pnr IS NOT NULL
		  AND created_at < NOW() - make_interval(secs => $1) LIMIT 25`, int(age.Seconds()))
	if err != nil {
		s.logger.Error("travu recovery: booked query failed", zap.Error(err))
		return
	}
	defer rows.Close()
	type item struct {
		orderID   uuid.UUID
		userID    uuid.UUID
		bookingID string
		pnr       string
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.orderID, &it.userID, &it.bookingID, &it.pnr); err != nil {
			continue
		}
		items = append(items, it)
	}
	for _, it := range items {
		ticket, err := s.client.TicketFlight(ctx, travu.TicketFlightRequest{BookingID: it.bookingID, PNRNumber: it.pnr})
		if err != nil {
			s.logger.Warn("travu recovery: re-ticketing failed, will retry", zap.Error(err), zap.String("order_id", it.orderID.String()))
			continue
		}
		s.finalizeBooking(ctx, it.userID, it.orderID, ModeFlight, ticket)
	}
}

func (s *Service) redeliverTickets(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, mode, receipt
		FROM travel_orders
		WHERE status='completed' AND ticket_delivered=FALSE AND receipt IS NOT NULL LIMIT 25`)
	if err != nil {
		s.logger.Error("travu recovery: undelivered query failed", zap.Error(err))
		return
	}
	defer rows.Close()
	type item struct {
		orderID uuid.UUID
		userID  uuid.UUID
		mode    string
		receipt []byte
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.orderID, &it.userID, &it.mode, &it.receipt); err != nil {
			continue
		}
		items = append(items, it)
	}
	for _, it := range items {
		var receipt travu.OrderReceipt
		if err := json.Unmarshal(it.receipt, &receipt); err != nil {
			s.logger.Warn("travu recovery: could not decode stored receipt", zap.Error(err), zap.String("order_id", it.orderID.String()))
			continue
		}
		s.deliverTicket(ctx, it.userID, it.orderID, it.mode, &receipt, nil)
	}
}
