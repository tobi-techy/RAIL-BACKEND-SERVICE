package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	alpacaService "github.com/rail-service/rail_service/internal/domain/services/alpaca"
	"github.com/rail-service/rail_service/pkg/logger"
)

// AlpacaWebhookHandlers handles Alpaca webhook events
type AlpacaWebhookHandlers struct {
	eventProcessor *alpacaService.EventProcessor
	logger         *logger.Logger
	webhookSecret  string // For signature verification
	skipVerify     bool   // Allow skipping in development only
	environment    string // Track environment to enforce verification in production
}

func NewAlpacaWebhookHandlers(eventProcessor *alpacaService.EventProcessor, logger *logger.Logger, webhookSecret string, skipVerify bool, environment string) *AlpacaWebhookHandlers {
	// Security fix: Never allow skipping verification in production or staging
	if (strings.EqualFold(environment, "production") || strings.EqualFold(environment, "staging")) && skipVerify {
		logger.Error("SECURITY VIOLATION: Attempted to skip Alpaca webhook verification in production/staging - forcing verification ON")
		skipVerify = false
	}

	if skipVerify {
		logger.Warn("⚠️  INSECURE MODE: Alpaca webhook signature verification is DISABLED",
			"environment", environment,
			"warning", "This should only be used in local development")
	}

	return &AlpacaWebhookHandlers{
		eventProcessor: eventProcessor,
		logger:         logger,
		webhookSecret:  webhookSecret,
		skipVerify:     skipVerify,
		environment:    environment,
	}
}

