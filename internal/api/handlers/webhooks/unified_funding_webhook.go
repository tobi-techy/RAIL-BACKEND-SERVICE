package webhooks

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// UnifiedFundingWebhookHandler routes funding webhooks from multiple sources
// POST /webhooks/funding
type UnifiedFundingWebhookHandler struct {
	bridgeHandler             *BridgeWebhookHandler
	circleHandler             *CircleWebhookHandler
	alpacaHandler             *AlpacaWebhookHandlers
	logger                    *zap.Logger
	allowInsecureVerification bool
}

// NewUnifiedFundingWebhookHandler creates a unified webhook handler
func NewUnifiedFundingWebhookHandler(
	bridgeHandler *BridgeWebhookHandler,
	circleHandler *CircleWebhookHandler,
	alpacaHandler *AlpacaWebhookHandlers,
	logger *zap.Logger,
	allowInsecureVerification bool,
) *UnifiedFundingWebhookHandler {
	return &UnifiedFundingWebhookHandler{
		bridgeHandler:             bridgeHandler,
		circleHandler:             circleHandler,
		alpacaHandler:             alpacaHandler,
		logger:                    logger,
		allowInsecureVerification: allowInsecureVerification,
	}
}

// SetWebhookSecret sets the webhook secret for a source
func (h *UnifiedFundingWebhookHandler) SetWebhookSecret(source, secret string) {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case string(WebhookSourceBridge):
		if h.bridgeHandler != nil {
			h.bridgeHandler.webhookSecret = secret
		}
	case string(WebhookSourceAlpaca):
		if h.alpacaHandler != nil {
			h.alpacaHandler.webhookSecret = secret
		}
	}
}

// WebhookSource identifies the source of a webhook
type WebhookSource string

const (
	WebhookSourceBridge WebhookSource = "bridge"
	WebhookSourceCircle WebhookSource = "circle"
	WebhookSourceAlpaca WebhookSource = "alpaca"
)

