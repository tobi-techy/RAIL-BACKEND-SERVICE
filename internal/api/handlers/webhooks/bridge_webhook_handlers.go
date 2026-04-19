package webhooks

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// BridgeWebhookService defines operations for processing Bridge events
type BridgeWebhookService interface {
	ProcessFiatDeposit(ctx *gin.Context, event *BridgeDepositEvent) error
	ProcessCryptoDeposit(ctx *gin.Context, transferID string, customerID string, amount decimal.Decimal, chain string) error
	ProcessTransferCompleted(ctx *gin.Context, transferID string) error
	ProcessTransferFailed(ctx *gin.Context, transferID string, status string) error
	ProcessTransferUnderReview(ctx *gin.Context, transferID string) error
	ProcessTransferRefundInFlight(ctx *gin.Context, transferID string) error
	ProcessTransferRefundFailed(ctx *gin.Context, transferID string) error
	ProcessTransferRefunded(ctx *gin.Context, transferID string) error
	ProcessCustomerStatusChanged(ctx *gin.Context, customerID string, status string) error
	// Card transaction methods
	AuthorizeCardAuthorization(ctx *gin.Context, cardID string, amount decimal.Decimal, merchantName, merchantCategory string) (bool, string, error)
	ProcessCardAuthorization(ctx *gin.Context, cardID string, amount decimal.Decimal, merchantName, merchantCategory string) error
	ProcessCardTransaction(ctx *gin.Context, cardID, transID string, amount decimal.Decimal, merchantName, merchantCategory, status string) error
	ProcessCardTransactionDeclined(ctx *gin.Context, cardID, transID, declineReason string) error
	ProcessCardStatusChanged(ctx *gin.Context, cardID, status string) error
}

// WalletWebhookService defines operations for processing wallet-related webhook events
type WalletWebhookService interface {
	SyncWalletStatus(ctx context.Context, bridgeWalletID string, status string) error
}

// BridgeWebhookHandler handles Bridge API webhook notifications
type BridgeWebhookHandler struct {
	service                 BridgeWebhookService
	walletService           WalletWebhookService
	logger                  *zap.Logger
	webhookSecret           string
	skipWebhookVerification bool   // Should ONLY be true in development with explicit config
	environment             string // Track environment to enforce verification in production
}

// NewBridgeWebhookHandler creates a new Bridge webhook handler
// skipWebhookVerification should ONLY be true in development/testing environments with explicit config
// IMPORTANT: In production, verification can NEVER be skipped regardless of this flag
func NewBridgeWebhookHandler(service BridgeWebhookService, walletService WalletWebhookService, logger *zap.Logger, webhookSecret string, skipWebhookVerification bool, environment string) *BridgeWebhookHandler {
	// Security fix: Never allow skipping verification in production
	if strings.EqualFold(environment, "production") && skipWebhookVerification {
		logger.Error("SECURITY VIOLATION: Attempted to skip webhook verification in production - forcing verification ON")
		skipWebhookVerification = false
	}

	// Log warning if verification is being skipped
	if skipWebhookVerification {
		logger.Warn("⚠️  INSECURE MODE: Webhook signature verification is DISABLED",
			zap.String("environment", environment),
			zap.String("warning", "This should only be used in local development"))
	}

	return &BridgeWebhookHandler{
		service:                 service,
		walletService:           walletService,
		logger:                  logger,
		webhookSecret:           webhookSecret,
		skipWebhookVerification: skipWebhookVerification,
		environment:             environment,
	}
}

// SetService updates the webhook service wiring after container initialization.
func (h *BridgeWebhookHandler) SetService(service BridgeWebhookService) {
	h.service = service
}

// BridgeWebhookPayload represents the Bridge webhook payload structure
type BridgeWebhookPayload struct {
	APIVersion        string                 `json:"api_version"`
	EventID           string                 `json:"event_id"`
	EventCategory     string                 `json:"event_category"`
	EventType         string                 `json:"event_type"`
	EventObjectID     string                 `json:"event_object_id"`
	EventObjectStatus string                 `json:"event_object_status"`
	EventObject       map[string]interface{} `json:"event_object"`
	EventCreatedAt    string                 `json:"event_created_at"`
}

// BridgeDepositEvent represents a deposit event from Bridge
type BridgeDepositEvent struct {
	VirtualAccountID string `json:"virtual_account_id"`
	CustomerID       string `json:"customer_id"`
	Amount           string `json:"amount"`
	Currency         string `json:"currency"`
	TransactionRef   string `json:"transaction_ref"`
	Status           string `json:"status"`
}

// BridgeTransferEvent represents a transfer event from Bridge
type BridgeTransferEvent struct {
	ID          string `json:"id"`
	CustomerID  string `json:"customer_id"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	Status      string `json:"status"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

// BridgeCustomerEvent represents a customer status change event
type BridgeCustomerEvent struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Email  string `json:"email"`
}

// HandleRealTimeAuth handles Bridge's synchronous real-time card authorization webhook.
// POST /webhooks/bridge/card-auth
// Bridge calls this endpoint synchronously during a card transaction and expects a
// JSON response with {"approved": true/false} within 500ms. If the endpoint times out,
// Bridge applies the configured fallback mode (default: DECLINE).
// Signature verification uses X-Webhook-Signature header (RSA, same format as standard webhooks).
func (h *BridgeWebhookHandler) HandleRealTimeAuth(c *gin.Context) {
	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot read body"})
		return
	}

	// Verify RSA signature (X-Webhook-Signature: t=<ts>,v0=<sig>)
	sig := c.GetHeader("X-Webhook-Signature")
	if sig == "" {
		h.logger.Warn("Real-time auth webhook missing X-Webhook-Signature")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing signature"})
		return
	}
	if !h.verifySignature(sig, rawBody) {
		h.logger.Warn("Real-time auth webhook signature verification failed")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	var req bridge.RealTimeAuthRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	h.logger.Info("Real-time auth request",
		zap.String("event_id", req.EventID),
		zap.String("authorization_id", req.Data.AuthorizationID),
		zap.String("card_account_id", req.Data.CardAccountID),
		zap.String("amount", req.Data.Amount),
		zap.String("merchant", req.Data.Merchant.Description))

	amount, err := decimal.NewFromString(req.Data.Amount)
	if err != nil || !amount.GreaterThan(decimal.Zero) {
		c.JSON(http.StatusOK, bridge.RealTimeAuthResponse{Approved: false, DecisionReason: "invalid_amount"})
		return
	}

	if h.service == nil {
		h.logger.Error("Real-time auth requested while webhook service is not configured")
		c.JSON(http.StatusOK, bridge.RealTimeAuthResponse{Approved: false, DecisionReason: "service_unavailable"})
		return
	}

	approved, reason, err := h.service.AuthorizeCardAuthorization(
		c,
		req.Data.CardAccountID,
		amount,
		req.Data.Merchant.Description,
		req.Data.Merchant.Category,
	)
	if err != nil {
		h.logger.Error("Real-time auth decision failed",
			zap.String("card_account_id", req.Data.CardAccountID),
			zap.String("authorization_id", req.Data.AuthorizationID),
			zap.Error(err))
		if strings.TrimSpace(reason) == "" {
			reason = "authorization_error"
		}
		c.JSON(http.StatusOK, bridge.RealTimeAuthResponse{Approved: false, DecisionReason: reason})
		return
	}

	c.JSON(http.StatusOK, bridge.RealTimeAuthResponse{Approved: approved, DecisionReason: reason})
}

