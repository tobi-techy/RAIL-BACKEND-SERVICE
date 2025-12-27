package webhooks

import (
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/rail-service/rail_service/pkg/retry"
	"go.uber.org/zap"
)

// WebhookHandlers handles webhook processing
type WebhookHandlers struct {
	fundingService   *funding.Service
	investingService *investing.Service
	validator        *validator.Validate
	webhookSecret    string
	logger           *logger.Logger
}

// NewWebhookHandlers creates a new WebhookHandlers instance
func NewWebhookHandlers(
	fundingService *funding.Service,
	investingService *investing.Service,
	logger *logger.Logger,
) *WebhookHandlers {
	return &WebhookHandlers{
		fundingService:   fundingService,
		investingService: investingService,
		validator:        validator.New(),
		logger:           logger,
	}
}

// SetWebhookSecret sets the webhook secret for signature verification
func (h *WebhookHandlers) SetWebhookSecret(secret string) {
	h.webhookSecret = secret
}

// ChainDepositWebhook handles POST /api/v1/webhooks/chain-deposit
func (h *WebhookHandlers) ChainDepositWebhook(c *gin.Context) {
	rawBody, err := c.GetRawData()
	if err != nil {
		common.RespondBadRequest(c, "Failed to read request body", nil)
		return
	}

	if err := h.verifySignature(c, rawBody); err != nil {
		h.logger.Warn("Webhook signature verification failed", zap.Error(err))
		common.SendUnauthorized(c, "Webhook signature verification failed")
		return
	}

	var webhook entities.ChainDepositWebhook
	if err := json.Unmarshal(rawBody, &webhook); err != nil {
		common.RespondBadRequest(c, "Invalid webhook payload", map[string]interface{}{"error": err.Error()})
		return
	}

	if err := h.validateDepositWebhook(&webhook); err != nil {
		common.SendBadRequest(c, "INVALID_WEBHOOK", err.Error())
		return
	}

	// Process webhook with retry logic for resilience
	retryConfig := retry.RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   500 * time.Millisecond,
		MaxDelay:    5 * time.Second,
		Multiplier:  2.0,
	}

	err = retry.WithExponentialBackoff(
		c.Request.Context(),
		retryConfig,
		func() error {
			return h.fundingService.ProcessChainDeposit(c.Request.Context(), &webhook)
		},
		isWebhookRetryableError,
	)

	if err != nil {
		h.logger.Error("Failed to process chain deposit webhook after retries",
			"error", err,
			"tx_hash", webhook.TxHash,
			"amount", webhook.Amount,
			"chain", webhook.Chain)

		if strings.Contains(err.Error(), "already processed") {
			h.logger.Info("Webhook already processed (idempotent)", "tx_hash", webhook.TxHash)
			common.SendSuccess(c, gin.H{"status": "already_processed"})
			return
		}

		common.SendInternalError(c, common.ErrCodeWebhookFailed, "Failed to process deposit webhook")
		return
	}

	h.logger.Info("Webhook processed successfully",
		"tx_hash", webhook.TxHash,
		"amount", webhook.Amount,
		"chain", webhook.Chain)

	common.SendSuccess(c, gin.H{"status": "processed"})
}

// BrokerageFillWebhook handles POST /api/v1/webhooks/brokerage-fill
func (h *WebhookHandlers) BrokerageFillWebhook(c *gin.Context) {
	rawBody, err := c.GetRawData()
	if err != nil {
		common.RespondBadRequest(c, "Failed to read request body", nil)
		return
	}

	if err := h.verifySignature(c, rawBody); err != nil {
		h.logger.Warn("Webhook signature verification failed", zap.Error(err))
		common.SendUnauthorized(c, "Webhook signature verification failed")
		return
	}

	var webhook entities.BrokerageFillWebhook
	if err := c.ShouldBindJSON(&webhook); err != nil {
		common.RespondBadRequest(c, "Invalid webhook payload", map[string]interface{}{"error": err.Error()})
		return
	}

	if err := h.investingService.ProcessBrokerageFill(c.Request.Context(), &webhook); err != nil {
		h.logger.Error("Failed to process brokerage fill webhook",
			"error", err,
			"order_id", webhook.OrderID)
		common.SendInternalError(c, common.ErrCodeWebhookFailed, "Failed to process fill webhook")
		return
	}

	common.SendSuccess(c, gin.H{"status": "processed"})
}

// TransferWebhook handles POST /api/v1/webhooks/transfer
func (h *WebhookHandlers) TransferWebhook(c *gin.Context) {
	rawBody, err := c.GetRawData()
	if err != nil {
		common.RespondBadRequest(c, "Failed to read request body", nil)
		return
	}

	if err := h.verifySignature(c, rawBody); err != nil {
		h.logger.Warn("Webhook signature verification failed", zap.Error(err))
		common.SendUnauthorized(c, "Webhook signature verification failed")
		return
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		common.RespondBadRequest(c, "Invalid webhook payload", map[string]interface{}{"error": err.Error()})
		return
	}

	h.logger.Info("Transfer webhook received",
		"payload", payload)

	// Process transfer webhook based on type
	transferType, ok := payload["type"].(string)
	if !ok {
		common.SendBadRequest(c, "INVALID_WEBHOOK", "Missing transfer type")
		return
	}

	switch transferType {
	case "incoming":
		h.logger.Info("Processing incoming transfer webhook")
	case "outgoing":
		h.logger.Info("Processing outgoing transfer webhook")
	default:
		h.logger.Warn("Unknown transfer type", "type", transferType)
	}

	common.SendSuccess(c, gin.H{"status": "processed"})
}