// HandleFundingWebhook routes webhooks based on source header or payload detection
// POST /webhooks/funding
func (h *UnifiedFundingWebhookHandler) HandleFundingWebhook(c *gin.Context) {
	// Read raw body
	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.logger.Error("Failed to read webhook body", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	// Detect source from headers or payload
	source := h.detectSource(c, rawBody)
	if source == "" {
		h.logger.Warn("Unknown webhook source", zap.String("headers", c.Request.Header.Get("X-Webhook-Source")))
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown webhook source"})
		return
	}

	h.logger.Info("Routing funding webhook",
		zap.String("source", string(source)),
		zap.Int("body_size", len(rawBody)))

	// Verify signature based on source
	if !h.verifySignature(c, source, rawBody) {
		h.logger.Warn("Invalid webhook signature", zap.String("source", string(source)))
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	// Route to appropriate handler
	switch source {
	case WebhookSourceBridge:
		h.routeToBridge(c, rawBody)
	case WebhookSourceCircle:
		h.routeToCircle(c, rawBody)
	case WebhookSourceAlpaca:
		h.routeToAlpaca(c, rawBody)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported source"})
	}
}

// detectSource determines the webhook source from headers or payload
func (h *UnifiedFundingWebhookHandler) detectSource(c *gin.Context, body []byte) WebhookSource {
	// Check explicit source header
	sourceHeader := c.GetHeader("X-Webhook-Source")
	if sourceHeader != "" {
		switch strings.ToLower(sourceHeader) {
		case "bridge":
			return WebhookSourceBridge
		case "circle":
			return WebhookSourceCircle
		case "alpaca":
			return WebhookSourceAlpaca
		}
	}

	// Check provider-specific headers
	if c.GetHeader("X-Bridge-Signature") != "" || c.GetHeader("Bridge-Signature") != "" {
		return WebhookSourceBridge
	}
	if c.GetHeader("X-Circle-Signature") != "" {
		return WebhookSourceCircle
	}
	if c.GetHeader("X-Alpaca-Signature") != "" || c.GetHeader("Alpaca-Signature") != "" {
		return WebhookSourceAlpaca
	}

	// Try to detect from payload structure
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err == nil {
		// Bridge webhooks have event_category
		if _, ok := payload["event_category"]; ok {
			return WebhookSourceBridge
		}
		// Circle webhooks have notificationType
		if _, ok := payload["notificationType"]; ok {
			return WebhookSourceCircle
		}
		// Alpaca webhooks have event field
		if _, ok := payload["event"]; ok {
			return WebhookSourceAlpaca
		}
	}

	return ""
}

// verifySignature verifies the webhook signature based on source
func (h *UnifiedFundingWebhookHandler) verifySignature(c *gin.Context, source WebhookSource, body []byte) bool {
	switch source {
	case WebhookSourceBridge:
		if h.bridgeHandler == nil {
			return false
		}
		signature := c.GetHeader("X-Bridge-Signature")
		if signature == "" {
			signature = c.GetHeader("Bridge-Signature")
		}
		return h.bridgeHandler.verifySignature(signature, body)
	case WebhookSourceCircle:
		if h.circleHandler == nil {
			return false
		}
		// In dev mode, skip signature verification entirely.
		if h.circleHandler.devMode {
			h.logger.Info("Circle webhook signature verification skipped (dev mode)")
			return true
		}
		keyID := c.GetHeader("X-Circle-Key-Id")
		signature := c.GetHeader("X-Circle-Signature")
		if keyID == "" || signature == "" {
			return false
		}
		if strings.TrimSpace(h.circleHandler.circleAPIKey) == "" {
			if h.allowInsecureVerification {
				h.logger.Warn("Circle API key not configured - allowing unified webhook verification bypass in development mode")
				return true
			}
			h.logger.Error("Circle API key not configured - rejecting unified webhook for security")
			return false
		}
		return h.circleHandler.verifySignature(c.Request.Context(), keyID, signature, body)
	case WebhookSourceAlpaca:
		if h.alpacaHandler == nil {
			return false
		}
		signature := c.GetHeader("X-Alpaca-Signature")
		if signature == "" {
			signature = c.GetHeader("Alpaca-Signature")
		}
		return h.alpacaHandler.verifySignature(signature, body)
	default:
		return false
	}
}

// routeToBridge routes to Bridge webhook handler
func (h *UnifiedFundingWebhookHandler) routeToBridge(c *gin.Context, body []byte) {
	if h.bridgeHandler == nil {
		h.logger.Warn("Bridge handler not configured")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "bridge_handler_not_configured"})
		return
	}

	// Parse Bridge payload
	var payload BridgeWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		h.logger.Error("Failed to parse Bridge payload", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	h.logger.Info("Processing Bridge webhook",
		zap.String("event_type", payload.EventType),
		zap.String("event_id", payload.EventID))

	// Route based on event category
	switch payload.EventCategory {
	case "virtual_account":
		h.processBridgeVirtualAccountEvent(c, &payload)
	case "transfer":
		h.processBridgeTransferEvent(c, &payload)
	case "customer":
		h.processBridgeCustomerEvent(c, &payload)
	default:
		h.logger.Info("Unhandled Bridge event category", zap.String("category", payload.EventCategory))
		c.JSON(http.StatusOK, gin.H{"status": "ignored"})
	}
}

// processBridgeVirtualAccountEvent handles Bridge virtual account events
func (h *UnifiedFundingWebhookHandler) processBridgeVirtualAccountEvent(c *gin.Context, payload *BridgeWebhookPayload) {
	if h.bridgeHandler == nil || h.bridgeHandler.service == nil {
		h.logger.Warn("Bridge webhook service not configured")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "bridge_service_not_configured"})
		return
	}

	switch payload.EventType {
	case "deposit.received", "deposit.completed":
		eventData, _ := json.Marshal(payload.EventObject)
		var depositEvent BridgeDepositEvent
		if err := json.Unmarshal(eventData, &depositEvent); err != nil {
			h.logger.Error("Failed to parse deposit event", zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid deposit event"})
			return
		}
		if err := h.bridgeHandler.service.ProcessFiatDeposit(c, &depositEvent); err != nil {
			h.logger.Error("Failed to process deposit", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "bridge_deposit_processing_failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "processed"})
	default:
		c.JSON(http.StatusOK, gin.H{"status": "ignored"})
	}
}

// processBridgeTransferEvent handles Bridge transfer events
func (h *UnifiedFundingWebhookHandler) processBridgeTransferEvent(c *gin.Context, payload *BridgeWebhookPayload) {
	if h.bridgeHandler == nil {
		h.logger.Warn("Bridge handler not configured for transfer event")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "bridge_handler_not_configured"})
		return
	}
	h.bridgeHandler.handleTransferEvent(c, *payload)
}

