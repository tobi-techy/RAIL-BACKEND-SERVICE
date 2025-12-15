package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/onchain"
	"github.com/rail-service/rail_service/pkg/logger"
)

// CircleWebhookHandler handles Circle API webhook notifications
type CircleWebhookHandler struct {
	onchainEngine *onchain.Engine
	logger        *logger.Logger
	webhookSecret string // For signature verification
}

// NewCircleWebhookHandler creates a new Circle webhook handler
func NewCircleWebhookHandler(
	onchainEngine *onchain.Engine,
	logger *logger.Logger,
	webhookSecret string,
) *CircleWebhookHandler {
	return &CircleWebhookHandler{
		onchainEngine: onchainEngine,
		logger:        logger,
		webhookSecret: webhookSecret,
	}
}

// HandleTransferNotification handles Circle transfer notifications
// POST /webhooks/circle/transfers
func (h *CircleWebhookHandler) HandleTransferNotification(c *gin.Context) {
	ctx := c.Request.Context()

	// Verify webhook signature
	signature := c.GetHeader("X-Circle-Signature")
	if signature == "" {
		h.logger.Warn("Missing Circle webhook signature")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing signature"})
		return
	}

	// Read raw body for signature verification
	var rawBody []byte
	if c.Request.Body != nil {
		rawBody, _ = c.GetRawData()
	}

	// Verify signature (implement actual verification based on Circle docs)
	if !h.verifySignature(signature, rawBody) {
		h.logger.Error("Invalid Circle webhook signature")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	// Parse webhook payload
	var webhook CircleTransferWebhook
	if err := json.Unmarshal(rawBody, &webhook); err != nil {
		h.logger.Error("Failed to parse Circle webhook", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	h.logger.Info("Received Circle transfer webhook",
		"notification_type", webhook.NotificationType,
		"transfer_id", webhook.TransferID,
		"status", webhook.Transfer.Status)

	// Process based on notification type
	switch webhook.NotificationType {
	case "transfers.created", "transfers.completed":
		if err := h.processIncomingTransfer(ctx, &webhook); err != nil {
			h.logger.Error("Failed to process incoming transfer",
				"transfer_id", webhook.TransferID,
				"error", err)
			// Return 200 to prevent retries for processing errors
			// Store failure for manual review
			c.JSON(http.StatusOK, gin.H{"status": "error", "message": err.Error()})
			return
		}

	case "transfers.failed":
		h.logger.Warn("Circle transfer failed",
			"transfer_id", webhook.TransferID,
			"error", webhook.Transfer.ErrorCode)
		// Handle failed transfers (e.g., notify user, reverse ledger entries)

	default:
		h.logger.Info("Unhandled Circle notification type",
			"type", webhook.NotificationType)
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// processIncomingTransfer processes an incoming USDC transfer
func (h *CircleWebhookHandler) processIncomingTransfer(ctx context.Context, webhook *CircleTransferWebhook) error {
	transfer := webhook.Transfer

	// Only process inbound transfers (deposits)
	if transfer.Source.Type != "blockchain" {
		h.logger.Debug("Ignoring non-blockchain transfer",
			"transfer_id", webhook.TransferID,
			"source_type", transfer.Source.Type)
		return nil
	}

	// Parse amount
	amount, err := decimal.NewFromString(transfer.Amount.Amount)
	if err != nil {
		return fmt.Errorf("invalid amount: %w", err)
	}

	// Map Circle wallet ID to user
	// This requires querying managed_wallets table
	circleWalletID := transfer.Destination.ID

	// Determine chain from transfer
	chain := h.mapCircleChainToChain(transfer.Source.Chain)

	// Build deposit request
	// Note: UserID needs to be determined from circleWalletID via managed_wallets
	// For now, we'll let the engine handle that lookup
	depositReq := &onchain.DepositRequest{
		UserID:         uuid.Nil, // Will be looked up by engine
		CircleWalletID: circleWalletID,
		Chain:          chain,
		TxHash:         transfer.TransactionHash,
		Token:          entities.StablecoinUSDC,
		Amount:         amount,
		FromAddress:    transfer.Source.Address,
	}

	// Process deposit via onchain engine
	if err := h.onchainEngine.ProcessDeposit(ctx, depositReq); err != nil {
		return fmt.Errorf("failed to process deposit: %w", err)
	}

	h.logger.Info("Circle transfer processed successfully",
		"transfer_id", webhook.TransferID,
		"amount", amount,
		"tx_hash", transfer.TransactionHash)

	return nil
}

// verifySignature verifies the Circle webhook signature using HMAC-SHA256
func (h *CircleWebhookHandler) verifySignature(signature string, body []byte) bool {
	// Skip verification in dev mode if secret is not configured
	if h.webhookSecret == "" {
		h.logger.Warn("Webhook secret not configured - skipping signature verification")
		return true
	}

	// Circle uses HMAC-SHA256 for webhook signatures
	mac := hmac.New(sha256.New, []byte(h.webhookSecret))
	mac.Write(body)
	expectedSignature := hex.EncodeToString(mac.Sum(nil))

	// Use constant-time comparison to prevent timing attacks
	if subtle.ConstantTimeCompare([]byte(expectedSignature), []byte(signature)) != 1 {
		h.logger.Warn("Webhook signature verification failed",
			"expected_prefix", expectedSignature[:16]+"...",
			"received_prefix", truncateString(signature, 16)+"...")
		return false
	}

	return true
}

// truncateString safely truncates a string to max length
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// mapCircleChainToChain maps Circle's chain identifier to our Chain type
func (h *CircleWebhookHandler) mapCircleChainToChain(circleChain string) entities.Chain {
	switch circleChain {
	case "SOL", "solana":
		return entities.ChainSolana
	case "MATIC", "polygon":
		return entities.ChainPolygon
	case "APTOS", "aptos":
		return entities.ChainAptos
	case "STARKNET", "starknet":
		return entities.ChainStarknet
	default:
		h.logger.Warn("Unknown Circle chain", "chain", circleChain)
		return entities.ChainSolana // Default fallback
	}
}

// ============================================================================
// WEBHOOK PAYLOAD TYPES
// ============================================================================

// CircleTransferWebhook represents a Circle transfer notification
type CircleTransferWebhook struct {
	NotificationType string          `json:"notificationType"`
	TransferID       string          `json:"transferId"`
	Transfer         CircleTransfer  `json:"transfer"`
	Timestamp        string          `json:"timestamp"`
}

// CircleTransfer represents the transfer details
type CircleTransfer struct {
	ID              string                  `json:"id"`
	Source          CircleTransferEndpoint  `json:"source"`
	Destination     CircleTransferEndpoint  `json:"destination"`
	Amount          CircleAmount            `json:"amount"`
	TransactionHash string                  `json:"transactionHash"`
	Status          string                  `json:"status"`
	CreateDate      string                  `json:"createDate"`
	ErrorCode       string                  `json:"errorCode,omitempty"`
}

// CircleTransferEndpoint represents source or destination
type CircleTransferEndpoint struct {
	Type    string `json:"type"` // "wallet", "blockchain", "wire"
	ID      string `json:"id"`
	Chain   string `json:"chain,omitempty"`
	Address string `json:"address,omitempty"`
}

// CircleAmount represents an amount with currency
type CircleAmount struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// ============================================================================
// ADDITIONAL WEBHOOK HANDLERS
// ============================================================================

// HandleWalletNotification handles Circle wallet notifications
// POST /webhooks/circle/wallets
func (h *CircleWebhookHandler) HandleWalletNotification(c *gin.Context) {
	// Handle wallet creation, updates, etc.
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// HandlePaymentNotification handles Circle payment notifications
// POST /webhooks/circle/payments
func (h *CircleWebhookHandler) HandlePaymentNotification(c *gin.Context) {
	// Handle payment status updates
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// RegisterRoutes registers Circle webhook routes
func (h *CircleWebhookHandler) RegisterRoutes(router *gin.RouterGroup) {
	webhooks := router.Group("/webhooks/circle")
	{
		webhooks.POST("/transfers", h.HandleTransferNotification)
		webhooks.POST("/wallets", h.HandleWalletNotification)
		webhooks.POST("/payments", h.HandlePaymentNotification)
	}
}
