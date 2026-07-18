package billpay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

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

	switch {
	case callbackSucceeded(event.Status):
		if _, err := s.db.ExecContext(ctx, `
			UPDATE airbills_orders SET status='completed', updated_at=NOW()
			WHERE airbills_id=$1 AND status IN ('sent','processing','held')`, event.ID); err != nil {
			return fmt.Errorf("airbills callback complete: %w", err)
		}
		s.logger.Info("airbills bill fulfilled via callback", zap.String("airbills_id", event.ID))
	case callbackFailed(event.Status):
		s.markFailed(ctx, event.ID, "callback_failed")
		s.logger.Warn("airbills callback reported failure — flagged for reconciliation", zap.String("airbills_id", event.ID))
	}
	return nil
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