// HandleTradeUpdate handles trade/order update webhooks from Alpaca
// POST /api/v1/webhooks/alpaca/trade
func (h *AlpacaWebhookHandlers) HandleTradeUpdate(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.logger.Error("Failed to read webhook body", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// Verify webhook signature
	signature := c.GetHeader("Alpaca-Signature")
	if !h.verifySignature(signature, body) {
		h.logger.Error("Invalid Alpaca webhook signature")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	// Parse the event
	var event struct {
		Event string          `json:"event"`
		Order json.RawMessage `json:"order"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		h.logger.Error("Failed to parse webhook", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	// Store event for processing
	eventID := generateStableEventID("trade_update", body)
	if exists, err := h.eventProcessor.EventExists(c.Request.Context(), eventID); err == nil && exists {
		h.logger.Info("Duplicate trade_update webhook, skipping", "event_id", eventID)
		c.JSON(http.StatusOK, gin.H{"status": "received"})
		return
	}
	if err := h.eventProcessor.StoreEvent(c.Request.Context(), "trade_update", eventID, body, nil, nil); err != nil {
		h.logger.Error("Failed to store event", "error", err)
	}

	// Process immediately for order fills
	if event.Event == "fill" || event.Event == "partial_fill" {
		var orderData struct {
			ID             string `json:"id"`
			Symbol         string `json:"symbol"`
			Side           string `json:"side"`
			FilledQty      string `json:"filled_qty"`
			FilledAvgPrice string `json:"filled_avg_price"`
			Status         string `json:"status"`
			FilledAt       string `json:"filled_at"`
		}
		if err := json.Unmarshal(event.Order, &orderData); err == nil {
			fillEvent := &entities.AlpacaOrderFillEvent{
				OrderID: orderData.ID,
				Symbol:  orderData.Symbol,
				Side:    orderData.Side,
				Status:  orderData.Status,
			}
			// Parse decimal values
			if qty, err := common.ParseDecimal(orderData.FilledQty); err == nil {
				fillEvent.FilledQty = qty
			}
			if price, err := common.ParseDecimal(orderData.FilledAvgPrice); err == nil {
				fillEvent.FilledAvgPrice = price
			}
			if t, err := common.ParseTime(orderData.FilledAt); err == nil {
				fillEvent.FilledAt = t
			}

			if err := h.eventProcessor.ProcessOrderFill(c.Request.Context(), fillEvent); err != nil {
				h.logger.Error("Failed to process order fill", "error", err, "order_id", orderData.ID)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "received"})
}

// HandleAccountUpdate handles account status update webhooks
// POST /api/v1/webhooks/alpaca/account
func (h *AlpacaWebhookHandlers) HandleAccountUpdate(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.logger.Error("Failed to read webhook body", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// Verify webhook signature
	signature := c.GetHeader("Alpaca-Signature")
	if !h.verifySignature(signature, body) {
		h.logger.Error("Invalid Alpaca webhook signature")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	var event struct {
		AccountID     string `json:"account_id"`
		AccountNumber string `json:"account_number"`
		Status        string `json:"status"`
		StatusFrom    string `json:"status_from"`
		Reason        string `json:"reason"`
		At            string `json:"at"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		h.logger.Error("Failed to parse account webhook", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	// Store event
	eventID := generateStableEventID("account_update", body)
	if exists, err := h.eventProcessor.EventExists(c.Request.Context(), eventID); err == nil && exists {
		h.logger.Info("Duplicate account_update webhook, skipping", "event_id", eventID)
		c.JSON(http.StatusOK, gin.H{"status": "received"})
		return
	}
	if err := h.eventProcessor.StoreEvent(c.Request.Context(), "account_update", eventID, body, nil, nil); err != nil {
		h.logger.Error("Failed to store event", "error", err)
	}

	// Process account update
	accountEvent := &entities.AlpacaAccountEvent{
		AccountID: event.AccountID,
		Status:    entities.AlpacaAccountStatus(event.Status),
		Reason:    event.Reason,
	}
	if t, err := common.ParseTime(event.At); err == nil {
		accountEvent.UpdatedAt = t
	}

	if err := h.eventProcessor.ProcessAccountUpdate(c.Request.Context(), accountEvent); err != nil {
		h.logger.Error("Failed to process account update", "error", err, "account_id", event.AccountID)
	}

	c.JSON(http.StatusOK, gin.H{"status": "received"})
}

// HandleTransferUpdate handles transfer/funding update webhooks
// POST /api/v1/webhooks/alpaca/transfer
func (h *AlpacaWebhookHandlers) HandleTransferUpdate(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.logger.Error("Failed to read webhook body", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// Verify webhook signature
	signature := c.GetHeader("Alpaca-Signature")
	if !h.verifySignature(signature, body) {
		h.logger.Error("Invalid Alpaca webhook signature")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	// Store event for processing
	eventID := generateStableEventID("transfer_update", body)
	if exists, err := h.eventProcessor.EventExists(c.Request.Context(), eventID); err == nil && exists {
		h.logger.Info("Duplicate transfer_update webhook, skipping", "event_id", eventID)
		c.JSON(http.StatusOK, gin.H{"status": "received"})
		return
	}
	if err := h.eventProcessor.StoreEvent(c.Request.Context(), "transfer_update", eventID, body, nil, nil); err != nil {
		h.logger.Error("Failed to store event", "error", err)
	}

	h.logger.Info("Transfer webhook received")
	c.JSON(http.StatusOK, gin.H{"status": "received"})
}

// HandleNonTradeActivity handles non-trade activity webhooks (dividends, fees, etc.)
// POST /api/v1/webhooks/alpaca/nta
func (h *AlpacaWebhookHandlers) HandleNonTradeActivity(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.logger.Error("Failed to read webhook body", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// Verify webhook signature
	signature := c.GetHeader("Alpaca-Signature")
	if !h.verifySignature(signature, body) {
		h.logger.Error("Invalid Alpaca webhook signature")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	// Store event for processing
	eventID := generateStableEventID("nta", body)
	if exists, err := h.eventProcessor.EventExists(c.Request.Context(), eventID); err == nil && exists {
		h.logger.Info("Duplicate nta webhook, skipping", "event_id", eventID)
		c.JSON(http.StatusOK, gin.H{"status": "received"})
		return
	}
	if err := h.eventProcessor.StoreEvent(c.Request.Context(), "nta", eventID, body, nil, nil); err != nil {
		h.logger.Error("Failed to store event", "error", err)
	}

	h.logger.Info("NTA webhook received")
	c.JSON(http.StatusOK, gin.H{"status": "received"})
}

// verifySignature verifies Alpaca webhook signature using HMAC-SHA256
// Alpaca uses HMAC-SHA256 with the webhook secret
func (h *AlpacaWebhookHandlers) verifySignature(signature string, body []byte) bool {
	// Skip verification in dev mode if secret is not configured
	if h.webhookSecret == "" {
		if h.skipVerify {
			h.logger.Warn("Alpaca webhook secret not configured - SKIPPING VERIFICATION (development mode)")
			return true
		}
		h.logger.Error("Alpaca webhook secret not configured - rejecting webhook for security")
		return false
	}

	// Alpaca uses HMAC-SHA256 for webhook signatures
	mac := hmac.New(sha256.New, []byte(h.webhookSecret))
	mac.Write(body)
	expectedSignature := hex.EncodeToString(mac.Sum(nil))

	// Use constant-time comparison to prevent timing attacks
	if subtle.ConstantTimeCompare([]byte(expectedSignature), []byte(signature)) != 1 {
		h.logger.Warn("Webhook signature verification failed",
			"expected_prefix", expectedSignature[:16]+"...",
			"received_prefix", safeTruncate(signature, 16)+"...")
		return false
	}

	return true
}

// safeTruncate safely truncates a string to max length
func safeTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// generateStableEventID creates a deterministic event ID from the event type and payload body.
// Retried webhooks with identical payloads produce the same ID, enabling deduplication.
func generateStableEventID(eventType string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(eventType))
	h.Write([]byte(":"))
	h.Write(body)
	return fmt.Sprintf("alpaca_%s", hex.EncodeToString(h.Sum(nil))[:32])
}
