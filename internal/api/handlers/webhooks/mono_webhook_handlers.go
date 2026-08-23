package webhooks

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	monosvc "github.com/rail-service/rail_service/internal/domain/services/mono"
	"go.uber.org/zap"
)

// MonoWebhookHandler handles inbound Mono webhooks for account status changes
// (reauthorisation, unlinking) and payment events.
type MonoWebhookHandler struct {
	service       *monosvc.Service
	webhookSecret string
	logger        *zap.Logger
}

func NewMonoWebhookHandler(service *monosvc.Service, webhookSecret string, logger *zap.Logger) *MonoWebhookHandler {
	return &MonoWebhookHandler{service: service, webhookSecret: webhookSecret, logger: logger}
}

// monoWebhookPayload is the expected body from Mono webhook deliveries.
type monoWebhookPayload struct {
	Event     string `json:"event"`      // e.g. "account_reauthorized", "account_unlinked"
	Account   string `json:"account"`    // Mono account ID
	AccountID string `json:"account_id"` // alternative field name
	Reference string `json:"reference"`  // payment reference for payment events
	Data      struct {
		Account   string `json:"account"`
		Reference string `json:"reference"`
	} `json:"data"`
}

// HandleWebhook processes inbound Mono webhook events.
// POST /webhooks/mono
func (h *MonoWebhookHandler) HandleWebhook(c *gin.Context) {
	rawBody, err := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBodySize))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	// Reject webhooks when no secret is configured — never process
	// unauthenticated payloads.
	if h.webhookSecret == "" {
		h.logger.Error("Mono webhook secret not configured; rejecting webhook")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "webhook verification not configured"})
		return
	}

	// Mono sends the dashboard-configured secret in the mono-webhook-secret
	// header. Compare in constant time to prevent timing oracles.
	provided := c.GetHeader("mono-webhook-secret")
	if subtle.ConstantTimeCompare([]byte(provided), []byte(h.webhookSecret)) != 1 {
		h.logger.Warn("Invalid Mono webhook signature")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	var payload monoWebhookPayload
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		h.logger.Error("Failed to parse Mono webhook", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	event := payload.Event
	if event == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing event type"})
		return
	}

	// Extract the Mono account ID — try multiple field names.
	monoAccountID := payload.Account
	if monoAccountID == "" {
		monoAccountID = payload.AccountID
	}
	if monoAccountID == "" {
		monoAccountID = payload.Data.Account
	}

	// For payment events, extract the reference.
	paymentRef := payload.Reference
	if paymentRef == "" {
		paymentRef = payload.Data.Reference
	}

	h.logger.Info("Mono webhook received",
		zap.String("event", event),
		zap.String("mono_account_id", monoAccountID),
		zap.String("reference", paymentRef))

	ctx := c.Request.Context()

	switch event {
	case "account_reauthorized":
		if err := h.service.HandleWebhook(ctx, event, monoAccountID); err != nil {
			h.logger.Error("Failed to handle Mono reauth webhook", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "processing failed"})
			return
		}

	case "account_unlinked":
		if err := h.service.HandleWebhook(ctx, event, monoAccountID); err != nil {
			h.logger.Error("Failed to handle Mono unlink webhook", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "processing failed"})
			return
		}

	case "payment_success":
		if paymentRef != "" {
			// Webhook is the trusted system caller (secret-verified above), so
			// use the reference-only variant — there is no user context here.
			if _, err := h.service.VerifyDepositByReference(ctx, paymentRef); err != nil {
				h.logger.Error("Failed to verify Mono payment on webhook", zap.Error(err), zap.String("reference", paymentRef))
			}
		}

	default:
		h.logger.Debug("Unhandled Mono webhook event", zap.String("event", event))
	}

	c.JSON(http.StatusOK, gin.H{"status": "processed"})
}
