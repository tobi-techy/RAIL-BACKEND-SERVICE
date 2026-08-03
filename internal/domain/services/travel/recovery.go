package travel

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/brij"
	"go.uber.org/zap"
)

// RunRecovery reconciles stuck travel orders. Safe to call repeatedly from a
// background worker:
//   - 'held' orders past abandonAge with a user hold but no ticketed intent are
//     checked against BRIJ: booked -> finalize, refunded -> reverse + mark
//     refunded, still active -> reverse the hold (abandoned).
//   - 'booked' orders past ticketingAge are polled until their intent reaches a
//     terminal state (booked -> complete + deliver, refunded -> reverse).
//   - 'completed' orders whose ticket never reached the user are re-delivered.
func (s *Service) RunRecovery(ctx context.Context) {
	const (
		abandonAge   = 15 * time.Minute
		ticketingAge = 3 * time.Minute
	)
	s.reconcileStuck(ctx, abandonAge)
	s.finalizeTicketed(ctx, ticketingAge)
	s.redeliverTickets(ctx)
}

type stuckOrder struct {
	orderID uuid.UUID
	intent  string
}

// reconcileStuck resolves 'held' orders that placed a user hold but never
// reached a terminal BRIJ state.
func (s *Service) reconcileStuck(ctx context.Context, age time.Duration) {
	if s.ledger == nil || s.client == nil {
		return
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(intent_id,'')
		FROM travel_orders
		WHERE status='held' AND hold_amount > 0 AND deposit_id IS NULL
		  AND created_at < NOW() - make_interval(secs => $1) LIMIT 25`, int(age.Seconds()))
	if err != nil {
		s.logger.Error("travel recovery: stuck query failed", zap.Error(err))
		return
	}
	defer rows.Close()
	var items []stuckOrder
	for rows.Next() {
		var it stuckOrder
		if err := rows.Scan(&it.orderID, &it.intent); err != nil {
			continue
		}
		items = append(items, it)
	}
	for _, it := range items {
		s.settleStuckOrder(ctx, it)
	}
}

func (s *Service) settleStuckOrder(ctx context.Context, it stuckOrder) {
	order, err := s.loadOrderByID(ctx, it.orderID)
	if err != nil {
		return
	}
	if strings.TrimSpace(it.intent) == "" {
		// No BRIJ intent: the hold was placed but Book never ran. Reverse.
		_ = s.reverseHold(ctx, order.UserID, order.ID, order.HoldAmount, "abandoned_recovery")
		return
	}
	intent, err := s.client.GetIntent(ctx, it.intent)
	if err != nil {
		s.logger.Warn("travel recovery: intent lookup failed, will retry", zap.Error(err), zap.String("order_id", order.ID.String()))
		return
	}
	switch intent.Status {
	case brij.StatusBooked:
		s.finalizeBooking(ctx, order.UserID, order, intent)
	case brij.StatusRefunded:
		reason := strings.TrimSpace(intent.RefundReason)
		if reason == "" {
			reason = "the airline refunded this booking"
		}
		_ = s.reverseHold(ctx, order.UserID, order.ID, order.HoldAmount, "refunded")
		_ = s.markRefunded(ctx, order.ID, reason)
		s.logger.Info("travel recovery: refunded stuck order", zap.String("order_id", order.ID.String()))
	default:
		// Still active past abandonAge with funds held on the user: release.
		_ = s.reverseHold(ctx, order.UserID, order.ID, order.HoldAmount, "abandoned_recovery")
		s.logger.Warn("travel recovery: reversed abandoned booking", zap.String("order_id", order.ID.String()), zap.String("intent_id", it.intent))
	}
}

// finalizeTicketed polls 'booked' orders whose intent should be terminal.
func (s *Service) finalizeTicketed(ctx context.Context, age time.Duration) {
	if s.client == nil {
		return
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM travel_orders
		WHERE status='booked' AND intent_id IS NOT NULL
		  AND created_at < NOW() - make_interval(secs => $1) LIMIT 25`, int(age.Seconds()))
	if err != nil {
		s.logger.Error("travel recovery: booked query failed", zap.Error(err))
		return
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		order, err := s.loadOrderByID(ctx, id)
		if err != nil {
			continue
		}
		intent, err := s.client.GetIntent(ctx, order.IntentID)
		if err != nil {
			s.logger.Warn("travel recovery: intent lookup failed, will retry", zap.Error(err), zap.String("order_id", order.ID.String()))
			continue
		}
		if !intent.IsTerminal() {
			continue
		}
		if intent.Status == brij.StatusBooked {
			s.finalizeBooking(ctx, order.UserID, order, intent)
			s.logger.Info("travel recovery: finalized ticketed booking", zap.String("order_id", order.ID.String()))
		} else {
			reason := strings.TrimSpace(intent.RefundReason)
			if reason == "" {
				reason = "the airline refunded this booking"
			}
			_ = s.reverseHold(ctx, order.UserID, order.ID, order.HoldAmount, "refunded")
			_ = s.markRefunded(ctx, order.ID, reason)
			s.logger.Warn("travel recovery: booking refunded before ticketing", zap.String("order_id", order.ID.String()))
		}
	}
}

// redeliverTickets re-sends the booking message for completed orders whose
// ticket never reached the user.
func (s *Service) redeliverTickets(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM travel_orders
		WHERE status='completed' AND ticket_delivered=FALSE AND receipt IS NOT NULL LIMIT 25`)
	if err != nil {
		s.logger.Error("travel recovery: undelivered query failed", zap.Error(err))
		return
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		order, err := s.loadOrderByID(ctx, id)
		if err != nil {
			continue
		}
		pnr := ""
		if order.BookingRef != "" {
			pnr = order.BookingRef
		} else if receipt := decodeReceipt(order.Receipt); receipt != nil {
			pnr = receipt.BookingRef
		}
		s.deliverTicket(ctx, order, pnr, nil)
	}
}

func decodeReceipt(raw []byte) *TicketReceipt {
	if len(raw) == 0 {
		return nil
	}
	var r TicketReceipt
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil
	}
	return &r
}
