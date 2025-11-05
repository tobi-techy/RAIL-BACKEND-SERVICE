package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/stack-service/stack_service/internal/adapters/due"
	"github.com/stack-service/stack_service/internal/domain/services"
	"github.com/stack-service/stack_service/pkg/logger"
	"github.com/stack-service/stack_service/pkg/webhook"
)

// DueWebhookHandler handles Due API webhook events
type DueWebhookHandler struct {
	offRampService *services.OffRampService
	logger         *logger.Logger
}

// NewDueWebhookHandler creates a new Due webhook handler
func NewDueWebhookHandler(offRampService *services.OffRampService, logger *logger.Logger) *DueWebhookHandler {
	return &DueWebhookHandler{
		offRampService: offRampService,
		logger:         logger,
	}
}

// HandleDepositEvent handles virtual account deposit events from Due
func (h *DueWebhookHandler) HandleDepositEvent(c *gin.Context) {
	h.logger.Info("Received Due webhook event")

	// Read raw body for signature validation
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.logger.Error("Failed to read request body", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// Validate signature (skip in development)
	// if err := h.ValidateWebhookSignature(c, body, "webhook_secret"); err != nil {
	// 	c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
	// 	return
	// }

	var event due.WebhookEvent
	if err := json.Unmarshal(body, &event); err != nil {
		h.logger.Error("Failed to parse webhook payload", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	h.logger.Info("Parsed webhook event", "type", event.Type)

	// Handle different event types
	switch event.Type {
	case "transfer.status_changed":
		h.handleTransferStatusChanged(c, event.Data)
	case "virtual_account.deposit":
		h.handleVirtualAccountDeposit(c, event.Data)
	default:
		h.logger.Warn("Unhandled webhook event type", "type", event.Type)
		c.JSON(http.StatusOK, gin.H{"message": "event type not handled"})
	}
}

// handleTransferStatusChanged processes transfer status change events
func (h *DueWebhookHandler) handleTransferStatusChanged(c *gin.Context, data map[string]interface{}) {
	// Parse transfer data
	transferJSON, err := json.Marshal(data)
	if err != nil {
		h.logger.Error("Failed to marshal transfer data", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	var transfer due.TransferWebhookData
	if err := json.Unmarshal(transferJSON, &transfer); err != nil {
		h.logger.Error("Failed to unmarshal transfer data", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid transfer data"})
		return
	}

	h.logger.Info("Processing transfer status change",
		"transfer_id", transfer.ID,
		"status", transfer.Status)

	// Process transfer completion
	if transfer.Status == due.TransferStatusCompleted || transfer.Status == due.TransferStatusPaymentProcessed {
		ctx := c.Request.Context()
		if err := h.offRampService.HandleTransferCompleted(ctx, transfer.ID); err != nil {
			h.logger.Error("Failed to handle transfer completion",
				"transfer_id", transfer.ID,
				"error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "processing failed"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "event processed"})
}

// handleVirtualAccountDeposit processes virtual account deposit events
func (h *DueWebhookHandler) handleVirtualAccountDeposit(c *gin.Context, data map[string]interface{}) {
	h.logger.Info("Processing virtual account deposit", "data", data)

	// Extract deposit information
	virtualAccountID, ok := data["virtual_account_id"].(string)
	if !ok {
		h.logger.Error("Missing virtual_account_id in deposit event")
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing virtual_account_id"})
		return
	}

	amount, ok := data["amount"].(string)
	if !ok {
		h.logger.Error("Missing amount in deposit event")
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing amount"})
		return
	}

	h.logger.Info("Deposit detected",
		"virtual_account_id", virtualAccountID,
		"amount", amount)

	// Initiate off-ramp process
	ctx := c.Request.Context()
	if err := h.offRampService.InitiateOffRamp(ctx, virtualAccountID, amount); err != nil {
		h.logger.Error("Failed to initiate off-ramp",
			"virtual_account_id", virtualAccountID,
			"error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "off-ramp initiation failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deposit processed"})
}

// ValidateWebhookSignature validates the webhook signature
func (h *DueWebhookHandler) ValidateWebhookSignature(c *gin.Context, payload []byte, secret string) error {
	signature := c.GetHeader("Due-Signature")
	if signature == "" {
		return fmt.Errorf("missing webhook signature")
	}

	if err := webhook.ValidateDueSignature(payload, signature, secret); err != nil {
		h.logger.Error("Invalid webhook signature", "error", err)
		return err
	}

	return nil
}
