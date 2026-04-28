package webhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// CircleWebhookEvent represents a Circle webhook notification.
type CircleWebhookEvent struct {
	SubscriptionID string                 `json:"subscriptionId"`
	NotificationID string                 `json:"notificationId"`
	NotificationType string              `json:"notificationType"`
	Notification   CircleTransactionEvent `json:"notification"`
	Timestamp      string                 `json:"timestamp"`
	Version        int                    `json:"version"`
}

// CircleTransactionEvent is the Transaction Object inside the webhook.
type CircleTransactionEvent struct {
	ID                 string   `json:"id"`
	State              string   `json:"state"`
	TxHash             string   `json:"txHash"`
	Blockchain         string   `json:"blockchain"`
	WalletID           string   `json:"walletId"`
	SourceAddress      string   `json:"sourceAddress"`
	DestinationAddress string   `json:"destinationAddress"`
	TokenID            string   `json:"tokenId"`
	Amounts            []string `json:"amounts"`
	TransactionType    string   `json:"transactionType"`
	CreateDate         string   `json:"createDate"`
	UpdateDate         string   `json:"updateDate"`
}

// CircleDepositProcessor processes inbound Circle deposits into the allocation engine.
type CircleDepositProcessor interface {
	ProcessCircleDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, chain, txHash, circleWalletID string) error
}

// CircleWalletLookup finds a user by their Circle wallet ID.
type CircleWalletLookup interface {
	GetUserByCircleWalletID(ctx context.Context, circleWalletID string) (uuid.UUID, error)
}

// CircleWebhookHandler handles Circle webhook notifications for inbound deposits.
type CircleWebhookHandler struct {
	depositProcessor CircleDepositProcessor
	walletLookup     CircleWalletLookup
	logger           *zap.Logger
	processedEvents  map[string]bool // simple idempotency (use Redis in production)
}

// NewCircleWebhookHandler creates a new Circle webhook handler.
func NewCircleWebhookHandler(
	depositProcessor CircleDepositProcessor,
	walletLookup CircleWalletLookup,
	logger *zap.Logger,
) *CircleWebhookHandler {
	return &CircleWebhookHandler{
		depositProcessor: depositProcessor,
		walletLookup:     walletLookup,
		logger:           logger,
		processedEvents:  make(map[string]bool),
	}
}

// HandleWebhook is the Gin handler for POST /webhooks/circle.
func (h *CircleWebhookHandler) HandleWebhook(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.logger.Error("Failed to read Circle webhook body", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	var event CircleWebhookEvent
	if err := json.Unmarshal(body, &event); err != nil {
		h.logger.Error("Failed to parse Circle webhook", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	// Idempotency: skip already-processed notifications
	if h.processedEvents[event.NotificationID] {
		h.logger.Debug("Duplicate Circle webhook, skipping", zap.String("notificationId", event.NotificationID))
		c.JSON(http.StatusOK, gin.H{"status": "already_processed"})
		return
	}

	h.logger.Info("Circle webhook received",
		zap.String("type", event.NotificationType),
		zap.String("txId", event.Notification.ID),
		zap.String("state", event.Notification.State),
		zap.String("walletId", event.Notification.WalletID))

	// Only process completed inbound transactions
	if event.NotificationType != "transactions.inbound" {
		c.JSON(http.StatusOK, gin.H{"status": "ignored", "reason": "not inbound"})
		return
	}
	if event.Notification.State != "COMPLETED" && event.Notification.State != "COMPLETE" {
		c.JSON(http.StatusOK, gin.H{"status": "ignored", "reason": "not complete"})
		return
	}

	// Process the deposit
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	if err := h.processInboundDeposit(ctx, &event); err != nil {
		h.logger.Error("Failed to process Circle deposit",
			zap.Error(err),
			zap.String("txId", event.Notification.ID),
			zap.String("walletId", event.Notification.WalletID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "processing failed"})
		return
	}

	h.processedEvents[event.NotificationID] = true
	c.JSON(http.StatusOK, gin.H{"status": "processed"})
}

func (h *CircleWebhookHandler) processInboundDeposit(ctx context.Context, event *CircleWebhookEvent) error {
	tx := event.Notification

	// Parse amount
	if len(tx.Amounts) == 0 {
		return fmt.Errorf("no amounts in transaction")
	}
	amount, err := decimal.NewFromString(tx.Amounts[0])
	if err != nil {
		return fmt.Errorf("invalid amount %q: %w", tx.Amounts[0], err)
	}
	if amount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("zero or negative amount")
	}

	// Look up user by Circle wallet ID
	userID, err := h.walletLookup.GetUserByCircleWalletID(ctx, tx.WalletID)
	if err != nil {
		return fmt.Errorf("wallet lookup failed for %s: %w", tx.WalletID, err)
	}

	// Map Circle blockchain to domain chain
	chain := circleBlockchainToDomainChain(tx.Blockchain)

	h.logger.Info("Processing Circle inbound deposit",
		zap.String("userID", userID.String()),
		zap.String("amount", amount.String()),
		zap.String("chain", chain),
		zap.String("txHash", tx.TxHash))

	return h.depositProcessor.ProcessCircleDeposit(ctx, userID, amount, chain, tx.TxHash, tx.WalletID)
}

func circleBlockchainToDomainChain(blockchain string) string {
	bc := strings.ToUpper(blockchain)
	switch {
	case strings.HasPrefix(bc, "SOL"):
		return string(entities.WalletChainSolana)
	case strings.HasPrefix(bc, "ETH"):
		return string(entities.WalletChainEthereum)
	case strings.HasPrefix(bc, "MATIC"):
		return string(entities.WalletChainPolygon)
	case strings.HasPrefix(bc, "BASE"):
		return string(entities.WalletChainBase)
	case strings.HasPrefix(bc, "AVAX"):
		return string(entities.WalletChainAvalanche)
	case strings.HasPrefix(bc, "ARB"):
		return string(entities.WalletChainArbitrum)
	case strings.HasPrefix(bc, "OP"):
		return string(entities.WalletChainOptimism)
	default:
		return bc
	}
}