// AccountWebhook handles POST /api/v1/webhooks/account
func (h *WebhookHandlers) AccountWebhook(c *gin.Context) {
	rawBody, err := c.GetRawData()
	if err != nil {
		common.RespondBadRequest(c, "Failed to read request body", nil)
		return
	}

	if err := h.verifySignature(c, rawBody); err != nil {
		h.logger.Warn("Webhook signature verification failed", zap.Error(err))
		common.SendUnauthorized(c, "Webhook signature verification failed")
		return
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		common.RespondBadRequest(c, "Invalid webhook payload", map[string]interface{}{"error": err.Error()})
		return
	}

	h.logger.Info("Account webhook received",
		"payload", payload)

	common.SendSuccess(c, gin.H{"status": "processed"})
}

// CircleWebhook handles POST /api/v1/webhooks/circle
func (h *WebhookHandlers) CircleWebhook(c *gin.Context) {
	rawBody, err := c.GetRawData()
	if err != nil {
		common.RespondBadRequest(c, "Failed to read request body", nil)
		return
	}

	// Circle uses different signature headers
	signature := c.GetHeader("X-Circle-Signature")
	if signature == "" {
		signature = c.GetHeader("X-Webhook-Signature")
	}

	if h.webhookSecret != "" && signature != "" {
		if err := verifyHMACSignature(rawBody, signature, h.webhookSecret); err != nil {
			h.logger.Warn("Circle webhook signature verification failed", zap.Error(err))
			common.SendUnauthorized(c, "Webhook signature verification failed")
			return
		}
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		common.RespondBadRequest(c, "Invalid webhook payload", map[string]interface{}{"error": err.Error()})
		return
	}

	h.logger.Info("Circle webhook received",
		"payload_type", payload["type"])

	// Route to appropriate handler based on notification type
	if notificationType, ok := payload["notificationType"].(string); ok {
		switch notificationType {
		case "transfers":
			h.logger.Info("Processing Circle transfer notification")
		case "wallets":
			h.logger.Info("Processing Circle wallet notification")
		case "payments":
			h.logger.Info("Processing Circle payment notification")
		default:
			h.logger.Debug("Unknown Circle notification type", "type", notificationType)
		}
	}

	common.SendSuccess(c, gin.H{"status": "processed"})
}

// Helper methods

func (h *WebhookHandlers) verifySignature(c *gin.Context, rawBody []byte) error {
	if h.webhookSecret == "" {
		return nil // No secret configured, skip verification
	}

	signature := c.GetHeader("X-Webhook-Signature")
	if signature == "" {
		signature = c.GetHeader("X-Hub-Signature-256")
	}

	return verifyHMACSignature(rawBody, signature, h.webhookSecret)
}

func (h *WebhookHandlers) validateDepositWebhook(webhook *entities.ChainDepositWebhook) error {
	if webhook.TxHash == "" {
		return fmt.Errorf("missing transaction hash")
	}

	if webhook.Amount == "" || webhook.Amount == "0" {
		return fmt.Errorf("invalid amount")
	}

	return nil
}

// verifyHMACSignature verifies HMAC-SHA256 webhook signature
func verifyHMACSignature(payload []byte, signature, secret string) error {
	if signature == "" {
		return fmt.Errorf("missing webhook signature")
	}

	// Remove common prefixes
	signature = strings.TrimPrefix(signature, "sha256=")
	signature = strings.TrimPrefix(signature, "hmac-sha256=")

	// Calculate expected signature
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))

	// Constant-time comparison to prevent timing attacks
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return fmt.Errorf("signature mismatch")
	}

	return nil
}

// isWebhookRetryableError determines if a webhook processing error should be retried
func isWebhookRetryableError(err error) bool {
	if err == nil {
		return false
	}

	errorMsg := err.Error()

	// Don't retry client errors or validation errors
	nonRetryableErrors := []string{
		"invalid",
		"malformed",
		"already processed",
		"duplicate",
		"not found",
		"unauthorized",
	}

	for _, msg := range nonRetryableErrors {
		if strings.Contains(strings.ToLower(errorMsg), msg) {
			return false
		}
	}

	// Retry on temporary failures
	retryableErrors := []string{
		"timeout",
		"connection",
		"temporary",
		"unavailable",
		"deadline exceeded",
		"network",
	}

	for _, msg := range retryableErrors {
		if strings.Contains(strings.ToLower(errorMsg), msg) {
			return true
		}
	}

	// By default, retry server errors
	return true
}