// processBridgeCustomerEvent handles Bridge customer events
func (h *UnifiedFundingWebhookHandler) processBridgeCustomerEvent(c *gin.Context, payload *BridgeWebhookPayload) {
	if h.bridgeHandler == nil || h.bridgeHandler.service == nil {
		h.logger.Warn("Bridge webhook service not configured")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "bridge_service_not_configured"})
		return
	}

	if payload.EventType == "customer.status_changed" {
		eventData, _ := json.Marshal(payload.EventObject)
		var customerEvent BridgeCustomerEvent
		if err := json.Unmarshal(eventData, &customerEvent); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid customer event"})
			return
		}
		if err := h.bridgeHandler.service.ProcessCustomerStatusChanged(c, customerEvent.ID, customerEvent.Status); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "bridge_customer_processing_failed"})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "processed"})
}

// routeToCircle routes to Circle webhook handler
func (h *UnifiedFundingWebhookHandler) routeToCircle(c *gin.Context, body []byte) {
	if h.circleHandler == nil {
		h.logger.Warn("Circle handler not configured")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "circle_handler_not_configured"})
		return
	}

	var webhook CircleTransferWebhook
	if err := json.Unmarshal(body, &webhook); err != nil {
		h.logger.Error("Failed to parse Circle payload", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	h.logger.Info("Processing Circle webhook",
		zap.String("notification_type", webhook.NotificationType),
		zap.String("transfer_id", webhook.TransferID))

	// Process based on notification type.
	switch {
	case strings.HasPrefix(strings.ToLower(webhook.NotificationType), "transfers.") ||
		strings.HasPrefix(strings.ToLower(webhook.NotificationType), "transactions."):
		ctx := c.Request.Context()
		if err := h.circleHandler.processIncomingTransfer(ctx, &webhook); err != nil {
			h.logger.Error("Failed to process Circle transfer", zap.Error(err))
			// Return 200 to stop Circle retries — Circle enforces a 5-second timeout
			// and will keep retrying on non-2XX responses.
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "received"})
}

// routeToAlpaca routes to Alpaca webhook handler
func (h *UnifiedFundingWebhookHandler) routeToAlpaca(c *gin.Context, body []byte) {
	if h.alpacaHandler == nil {
		h.logger.Warn("Alpaca handler not configured")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "alpaca_handler_not_configured"})
		return
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		h.logger.Error("Failed to parse Alpaca payload", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	eventType, _ := payload["event"].(string)
	h.logger.Info("Processing Alpaca webhook", zap.String("event", eventType))

	// Route based on event type
	// Restore request body because downstream handlers expect to read it.
	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
	switch {
	case strings.HasPrefix(eventType, "trade"):
		h.alpacaHandler.HandleTradeUpdate(c)
	case strings.HasPrefix(eventType, "account"):
		h.alpacaHandler.HandleAccountUpdate(c)
	case strings.HasPrefix(eventType, "transfer"):
		h.alpacaHandler.HandleTransferUpdate(c)
	default:
		c.JSON(http.StatusOK, gin.H{"status": "ignored"})
	}
}
