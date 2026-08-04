package travel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
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
//   - user-requested refunds (refund_status='requested') are polled until the
//     intent settles, then the hold is credited back and the user is notified.
func (s *Service) RunRecovery(ctx context.Context) {
	const (
		abandonAge   = 15 * time.Minute
		ticketingAge = 3 * time.Minute
	)
	s.reconcileStuck(ctx, abandonAge)
	s.finalizeTicketed(ctx, ticketingAge)
	s.redeliverTickets(ctx)
	s.reconcileRefunds(ctx)
}

// neverSettledAge is how long a 'booked' order may sit without its intent
// reaching a terminal state before the hold is released. Booking normally
// settles in minutes; anything still active after this is treated as stuck.
const neverSettledAge = 2 * time.Hour

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
		reason := refundReason(intent)
		_ = s.reverseHold(ctx, order.UserID, order.ID, order.HoldAmount, "refunded")
		_ = s.markRefunded(ctx, order.ID, reason)
		s.notifyRefundResolved(ctx, order, reason)
		s.logger.Info("travel recovery: refunded stuck order", zap.String("order_id", order.ID.String()))
	default:
		// Still active past abandonAge with funds held on the user: release.
		_ = s.reverseHold(ctx, order.UserID, order.ID, order.HoldAmount, "abandoned_recovery")
		s.notifyHoldReleased(ctx, order, "the booking never went through")
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
			// Still active long past the normal ticketing window with a hold on
			// the user: the booking never settled, so release the funds. A short
			// wait avoids racing a booking that is merely slow.
			if time.Since(order.CreatedAt) > neverSettledAge {
				_ = s.reverseHold(ctx, order.UserID, order.ID, order.HoldAmount, "booking_never_settled")
				s.notifyHoldReleased(ctx, order, "the airline never confirmed this booking")
				s.logger.Warn("travel recovery: reversed booking that never settled", zap.String("order_id", order.ID.String()))
			}
			continue
		}
		if intent.Status == brij.StatusBooked {
			s.finalizeBooking(ctx, order.UserID, order, intent)
			s.logger.Info("travel recovery: finalized ticketed booking", zap.String("order_id", order.ID.String()))
		} else {
			reason := refundReason(intent)
			_ = s.reverseHold(ctx, order.UserID, order.ID, order.HoldAmount, "refunded")
			_ = s.markRefunded(ctx, order.ID, reason)
			s.notifyRefundResolved(ctx, order, reason)
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

// reconcileRefunds polls user-requested refunds (refund_status='requested')
// until their BRIJ intent settles. A refunded intent releases the user's hold
// and notifies them; a still-booked intent means the review is ongoing.
func (s *Service) reconcileRefunds(ctx context.Context) {
	if s.client == nil || s.ledger == nil {
		return
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM travel_orders
		WHERE refund_status='requested' AND deposit_id IS NULL
		ORDER BY updated_at ASC LIMIT 25`)
	if err != nil {
		s.logger.Error("travel recovery: refund query failed", zap.Error(err))
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
		if strings.TrimSpace(order.IntentID) == "" {
			continue
		}
		intent, err := s.client.GetIntent(ctx, order.IntentID)
		if err != nil {
			s.logger.Warn("travel recovery: refund intent lookup failed, will retry", zap.Error(err), zap.String("order_id", order.ID.String()))
			continue
		}
		if intent.Status != brij.StatusRefunded {
			continue // still under review
		}
		s.settleRefundedOrder(ctx, order, refundReason(intent))
	}
}

// settleRefundedOrder releases a refunded booking's hold and records the
// outcome. Completed bookings are credited via creditRefund (they were paid);
// held/booked orders reverse the still-open hold.
func (s *Service) settleRefundedOrder(ctx context.Context, order *orderRow, reason string) {
	if order.Status == StatusCompleted {
		if err := s.creditRefund(ctx, order, reason); err != nil {
			s.logger.Error("travel recovery: failed to credit refund", zap.Error(err), zap.String("order_id", order.ID.String()))
		}
		return
	}
	_ = s.reverseHold(ctx, order.UserID, order.ID, order.HoldAmount, "refunded")
	_ = s.markRefunded(ctx, order.ID, reason)
	s.notifyRefundResolved(ctx, order, reason)
	s.logger.Info("travel recovery: refunded order settled", zap.String("order_id", order.ID.String()))
}

// creditRefund posts the refund back to the user's Spend balance for a
// completed (paid) booking and records it. Claimed via deposit_id so only one
// worker credits; the ledger reversal is keyed on the order id so a double
// claim can never double-credit.
func (s *Service) creditRefund(ctx context.Context, order *orderRow, reason string) error {
	if s.ledger == nil {
		return fmt.Errorf("ledger unavailable for refund credit")
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE travel_orders SET status='refunded', refund_status='approved', refund_reason=$2,
		       refunded_at=NOW(), refund_credited_at=NOW(), deposit_id=gen_random_uuid(), updated_at=NOW()
		WHERE id=$1 AND status='completed' AND deposit_id IS NULL`, order.ID, reason)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // already claimed or not credit-eligible
	}
	if err := s.ledger.ReverseTransaction(ctx, order.UserID, entities.AccountTypeSpendingBalance,
		order.ID.String(), order.HoldAmount, map[string]interface{}{
			"provider": "brij", "type": "travel_refund_credited", "order_id": order.ID.String(),
		}); err != nil {
		s.logger.Error("CRITICAL: failed to credit travel refund", zap.Error(err), zap.String("order_id", order.ID.String()))
		if _, unclaimErr := s.db.ExecContext(ctx, `
			UPDATE travel_orders SET status='completed', refund_status='requested', refund_reason=NULL,
			       refunded_at=NULL, refund_credited_at=NULL, deposit_id=NULL, updated_at=NOW()
			WHERE id=$1 AND status='refunded'`, order.ID); unclaimErr != nil {
			return fmt.Errorf("refund credit failed: %w; unclaim also failed: %v", err, unclaimErr)
		}
		return err
	}
	s.notifyRefundResolved(ctx, order, reason)
	return nil
}

