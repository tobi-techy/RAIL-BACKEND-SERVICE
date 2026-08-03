package billpay

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/airbills"
	"go.uber.org/zap"
)

// HandleCallback verifies and processes an Airbills fulfillment callback. The
// raw body and signature come straight from the HTTP handler. A configured
// webhook secret is required; deliveries are de-duplicated so retries are cheap
// no-ops. On a success status the matching order is marked completed; a terminal
// failure marks it failed for reconciliation (funds already left the wallet, so
// the hold is never reversed here).
func (s *Service) HandleCallback(ctx context.Context, body []byte, signature string) error {
	if err := airbills.VerifyWebhookSignature(body, signature, s.WebhookSecret()); err != nil {
		return fmt.Errorf("airbills callback signature: %w", err)
	}

	var event airbills.CallbackEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return fmt.Errorf("airbills callback decode: %w", err)
	}
	if event.ID == "" {
		return fmt.Errorf("airbills callback missing transaction id")
	}

	deliveryID := event.ID + ":" + event.Status
	if deliveryID == ":" {
		sum := sha256.Sum256(body)
		deliveryID = hex.EncodeToString(sum[:])
	}
	var inserted bool
	if err := s.db.QueryRowContext(ctx, `
		INSERT INTO airbills_webhook_deliveries (delivery_id, product_code, airbills_id)
		VALUES ($1,$2,$3) ON CONFLICT (delivery_id) DO NOTHING
		RETURNING true`, deliveryID, event.ProductCode, event.ID).Scan(&inserted); err != nil {
		// No row returned means we've already processed this delivery.
		s.logger.Debug("airbills callback already processed", zap.String("delivery_id", deliveryID))
		return nil
	}

	if _, err := s.db.ExecContext(ctx, `
		UPDATE airbills_orders SET last_callback_status=$1, last_callback_at=NOW(), updated_at=NOW()
		WHERE airbills_id=$2`, event.Status, event.ID); err != nil {
		s.logger.Warn("airbills callback: failed to record status", zap.Error(err), zap.String("airbills_id", event.ID))
	}

	order, err := s.lookupOrderByAirbillsID(ctx, event.ID)
	if err != nil && err != sql.ErrNoRows {
		s.logger.Warn("airbills callback: failed to lookup order", zap.Error(err), zap.String("airbills_id", event.ID))
	}

	switch {
	case callbackSucceeded(event.Status):
		res, err := s.db.ExecContext(ctx, `
			UPDATE airbills_orders SET status='completed', updated_at=NOW()
			WHERE airbills_id=$1 AND status IN ('sent','processing','held')`, event.ID)
		if err != nil {
			return fmt.Errorf("airbills callback complete: %w", err)
		}
		rows, _ := res.RowsAffected()
		if rows > 0 {
			s.logger.Info("airbills bill fulfilled via callback", zap.String("airbills_id", event.ID))
			if order != nil {
				s.notifyBillStatus(ctx, order, "completed")
			}
		} else {
			s.logger.Debug("airbills callback completion ignored: already terminal", zap.String("airbills_id", event.ID))
		}
	case callbackFailed(event.Status):
		rows := s.markFailed(ctx, event.ID, "callback_failed")
		if rows > 0 {
			s.logger.Warn("airbills callback reported failure — flagged for reconciliation", zap.String("airbills_id", event.ID))
			if order != nil {
				s.notifyBillStatus(ctx, order, "failed")
			}
		} else {
			s.logger.Debug("airbills callback failure ignored: already terminal", zap.String("airbills_id", event.ID))
		}
	}
	return nil
}

// callbackOrder holds the fields needed for a user-facing callback notification.
type callbackOrder struct {
	userID        uuid.UUID
	category      string
	recipient     string
	amountNGN     float64
	amountUSDC    string
	beneficiaryID *uuid.UUID
}

func (s *Service) lookupOrderByAirbillsID(ctx context.Context, airbillsID string) (*callbackOrder, error) {
	var o callbackOrder
	var usdc sql.NullString
	var beneficiaryID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id, product_category, recipient, amount_ngn, amount_in_token, beneficiary_id
		FROM airbills_orders WHERE airbills_id=$1`, airbillsID).Scan(
		&o.userID, &o.category, &o.recipient, &o.amountNGN, &usdc, &beneficiaryID)
	if err != nil {
		return nil, err
	}
	o.amountUSDC = usdc.String
	if beneficiaryID.Valid {
		if id, err := uuid.Parse(beneficiaryID.String); err == nil {
			o.beneficiaryID = &id
		}
	}
	return &o, nil
}

func (s *Service) notifyBillStatus(ctx context.Context, order *callbackOrder, status string) {
	if s.notifier == nil {
		return
	}
	var title, body string
	data := map[string]interface{}{
		"type":       "airbills_bill_status",
		"category":   order.category,
		"recipient":  order.recipient,
		"amount_ngn": order.amountNGN,
		"status":     status,
	}
	if order.beneficiaryID != nil {
		data["beneficiary_id"] = order.beneficiaryID.String()
	}
	cat := strings.ToLower(order.category)
	switch status {
	case "completed":
		title = fmt.Sprintf("%s paid", titleCase(cat))
		body = fmt.Sprintf("₦%.0f %s for %s is complete.", order.amountNGN, cat, order.recipient)
		if order.amountUSDC != "" {
			body = fmt.Sprintf("₦%.0f %s for %s is complete (%s USDC).", order.amountNGN, cat, order.recipient, order.amountUSDC)
		}
	case "failed":
		title = fmt.Sprintf("%s payment failed", titleCase(cat))
		body = fmt.Sprintf("₦%.0f %s for %s failed. We'll help you retry.", order.amountNGN, cat, order.recipient)
	default:
		return
	}
	if err := s.notifier.SendPush(ctx, order.userID, title, body, data); err != nil {
		s.logger.Warn("airbills callback: failed to send push notification", zap.Error(err), zap.String("recipient", order.recipient))
	}
}

// titleCase capitalises the first letter of a string (replaces deprecated strings.Title).
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func callbackSucceeded(status string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	switch s {
	case airbills.StatusSuccess, airbills.StatusAlreadyProcessed, "success", "successful", "completed", "delivered", "paid":
		return true
	default:
		return false
	}
}

func callbackFailed(status string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	switch s {
	case "failed", "failure", "error", "declined", "reversed", "cancelled":
		return true
	default:
		return false
	}
}