// HandleWebhook handles all Bridge webhook events
// POST /webhooks/bridge
func (h *BridgeWebhookHandler) HandleWebhook(c *gin.Context) {
	// Read raw body
	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.logger.Error("Failed to read webhook body", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	// Verify signature
	signature := getBridgeSignatureHeader(c)
	if !h.verifySignature(signature, rawBody) {
		h.logger.Warn("Invalid Bridge webhook signature")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid signature"})
		return
	}

	// Parse payload
	var payload BridgeWebhookPayload
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		h.logger.Error("Failed to parse webhook payload", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	h.logger.Info("Received Bridge webhook",
		zap.String("event_id", payload.EventID),
		zap.String("event_type", payload.EventType),
		zap.String("event_category", payload.EventCategory),
		zap.String("event_object_status", payload.EventObjectStatus))

	// Route by event category (new Bridge format) or event type (legacy)
	switch payload.EventCategory {
	// Liquidation Address Drain events (crypto deposits via liquidation addresses)
	case "liquidation_address.drain":
		h.handleLiquidationAddressDrain(c, payload)

	// Virtual Account Activity - fiat deposits
	case "virtual_account.activity":
		h.handleVirtualAccountActivity(c, payload)

	// Transfer events
	case "transfer":
		h.handleTransferEvent(c, payload)

	// Customer events
	case "customer":
		h.handleCustomerEvent(c, payload)

	// KYC Link events
	case "kyc_link":
		h.handleKYCLinkEvent(c, payload)

	// Card Account events
	case "card_account":
		h.handleCardAccountEvent(c, payload)

	// Card Transaction events
	case "card_transaction":
		h.handleCardTransactionEvent(c, payload)

	// Posted Card Transaction events
	case "posted_card_account_transaction":
		h.handlePostedCardTransaction(c, payload)

	// Card Withdrawal events (top-up funding lifecycle)
	case "card_withdrawal":
		h.handleCardWithdrawalEvent(c, payload)

	// Wallet lifecycle events
	case "wallet":
		h.handleWalletEvent(c, payload)

	default:
		// Fallback to legacy event_type routing for backwards compatibility
		h.handleLegacyEventType(c, payload)
	}
}

// handleLiquidationAddressDrain processes liquidation_address.drain events.
// Only payment_processed is the final state where funds have reached the destination.
func (h *BridgeWebhookHandler) handleLiquidationAddressDrain(c *gin.Context, payload BridgeWebhookPayload) {
	state := getStringField(payload.EventObject, "state")
	drainID := getStringField(payload.EventObject, "id")
	customerID := getStringField(payload.EventObject, "customer_id")

	h.logger.Info("Liquidation address drain event",
		zap.String("drain_id", drainID),
		zap.String("state", state),
		zap.String("customer_id", customerID))

	if state != "payment_processed" {
		h.logger.Info("Skipping non-final drain state",
			zap.String("drain_id", drainID),
			zap.String("state", state))
		c.JSON(http.StatusOK, gin.H{"status": "acknowledged"})
		return
	}

	if h.service == nil {
		h.logger.Error("Bridge webhook service is not configured")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service_unavailable"})
		return
	}

	var amount decimal.Decimal
	if amountStr := getStringField(payload.EventObject, "amount"); amountStr != "" {
		amount, _ = decimal.NewFromString(amountStr)
	}

	chain := getStringField(payload.EventObject, "chain")
	// Drain objects may not have a top-level "chain". Fall back to
	// destination.payment_rail which indicates where Bridge sent the funds.
	if chain == "" {
		if dest, ok := payload.EventObject["destination"].(map[string]interface{}); ok {
			chain = getStringField(dest, "payment_rail")
		}
	}

	if err := h.service.ProcessCryptoDeposit(c, drainID, customerID, amount, chain); err != nil {
		h.logger.Error("Failed to process liquidation address drain",
			zap.String("drain_id", drainID),
			zap.String("customer_id", customerID),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "drain_processing_failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// handleVirtualAccountActivity processes virtual_account.activity events (fiat deposits)
func (h *BridgeWebhookHandler) handleVirtualAccountActivity(c *gin.Context, payload BridgeWebhookPayload) {
	eventType := fmt.Sprintf("%v", payload.EventObject["type"])
	h.logger.Info("Virtual account activity",
		zap.String("activity_type", eventType),
		zap.String("event_type", payload.EventType))

	// Only process payment_processed — the final, on-chain-confirmed state.
	// All other event types (funds_received, payment_submitted, funds_scheduled,
	// in_review, refunded, microdeposit, account_update, deactivation, reactivation)
	// are informational and must not trigger deposit processing.
	if eventType != "payment_processed" {
		h.logger.Info("Skipping non-final virtual account activity event",
			zap.String("activity_type", eventType),
			zap.String("virtual_account_id", getStringField(payload.EventObject, "virtual_account_id")))
		c.JSON(http.StatusOK, gin.H{"status": "acknowledged"})
		return
	}

	// Extract deposit details
	event := &BridgeDepositEvent{
		VirtualAccountID: getStringField(payload.EventObject, "virtual_account_id"),
		CustomerID:       getStringField(payload.EventObject, "customer_id"),
		Amount:           getStringField(payload.EventObject, "amount"),
		Currency:         getStringField(payload.EventObject, "currency"),
		TransactionRef:   getStringField(payload.EventObject, "deposit_id"),
		Status:           eventType,
	}

	if h.service == nil {
		h.logger.Error("Bridge webhook service is not configured")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service_unavailable"})
		return
	}

	if err := h.service.ProcessFiatDeposit(c, event); err != nil {
		h.logger.Error("Failed to process virtual account activity",
			zap.String("virtual_account_id", event.VirtualAccountID),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "bridge_deposit_processing_failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// handleTransferEvent processes transfer events
func (h *BridgeWebhookHandler) handleTransferEvent(c *gin.Context, payload BridgeWebhookPayload) {
	transferID := payload.EventObjectID
	state := payload.EventObjectStatus

	h.logger.Info("Transfer event",
		zap.String("transfer_id", transferID),
		zap.String("state", state),
		zap.String("event_type", payload.EventType))

	if h.service == nil {
		h.logger.Error("Bridge webhook service is not configured")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service_unavailable"})
		return
	}

	switch state {
	case "payment_processed":
		// Success state - complete the withdrawal
		if err := h.service.ProcessTransferCompleted(c, transferID); err != nil {
			h.logger.Error("Failed to process transfer completed", zap.Error(err))
		}
	case "funds_received", "payment_submitted":
		// Intermediate states - just log and wait for final state
		h.logger.Info("Transfer in progress",
			zap.String("transfer_id", transferID),
			zap.String("state", state))
	case "awaiting_funds":
		// Transfer waiting for funds - this is normal for offramp
		h.logger.Info("Transfer awaiting funds",
			zap.String("transfer_id", transferID),
			zap.String("state", state))
	case "in_review":
		// Transfer under compliance review - needs attention
		h.logger.Warn("Transfer under compliance review - requires attention",
			zap.String("transfer_id", transferID),
			zap.String("state", state))
		if err := h.service.ProcessTransferUnderReview(c, transferID); err != nil {
			h.logger.Error("Failed to process transfer under review", zap.Error(err))
		}
	case "undeliverable":
		// Transfer cannot be delivered - needs refund
		h.logger.Warn("Transfer undeliverable - initiating refund",
			zap.String("transfer_id", transferID),
			zap.String("state", state))
		if err := h.service.ProcessTransferFailed(c, transferID, state); err != nil {
			h.logger.Error("Failed to process undeliverable transfer", zap.Error(err))
		}
	case "returned":
		// Transfer was returned - reverse the funds
		h.logger.Warn("Transfer returned - reversing funds",
			zap.String("transfer_id", transferID),
			zap.String("state", state))
		if err := h.service.ProcessTransferFailed(c, transferID, state); err != nil {
			h.logger.Error("Failed to process returned transfer", zap.Error(err))
		}
	case "refund_in_flight":
		// Refund in progress
		h.logger.Info("Refund in progress",
			zap.String("transfer_id", transferID),
			zap.String("state", state))
		if err := h.service.ProcessTransferRefundInFlight(c, transferID); err != nil {
			h.logger.Error("Failed to process refund in flight", zap.Error(err))
		}
	case "refund_failed":
		// Critical - refund failed, manual intervention required
		h.logger.Error("CRITICAL: Refund failed - manual intervention required",
			zap.String("transfer_id", transferID),
			zap.String("state", state))
		if err := h.service.ProcessTransferRefundFailed(c, transferID); err != nil {
			h.logger.Error("Failed to process refund failure", zap.Error(err))
		}
	case "refunded":
		// Transfer successfully refunded
		h.logger.Info("Transfer refunded",
			zap.String("transfer_id", transferID),
			zap.String("state", state))
		if err := h.service.ProcessTransferRefunded(c, transferID); err != nil {
			h.logger.Error("Failed to process refunded transfer", zap.Error(err))
		}
	case "canceled":
		// Transfer was canceled
		h.logger.Info("Transfer canceled",
			zap.String("transfer_id", transferID),
			zap.String("state", state))
		if err := h.service.ProcessTransferFailed(c, transferID, state); err != nil {
			h.logger.Error("Failed to process canceled transfer", zap.Error(err))
		}
	case "error":
		// Generic error state
		h.logger.Error("Transfer error state",
			zap.String("transfer_id", transferID),
			zap.String("state", state))
		if err := h.service.ProcessTransferFailed(c, transferID, state); err != nil {
			h.logger.Error("Failed to process error transfer", zap.Error(err))
		}
	default:
		h.logger.Info("Unhandled transfer state",
			zap.String("transfer_id", transferID),
			zap.String("state", state))
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// handleCustomerEvent processes customer events
func (h *BridgeWebhookHandler) handleCustomerEvent(c *gin.Context, payload BridgeWebhookPayload) {
	customerID := payload.EventObjectID
	status := getStringField(payload.EventObject, "status")

	h.logger.Info("Customer event",
		zap.String("customer_id", customerID),
		zap.String("status", status),
		zap.String("event_type", payload.EventType))

	if h.service == nil {
		h.logger.Error("Bridge webhook service is not configured")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service_unavailable"})
		return
	}

	if err := h.service.ProcessCustomerStatusChanged(c, customerID, status); err != nil {
		h.logger.Error("Failed to process customer event", zap.Error(err))
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// handleKYCLinkEvent processes kyc_link events
func (h *BridgeWebhookHandler) handleKYCLinkEvent(c *gin.Context, payload BridgeWebhookPayload) {
	if h.service == nil {
		h.logger.Error("Bridge webhook service is not configured")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service_unavailable"})
		return
	}

	kycStatus := getStringField(payload.EventObject, "kyc_status")
	tosStatus := getStringField(payload.EventObject, "tos_status")
	customerID := getStringField(payload.EventObject, "customer_id")

	h.logger.Info("KYC link event",
		zap.String("kyc_status", kycStatus),
		zap.String("tos_status", tosStatus),
		zap.String("customer_id", customerID),
		zap.String("event_type", payload.EventType))

	// Map KYC status to customer status update
	if customerID != "" && kycStatus != "" {
		mappedStatus := mapKYCStatusToCustomerStatus(kycStatus)
		if err := h.service.ProcessCustomerStatusChanged(c, customerID, mappedStatus); err != nil {
			h.logger.Error("Failed to process KYC status change", zap.Error(err))
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// handleCardAccountEvent processes card_account events
func (h *BridgeWebhookHandler) handleCardAccountEvent(c *gin.Context, payload BridgeWebhookPayload) {
	if h.service == nil {
		h.logger.Error("Bridge webhook service is not configured")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service_unavailable"})
		return
	}

	cardAccountID := payload.EventObjectID
	status := payload.EventObjectStatus

	h.logger.Info("Card account event",
		zap.String("card_account_id", cardAccountID),
		zap.String("status", status),
		zap.String("event_type", payload.EventType))

	if err := h.service.ProcessCardStatusChanged(c, cardAccountID, status); err != nil {
		h.logger.Error("Failed to process card account event", zap.Error(err))
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// handleCardTransactionEvent processes card_transaction events
func (h *BridgeWebhookHandler) handleCardTransactionEvent(c *gin.Context, payload BridgeWebhookPayload) {
	if h.service == nil {
		h.logger.Error("Bridge webhook service is not configured")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service_unavailable"})
		return
	}

	transactionID := payload.EventObjectID
	rawStatus := strings.TrimSpace(payload.EventObjectStatus)
	if rawStatus == "" {
		rawStatus = getStringField(payload.EventObject, "status")
	}
	status := normalizeBridgeWebhookCardStatus(rawStatus)
	if status == "" {
		status = "pending"
	}
	cardAccountID := getStringField(payload.EventObject, "card_account_id")

	var amount decimal.Decimal
	if amountStr := getStringField(payload.EventObject, "amount"); amountStr != "" {
		amount, _ = decimal.NewFromString(amountStr)
	}

	merchantName := getStringField(payload.EventObject, "merchant_name")
	merchantCategory := getStringField(payload.EventObject, "merchant_category")

	h.logger.Info("Card transaction event",
		zap.String("transaction_id", transactionID),
		zap.String("card_account_id", cardAccountID),
		zap.String("status", status),
		zap.String("amount", amount.String()))

	switch status {
	case "declined":
		if err := h.service.ProcessCardTransaction(c, cardAccountID, transactionID, amount, merchantName, merchantCategory, status); err != nil {
			h.logger.Error("Failed to process declined transaction", zap.Error(err))
		}
	case "pending":
		if err := h.service.ProcessCardAuthorization(c, cardAccountID, amount, merchantName, merchantCategory); err != nil {
			h.logger.Error("Failed to process card authorization", zap.Error(err))
		} else if err := h.service.ProcessCardTransaction(c, cardAccountID, transactionID, amount, merchantName, merchantCategory, "pending"); err != nil {
			h.logger.Error("Failed to record pending authorization", zap.Error(err))
		}
	default:
		if err := h.service.ProcessCardTransaction(c, cardAccountID, transactionID, amount, merchantName, merchantCategory, status); err != nil {
			h.logger.Error("Failed to process card transaction", zap.Error(err))
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// handlePostedCardTransaction processes posted_card_account_transaction events
func (h *BridgeWebhookHandler) handlePostedCardTransaction(c *gin.Context, payload BridgeWebhookPayload) {
	if h.service == nil {
		h.logger.Error("Bridge webhook service is not configured")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service_unavailable"})
		return
	}

	transactionID := payload.EventObjectID
	cardAccountID := getStringField(payload.EventObject, "card_account_id")

	var amount decimal.Decimal
	if amountStr := getStringField(payload.EventObject, "amount"); amountStr != "" {
		amount, _ = decimal.NewFromString(amountStr)
	}

	merchantName := getStringField(payload.EventObject, "merchant_name")
	merchantCategory := getStringField(payload.EventObject, "merchant_category")

	h.logger.Info("Posted card transaction",
		zap.String("transaction_id", transactionID),
		zap.String("card_account_id", cardAccountID),
		zap.String("amount", amount.String()))

	if err := h.service.ProcessCardTransaction(c, cardAccountID, transactionID, amount, merchantName, merchantCategory, "posted"); err != nil {
		h.logger.Error("Failed to process posted card transaction", zap.Error(err))
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// handleCardWithdrawalEvent processes card_withdrawal events.
// These events are currently logged for observability and acknowledged.
func (h *BridgeWebhookHandler) handleCardWithdrawalEvent(c *gin.Context, payload BridgeWebhookPayload) {
	withdrawalID := payload.EventObjectID
	status := payload.EventObjectStatus

	h.logger.Info("Card withdrawal event",
		zap.String("withdrawal_id", withdrawalID),
		zap.String("status", status),
		zap.String("event_type", payload.EventType))

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *BridgeWebhookHandler) handleWalletEvent(c *gin.Context, payload BridgeWebhookPayload) {
	walletID := payload.EventObjectID
	status := payload.EventObjectStatus
	walletAddress := getStringField(payload.EventObject, "address")
	chain := getStringField(payload.EventObject, "chain")

	h.logger.Info("Wallet event received",
		zap.String("wallet_id", walletID),
		zap.String("status", status),
		zap.String("address", walletAddress),
		zap.String("chain", chain),
		zap.String("event_type", payload.EventType))

	if h.walletService == nil {
		h.logger.Warn("Wallet service not configured for webhook handling - logging event only")
		c.JSON(http.StatusOK, gin.H{"status": "acknowledged", "message": "wallet service not configured"})
		return
	}

	if err := h.walletService.SyncWalletStatus(c.Request.Context(), walletID, status); err != nil {
		h.logger.Error("Failed to sync wallet status",
			zap.String("wallet_id", walletID),
			zap.String("status", status),
			zap.Error(err))
		c.JSON(http.StatusOK, gin.H{"status": "acknowledged", "synced": false})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "acknowledged", "synced": true})
}

// handleLegacyEventType handles old-style event types for backwards compatibility
func (h *BridgeWebhookHandler) handleLegacyEventType(c *gin.Context, payload BridgeWebhookPayload) {
	switch payload.EventType {
	case "virtual_account.deposit.received", "virtual_account.deposit.completed":
		h.handleDepositReceived(c, payload)
	case "transfer.completed":
		h.handleTransferCompleted(c, payload)
	case "transfer.failed":
		h.handleTransferFailed(c, payload)
	case "card.authorization.request":
		h.handleCardAuthorization(c, payload)
	case "card.transaction.completed", "card.transaction.captured":
		h.handleCardTransaction(c, payload)
	case "card.transaction.declined":
		h.handleCardTransactionDeclined(c, payload)
	case "card.status_changed":
		h.handleCardStatusChanged(c, payload)
	case "customer.status_changed", "customer.kyc.approved", "customer.kyc.rejected":
		h.handleCustomerStatusChanged(c, payload)
	default:
		h.logger.Info("Unhandled Bridge event",
			zap.String("event_type", payload.EventType),
			zap.String("event_category", payload.EventCategory))
		c.JSON(http.StatusOK, gin.H{"status": "ignored"})
	}
}

func (h *BridgeWebhookHandler) handleDepositReceived(c *gin.Context, payload BridgeWebhookPayload) {
	if h.service == nil {
		h.logger.Error("Bridge webhook service is not configured")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service_unavailable"})
		return
	}

	// Extract deposit details from event object
	event := &BridgeDepositEvent{
		VirtualAccountID: payload.EventObjectID,
		Status:           payload.EventObjectStatus,
	}

	// Parse event object for additional details
	if amount, ok := payload.EventObject["amount"].(string); ok {
		event.Amount = amount
	}
	if currency, ok := payload.EventObject["currency"].(string); ok {
		event.Currency = currency
	}
	if txRef, ok := payload.EventObject["transaction_ref"].(string); ok {
		event.TransactionRef = txRef
	}
	if customerID, ok := payload.EventObject["customer_id"].(string); ok {
		event.CustomerID = customerID
	}

	if err := h.service.ProcessFiatDeposit(c, event); err != nil {
		h.logger.Error("Failed to process fiat deposit",
			zap.String("virtual_account_id", event.VirtualAccountID),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "bridge_deposit_processing_failed"})
		return
	}

	h.logger.Info("Fiat deposit processed",
		zap.String("virtual_account_id", event.VirtualAccountID),
		zap.String("amount", event.Amount))

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *BridgeWebhookHandler) handleTransferCompleted(c *gin.Context, payload BridgeWebhookPayload) {
	if h.service == nil {
		h.logger.Error("Bridge webhook service is not configured")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service_unavailable"})
		return
	}

	transferID := payload.EventObjectID

	if err := h.service.ProcessTransferCompleted(c, transferID); err != nil {
		h.logger.Error("Failed to process transfer completed",
			zap.String("transfer_id", transferID),
			zap.Error(err))
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *BridgeWebhookHandler) handleTransferFailed(c *gin.Context, payload BridgeWebhookPayload) {
	h.logger.Warn("Bridge transfer failed",
		zap.String("transfer_id", payload.EventObjectID),
		zap.String("status", payload.EventObjectStatus))

	if h.service != nil {
		if err := h.service.ProcessTransferFailed(c, payload.EventObjectID, payload.EventObjectStatus); err != nil {
			h.logger.Error("Failed to process transfer failure",
				zap.String("transfer_id", payload.EventObjectID),
				zap.Error(err))
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "acknowledged"})
}

func (h *BridgeWebhookHandler) handleCustomerStatusChanged(c *gin.Context, payload BridgeWebhookPayload) {
	if h.service == nil {
		h.logger.Error("Bridge webhook service is not configured")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service_unavailable"})
		return
	}

	customerID := payload.EventObjectID
	status := payload.EventObjectStatus

	if err := h.service.ProcessCustomerStatusChanged(c, customerID, status); err != nil {
		h.logger.Error("Failed to process customer status change",
			zap.String("customer_id", customerID),
			zap.Error(err))
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *BridgeWebhookHandler) handleCardAuthorization(c *gin.Context, payload BridgeWebhookPayload) {
	cardID := payload.EventObjectID

	var amount decimal.Decimal
	if amountStr, ok := payload.EventObject["amount"].(string); ok {
		var err error
		amount, err = decimal.NewFromString(amountStr)
		if err != nil {
			h.logger.Error("Failed to parse card authorization amount",
				zap.String("card_id", cardID),
				zap.String("raw_amount", amountStr),
				zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid amount format"})
			return
		}
	}

	merchantName := ""
	if mn, ok := payload.EventObject["merchant_name"].(string); ok {
		merchantName = mn
	}
	merchantCategory := ""
	if mc, ok := payload.EventObject["merchant_category"].(string); ok {
		merchantCategory = mc
	}

	h.logger.Info("Card authorization request",
		zap.String("card_id", cardID),
		zap.String("amount", amount.String()),
		zap.String("merchant", merchantName))

	if h.service != nil {
		if err := h.service.ProcessCardAuthorization(c, cardID, amount, merchantName, merchantCategory); err != nil {
			h.logger.Error("Failed to process card authorization", zap.Error(err))
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *BridgeWebhookHandler) handleCardTransaction(c *gin.Context, payload BridgeWebhookPayload) {
	cardID := payload.EventObjectID
	transID := ""
	if tid, ok := payload.EventObject["transaction_id"].(string); ok {
		transID = tid
	}

	var amount decimal.Decimal
	if amountStr, ok := payload.EventObject["amount"].(string); ok {
		var err error
		amount, err = decimal.NewFromString(amountStr)
		if err != nil {
			h.logger.Error("Failed to parse card transaction amount",
				zap.String("card_id", cardID),
				zap.String("transaction_id", transID),
				zap.String("raw_amount", amountStr),
				zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid amount format"})
			return
		}
	}

	merchantName := ""
	if mn, ok := payload.EventObject["merchant_name"].(string); ok {
		merchantName = mn
	}
	merchantCategory := ""
	if mc, ok := payload.EventObject["merchant_category"].(string); ok {
		merchantCategory = mc
	}

	h.logger.Info("Card transaction completed",
		zap.String("card_id", cardID),
		zap.String("transaction_id", transID),
		zap.String("amount", amount.String()))

	if h.service != nil {
		if err := h.service.ProcessCardTransaction(c, cardID, transID, amount, merchantName, merchantCategory, "completed"); err != nil {
			h.logger.Error("Failed to process card transaction", zap.Error(err))
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *BridgeWebhookHandler) handleCardTransactionDeclined(c *gin.Context, payload BridgeWebhookPayload) {
	cardID := payload.EventObjectID
	transID := ""
	if tid, ok := payload.EventObject["transaction_id"].(string); ok {
		transID = tid
	}

	declineReason := ""
	if dr, ok := payload.EventObject["decline_reason"].(string); ok {
		declineReason = dr
	}

	h.logger.Warn("Card transaction declined",
		zap.String("card_id", cardID),
		zap.String("transaction_id", transID),
		zap.String("reason", declineReason))

	if h.service != nil {
		if err := h.service.ProcessCardTransactionDeclined(c, cardID, transID, declineReason); err != nil {
			h.logger.Error("Failed to process declined transaction", zap.Error(err))
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "acknowledged"})
}

func (h *BridgeWebhookHandler) handleCardStatusChanged(c *gin.Context, payload BridgeWebhookPayload) {
	cardID := payload.EventObjectID
	status := payload.EventObjectStatus

	h.logger.Info("Card status changed",
		zap.String("card_id", cardID),
		zap.String("status", status))

	if h.service != nil {
		if err := h.service.ProcessCardStatusChanged(c, cardID, status); err != nil {
			h.logger.Error("Failed to process card status change", zap.Error(err))
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *BridgeWebhookHandler) verifySignature(signature string, body []byte) bool {
	if h.webhookSecret == "" {
		if h.skipWebhookVerification {
			h.logger.Warn("⚠️  INSECURE: Bridge webhook verification disabled - no secret configured")
			return true
		}
		h.logger.Error("Bridge webhook public key not configured - rejecting webhook for security")
		return false
	}

	timestamp, parsedSig := parseBridgeSignatureHeader(signature)
	if parsedSig == "" {
		parsedSig = strings.TrimSpace(signature)
	}

	// Bridge uses RSA-SHA256 signatures with PEM-encoded public key
	if strings.Contains(h.webhookSecret, "BEGIN PUBLIC KEY") {
		return h.verifyRSASignature(timestamp, parsedSig, body)
	}

	// Fallback to HMAC for backwards compatibility
	mac := hmac.New(sha256.New, []byte(h.webhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	if hmac.Equal([]byte(expected), []byte(parsedSig)) {
		return true
	}

	// Bridge timestamped signature format: t=<timestamp>,v0=<signature>
	if timestamp != "" {
		mac = hmac.New(sha256.New, []byte(h.webhookSecret))
		mac.Write([]byte(timestamp + "." + string(body)))
		expected = hex.EncodeToString(mac.Sum(nil))
		if hmac.Equal([]byte(expected), []byte(parsedSig)) {
			return true
		}
	}

	return false
}

// verifyRSASignature verifies Bridge webhook using RSA public key
func (h *BridgeWebhookHandler) verifyRSASignature(timestamp, sig string, body []byte) bool {
	// Enforce timestamped signatures to reduce replay risk.
	if timestamp == "" {
		h.logger.Warn("Bridge RSA signature missing timestamp")
		return false
	}
	eventTime, err := parseBridgeWebhookTimestamp(timestamp)
	if err != nil {
		h.logger.Warn("Bridge RSA signature timestamp parse failed", zap.Error(err))
		return false
	}
	// Allow up to 72 hours for retried/delayed deliveries (Bridge retries stuck webhooks days later).
	// Replay protection is handled by Redis deduplication in the webhook security middleware.
	if time.Since(eventTime) > 72*time.Hour {
		h.logger.Warn("Bridge webhook timestamp too old", zap.Time("event_time", eventTime))
		return false
	}
	if eventTime.After(time.Now().Add(1 * time.Minute)) {
		h.logger.Warn("Bridge webhook timestamp too far in future", zap.Time("event_time", eventTime))
		return false
	}

	if sig == "" {
		h.logger.Warn("Bridge RSA signature missing signature component")
		return false
	}

	// Normalize PEM key - handle single-line format from .env
	pemKey := h.webhookSecret
	if !strings.Contains(pemKey, "\n") {
		// Single line PEM - add newlines
		pemKey = strings.Replace(pemKey, "-----BEGIN PUBLIC KEY-----", "-----BEGIN PUBLIC KEY-----\n", 1)
		pemKey = strings.Replace(pemKey, "-----END PUBLIC KEY-----", "\n-----END PUBLIC KEY-----", 1)
	}

	// Parse PEM public key
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		h.logger.Error("Failed to parse PEM block from webhook secret")
		return false
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		h.logger.Error("Failed to parse public key", zap.Error(err))
		return false
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		h.logger.Error("Public key is not RSA")
		return false
	}

	// Decode base64 signature — Bridge uses strict standard base64 encoding.
	sigBytes, err := base64.StdEncoding.Strict().DecodeString(sig)
	if err != nil {
		h.logger.Error("Failed to decode signature", zap.Error(err))
		return false
	}

	// Bridge signs: timestamp + "." + body (double SHA256 per Bridge Go sample).
	signedPayload := []byte(timestamp + "." + string(body))
	hashed := sha256.Sum256(signedPayload)
	hashed = sha256.Sum256(hashed[:])

	// Verify RSA-SHA256 signature
	err = rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, hashed[:], sigBytes)
	if err != nil {
		h.logger.Warn("Bridge webhook signature verification failed", zap.Error(err))
		return false
	}

	return true
}

func parseBridgeSignatureHeader(signatureHeader string) (timestamp string, signature string) {
	trimmed := strings.TrimSpace(signatureHeader)
	if trimmed == "" {
		return "", ""
	}

	if !strings.Contains(trimmed, "=") {
		return "", trimmed
	}

	parts := strings.Split(trimmed, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "t=") {
			timestamp = strings.TrimPrefix(part, "t=")
			continue
		}
		if strings.HasPrefix(part, "v0=") {
			signature = strings.TrimPrefix(part, "v0=")
			continue
		}
		if strings.HasPrefix(part, "v1=") && signature == "" {
			// Keep backward compatibility with legacy header formats.
			signature = strings.TrimPrefix(part, "v1=")
		}
	}

	return strings.TrimSpace(timestamp), strings.TrimSpace(signature)
}

func parseBridgeWebhookTimestamp(raw string) (time.Time, error) {
	ts, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return time.Time{}, err
	}

	// Bridge webhook timestamp uses milliseconds in current docs.
	if ts > 1_000_000_000_000 {
		return time.UnixMilli(ts), nil
	}
	return time.Unix(ts, 0), nil
}

// BridgeWebhookServiceImpl implements BridgeWebhookService
type BridgeWebhookServiceImpl struct {
	virtualAccountService BridgeVirtualAccountProcessor
	customerService       BridgeCustomerProcessor
	cardService           BridgeCardProcessor
	withdrawalService     BridgeWithdrawalProcessor
	notifier              BridgeWebhookNotifier
	userRepo              UserRepositoryForCustomer
	logger                *zap.Logger
	db                    *sql.DB
}

// BridgeVirtualAccountProcessor processes virtual account events
type BridgeVirtualAccountProcessor interface {
	ProcessFiatDeposit(ctx *gin.Context, event *BridgeDepositEvent) error
	ProcessCryptoDeposit(ctx context.Context, userID uuid.UUID, transferID string, amount decimal.Decimal, chain string) error
}

// BridgeCustomerProcessor processes customer events
type BridgeCustomerProcessor interface {
	UpdateCustomerStatus(ctx context.Context, customerID string, status string) error
}

// BridgeCardProcessor processes card events
type BridgeCardProcessor interface {
	Authorize(ctx *gin.Context, cardID string, amount decimal.Decimal, merchantName, merchantCategory string) (bool, string, error)
	ProcessAuthorization(ctx *gin.Context, cardID string, amount decimal.Decimal, merchantName, merchantCategory string) error
	RecordTransaction(ctx *gin.Context, cardID, transactionID string, amount decimal.Decimal, merchantName, merchantCategory, status string) error
	RecordDeclinedTransaction(ctx *gin.Context, cardID, transactionID, declineReason string) error
	SyncCardStatus(ctx *gin.Context, cardID, status string) error
}

type BridgeWithdrawalProcessor interface {
	CompleteWithdrawalByTransferID(ctx context.Context, transferID string) error
	FailWithdrawalByTransferID(ctx context.Context, transferID, reason string) error
	MarkWithdrawalUnderReview(ctx context.Context, transferID string) error
	UpdateWithdrawalStatus(ctx context.Context, transferID, status string) error
	MarkWithdrawalRefundFailed(ctx context.Context, transferID string) error
	MarkWithdrawalRefunded(ctx context.Context, transferID string) error
}

// BridgeWebhookNotifier sends notifications for Bridge events
type BridgeWebhookNotifier interface {
	NotifyDepositReceived(ctx *gin.Context, userID uuid.UUID, amount, currency string) error
	NotifyKYCStatusChanged(ctx *gin.Context, userID uuid.UUID, status string) error
}

// NewBridgeWebhookService creates a new Bridge webhook service
func NewBridgeWebhookService(
	virtualAccountService BridgeVirtualAccountProcessor,
	customerService BridgeCustomerProcessor,
	cardService BridgeCardProcessor,
	withdrawalService BridgeWithdrawalProcessor,
	notifier BridgeWebhookNotifier,
	userRepo UserRepositoryForCustomer,
	logger *zap.Logger,
	db *sql.DB,
) *BridgeWebhookServiceImpl {
	return &BridgeWebhookServiceImpl{
		virtualAccountService: virtualAccountService,
		customerService:       customerService,
		cardService:           cardService,
		withdrawalService:     withdrawalService,
		notifier:              notifier,
		userRepo:              userRepo,
		logger:                logger,
		db:                    db,
	}
}

func (s *BridgeWebhookServiceImpl) ProcessFiatDeposit(ctx *gin.Context, event *BridgeDepositEvent) error {
	return s.virtualAccountService.ProcessFiatDeposit(ctx, event)
}

func (s *BridgeWebhookServiceImpl) ProcessCryptoDeposit(ctx *gin.Context, transferID string, customerID string, amount decimal.Decimal, chain string) error {
	s.logger.Info("Crypto deposit transfer completed", zap.String("transfer_id", transferID), zap.String("customer_id", customerID), zap.String("amount", amount.String()))
	if s.virtualAccountService == nil {
		s.logger.Warn("Virtual account service not configured, skipping crypto deposit", zap.String("transfer_id", transferID))
		return nil
	}
	if customerID == "" {
		s.logger.Error("Empty customer_id in transfer webhook", zap.String("transfer_id", transferID))
		return fmt.Errorf("empty customer_id for transfer %s", transferID)
	}
	if !amount.GreaterThan(decimal.Zero) {
		s.logger.Error("Invalid amount in transfer webhook", zap.String("transfer_id", transferID), zap.String("amount", amount.String()))
		return fmt.Errorf("invalid amount %s for transfer %s", amount.String(), transferID)
	}
	user, err := s.userRepo.GetByBridgeCustomerID(ctx, customerID)
	if err != nil {
		s.logger.Error("Failed to look up user for Bridge customer", zap.String("customer_id", customerID), zap.Error(err))
		return fmt.Errorf("look up user for customer %s: %w", customerID, err)
	}
	if user == nil {
		s.logger.Warn("No user found for Bridge customer ID", zap.String("customer_id", customerID))
		return nil
	}
	return s.virtualAccountService.ProcessCryptoDeposit(ctx, user.ID, transferID, amount, chain)
}

func (s *BridgeWebhookServiceImpl) ProcessTransferCompleted(ctx *gin.Context, transferID string) error {
	s.logger.Info("Provider transfer completed", zap.String("transfer_id", transferID))

	// Mark PAJ offramp orders as completed (no session needed)
	if s.db != nil {
		result, _ := s.db.ExecContext(ctx, `
			UPDATE paj_orders SET status = 'completed', updated_at = NOW()
			WHERE bridge_transfer_id = $1 AND order_type = 'offramp' AND status NOT IN ('completed', 'failed')`,
			transferID)
		if rows, _ := result.RowsAffected(); rows > 0 {
			s.logger.Info("PAJ offramp order marked completed via Bridge webhook",
				zap.String("transfer_id", transferID))
		}
	}

	if s.withdrawalService == nil {
		s.logger.Warn("Withdrawal service not configured, skipping transfer settlement", zap.String("transfer_id", transferID))
		return nil
	}
	return s.withdrawalService.CompleteWithdrawalByTransferID(ctx, transferID)
}

func (s *BridgeWebhookServiceImpl) ProcessTransferFailed(ctx *gin.Context, transferID string, status string) error {
	s.logger.Warn("Provider transfer failed",
		zap.String("transfer_id", transferID),
		zap.String("status", status))
	if s.withdrawalService == nil {
		s.logger.Warn("Withdrawal service not configured, skipping transfer failure handling", zap.String("transfer_id", transferID))
		return nil
	}
	return s.withdrawalService.FailWithdrawalByTransferID(ctx, transferID, "bridge transfer "+strings.ToLower(strings.TrimSpace(status)))
}

func (s *BridgeWebhookServiceImpl) ProcessTransferUnderReview(ctx *gin.Context, transferID string) error {
	s.logger.Warn("Transfer under compliance review",
		zap.String("transfer_id", transferID))
	if s.withdrawalService == nil {
		s.logger.Error("Withdrawal service not configured for ProcessTransferUnderReview", zap.String("transfer_id", transferID))
		return fmt.Errorf("withdrawal service not configured for ProcessTransferUnderReview")
	}
	return s.withdrawalService.MarkWithdrawalUnderReview(ctx, transferID)
}

func (s *BridgeWebhookServiceImpl) ProcessTransferRefundInFlight(ctx *gin.Context, transferID string) error {
	s.logger.Info("Refund in flight for transfer",
		zap.String("transfer_id", transferID))
	if s.withdrawalService == nil {
		s.logger.Error("Withdrawal service not configured for ProcessTransferRefundInFlight", zap.String("transfer_id", transferID))
		return fmt.Errorf("withdrawal service not configured for ProcessTransferRefundInFlight")
	}
	return s.withdrawalService.UpdateWithdrawalStatus(ctx, transferID, "refund_in_flight")
}

func (s *BridgeWebhookServiceImpl) ProcessTransferRefundFailed(ctx *gin.Context, transferID string) error {
	s.logger.Error("Refund failed for transfer - requires manual intervention",
		zap.String("transfer_id", transferID))
	if s.withdrawalService == nil {
		s.logger.Error("Withdrawal service not configured for ProcessTransferRefundFailed", zap.String("transfer_id", transferID))
		return fmt.Errorf("withdrawal service not configured for ProcessTransferRefundFailed")
	}
	return s.withdrawalService.MarkWithdrawalRefundFailed(ctx, transferID)
}

func (s *BridgeWebhookServiceImpl) ProcessTransferRefunded(ctx *gin.Context, transferID string) error {
	s.logger.Info("Transfer has been refunded",
		zap.String("transfer_id", transferID))
	if s.withdrawalService == nil {
		s.logger.Error("Withdrawal service not configured for ProcessTransferRefunded", zap.String("transfer_id", transferID))
		return fmt.Errorf("withdrawal service not configured for ProcessTransferRefunded")
	}
	return s.withdrawalService.MarkWithdrawalRefunded(ctx, transferID)
}

func (s *BridgeWebhookServiceImpl) ProcessCustomerStatusChanged(ctx *gin.Context, customerID string, status string) error {
	if s.customerService != nil {
		return s.customerService.UpdateCustomerStatus(ctx, customerID, status)
	}
	return nil
}

// Card processing methods - wired to CardService

func (s *BridgeWebhookServiceImpl) AuthorizeCardAuthorization(ctx *gin.Context, cardID string, amount decimal.Decimal, merchantName, merchantCategory string) (bool, string, error) {
	if s.cardService == nil {
		s.logger.Warn("Card service not configured, declining authorization",
			zap.String("card_id", cardID))
		return false, "service_unavailable", nil
	}
	return s.cardService.Authorize(ctx, cardID, amount, merchantName, merchantCategory)
}

func (s *BridgeWebhookServiceImpl) ProcessCardAuthorization(ctx *gin.Context, cardID string, amount decimal.Decimal, merchantName, merchantCategory string) error {
	if s.cardService == nil {
		s.logger.Warn("Card service not configured, skipping authorization processing",
			zap.String("card_id", cardID))
		return nil
	}
	return s.cardService.ProcessAuthorization(ctx, cardID, amount, merchantName, merchantCategory)
}

func (s *BridgeWebhookServiceImpl) ProcessCardTransaction(ctx *gin.Context, cardID, transID string, amount decimal.Decimal, merchantName, merchantCategory, status string) error {
	if s.cardService == nil {
		s.logger.Warn("Card service not configured, skipping transaction processing",
			zap.String("card_id", cardID))
		return nil
	}
	return s.cardService.RecordTransaction(ctx, cardID, transID, amount, merchantName, merchantCategory, status)
}

func (s *BridgeWebhookServiceImpl) ProcessCardTransactionDeclined(ctx *gin.Context, cardID, transID, declineReason string) error {
	if s.cardService == nil {
		s.logger.Warn("Card service not configured, skipping declined transaction processing",
			zap.String("card_id", cardID))
		return nil
	}
	return s.cardService.RecordDeclinedTransaction(ctx, cardID, transID, declineReason)
}

func (s *BridgeWebhookServiceImpl) ProcessCardStatusChanged(ctx *gin.Context, cardID, status string) error {
	if s.cardService == nil {
		s.logger.Warn("Card service not configured, skipping status change processing",
			zap.String("card_id", cardID))
		return nil
	}
	return s.cardService.SyncCardStatus(ctx, cardID, status)
}

// Helper functions for extracting fields from event objects

func getBridgeSignatureHeader(c *gin.Context) string {
	if sig := strings.TrimSpace(c.GetHeader("X-Webhook-Signature")); sig != "" {
		return sig
	}
	if sig := strings.TrimSpace(c.GetHeader("X-Bridge-Signature")); sig != "" {
		return sig
	}
	return strings.TrimSpace(c.GetHeader("Bridge-Signature"))
}

func normalizeBridgeWebhookCardStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "approved", "authorized", "incrementally_authorized", "authorizing":
		return "pending"
	case "denied", "failed", "expired", "timeout":
		return "declined"
	case "posted", "captured", "settled", "partially_settled", "incrementally_settled":
		return "completed"
	case "refunded", "refund", "partially_reversed":
		return "reversed"
	default:
		return strings.ToLower(strings.TrimSpace(status))
	}
}

// getStringField safely extracts a string field from a map, also handling numeric values
func getStringField(obj map[string]interface{}, key string) string {
	if val, ok := obj[key]; ok {
		switch v := val.(type) {
		case string:
			return v
		case float64:
			return decimal.NewFromFloat(v).String()
		case json.Number:
			return v.String()
		}
	}
	return ""
}

// mapKYCStatusToCustomerStatus maps Bridge KYC status to customer status
func mapKYCStatusToCustomerStatus(kycStatus string) string {
	switch kycStatus {
	case "approved":
		return "active"
	case "rejected":
		return "rejected"
	case "incomplete", "not_started":
		return "incomplete"
	case "under_review", "manual_review":
		return "under_review"
	default:
		return kycStatus
	}
}

// BridgeCustomerStatusProcessor handles customer status change events from Bridge
type BridgeCustomerStatusProcessor struct {
	userRepo UserRepositoryForCustomer
	notifier BridgeWebhookNotifier
	logger   *zap.Logger
}

// UserRepositoryForCustomer defines the interface for user lookups needed by customer processor
type UserRepositoryForCustomer interface {
	GetByBridgeCustomerID(ctx context.Context, bridgeCustomerID string) (*entities.UserProfile, error)
	UpdateBridgeKYCStatus(ctx context.Context, userID uuid.UUID, status string) error
	UpdateKYCStatus(ctx context.Context, userID uuid.UUID, status entities.KYCStatus, approvedAt *time.Time, rejectionReason *string) error
	UpdateOnboardingStatus(ctx context.Context, userID uuid.UUID, status entities.OnboardingStatus) error
}

// NewBridgeCustomerStatusProcessor creates a new customer status processor
func NewBridgeCustomerStatusProcessor(
	userRepo UserRepositoryForCustomer,
	logger *zap.Logger,
) *BridgeCustomerStatusProcessor {
	return &BridgeCustomerStatusProcessor{
		userRepo: userRepo,
		logger:   logger,
	}
}

// SetNotifier wires the push notifier after construction
func (s *BridgeCustomerStatusProcessor) SetNotifier(n BridgeWebhookNotifier) {
	s.notifier = n
}

// UpdateCustomerStatus processes Bridge customer status changes
// This is the implementation of BridgeCustomerProcessor interface
func (s *BridgeCustomerStatusProcessor) UpdateCustomerStatus(ctx context.Context, customerID string, status string) error {
	s.logger.Info("Processing Bridge customer status change",
		zap.String("customer_id", customerID),
		zap.String("status", status))

	// Find user by Bridge customer ID
	user, err := s.userRepo.GetByBridgeCustomerID(ctx, customerID)
	if err != nil {
		s.logger.Error("Failed to find user by Bridge customer ID",
			zap.Error(err),
			zap.String("customer_id", customerID))
		return fmt.Errorf("failed to find user: %w", err)
	}

	if user == nil {
		s.logger.Warn("No user found for Bridge customer ID",
			zap.String("customer_id", customerID))
		return fmt.Errorf("user not found for customer: %s", customerID)
	}

	s.logger.Info("Found user for customer status update",
		zap.String("user_id", user.ID.String()),
		zap.String("customer_id", customerID),
		zap.String("new_status", status))

	// Update bridge_kyc_status
	bridgeKYCStatus := mapBridgeKYCStatus(status)

	if err := s.userRepo.UpdateBridgeKYCStatus(ctx, user.ID, bridgeKYCStatus); err != nil {
		s.logger.Error("Failed to update user bridge_kyc_status",
			zap.Error(err),
			zap.String("user_id", user.ID.String()))
		return fmt.Errorf("failed to update user status: %w", err)
	}

	s.logger.Info("Updated bridge_kyc_status",
		zap.String("user_id", user.ID.String()),
		zap.String("new_status", bridgeKYCStatus))

	// When Bridge goes active, promote kyc_status to approved and complete onboarding.
	// Bridge is the authoritative source for KYC approval.
	if bridgeKYCStatus == "active" {
		now := time.Now()
		if err := s.userRepo.UpdateKYCStatus(ctx, user.ID, entities.KYCStatusApproved, &now, nil); err != nil {
			s.logger.Error("Failed to promote kyc_status to approved on Bridge active",
				zap.Error(err), zap.String("user_id", user.ID.String()))
		}
		if err := s.userRepo.UpdateOnboardingStatus(ctx, user.ID, entities.OnboardingStatusCompleted); err != nil {
			s.logger.Error("Failed to complete onboarding on Bridge active",
				zap.Error(err), zap.String("user_id", user.ID.String()))
		}
		s.logger.Info("KYC approved — Bridge active",
			zap.String("user_id", user.ID.String()))
	}

	// Send push notification for KYC outcome
	if s.notifier != nil {
		if ginCtx, ok := ctx.(*gin.Context); ok {
			if notifyErr := s.notifier.NotifyKYCStatusChanged(ginCtx, user.ID, bridgeKYCStatus); notifyErr != nil {
				s.logger.Warn("Failed to send KYC notification", zap.Error(notifyErr))
			}
		}
	}

	// Virtual accounts are now created on-demand via POST /api/v1/funding/virtual-account,
	// not auto-provisioned on KYC approval.

	return nil
}

// mapBridgeKYCStatus maps Bridge customer status to our internal KYC status
func mapBridgeKYCStatus(bridgeStatus string) string {
	switch strings.ToLower(bridgeStatus) {
	case "active", "approved":
		return "active"
	case "rejected", "denied":
		return "rejected"
	case "pending", "processing", "under_review", "in_review":
		return "pending"
	case "incomplete", "not_started":
		return "incomplete"
	default:
		return strings.ToLower(bridgeStatus)
	}
}