// refundReason prefers the airline-provided refund reason.
func refundReason(intent *brij.BookingIntent) string {
	if intent != nil {
		if r := strings.TrimSpace(intent.RefundReason); r != "" {
			return r
		}
	}
	return "the airline refunded this booking"
}

// notifyRefundResolved tells the user a refund landed in their Spend balance.
func (s *Service) notifyRefundResolved(ctx context.Context, order *orderRow, reason string) {
	if s.deliverer == nil {
		return
	}
	if err := s.deliverer.SendMessage(ctx, order.UserID, refundResolvedMessage(order, reason)); err != nil {
		s.logger.Warn("failed to notify refund resolution", zap.Error(err), zap.String("order_id", order.ID.String()))
	}
}

// notifyHoldReleased tells the user a hold was released because a booking did
// not go through.
func (s *Service) notifyHoldReleased(ctx context.Context, order *orderRow, reason string) {
	if s.deliverer == nil {
		return
	}
	if err := s.deliverer.SendMessage(ctx, order.UserID, holdReleasedMessage(order, reason)); err != nil {
		s.logger.Warn("failed to notify hold release", zap.Error(err), zap.String("order_id", order.ID.String()))
	}
}

// refundResolvedMessage renders the refund confirmation sent to the user.
func refundResolvedMessage(order *orderRow, reason string) string {
	route := strings.TrimSpace(order.Route)
	if route == "" {
		route = "your flight"
	}
	msg := fmt.Sprintf("Good news — your refund for %s is complete: $%s has been returned to your Spend balance.",
		route, order.HoldAmount.StringFixed(2))
	if strings.TrimSpace(reason) != "" {
		msg += " " + strings.TrimSpace(reason) + "."
	}
	return msg
}

// holdReleasedMessage renders the released-hold notice sent to the user.
func holdReleasedMessage(order *orderRow, reason string) string {
	route := strings.TrimSpace(order.Route)
	if route == "" {
		route = "your flight"
	}
	msg := fmt.Sprintf("The hold for %s ($%s) was released back to your Spend balance.",
		route, order.HoldAmount.StringFixed(2))
	if strings.TrimSpace(reason) != "" {
		msg += " " + strings.TrimSpace(reason) + "."
	}
	return msg
}
