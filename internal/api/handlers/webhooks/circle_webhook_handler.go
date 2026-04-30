package webhooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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

// CircleDepositNotifier sends deposit-related push notifications.
type CircleDepositNotifier interface {
	NotifyDepositDetected(ctx context.Context, userID uuid.UUID, chain string) error
}

// CircleWebhookHandler handles Circle webhook notifications for inbound deposits.
type CircleWebhookHandler struct {
	depositProcessor CircleDepositProcessor
	walletLookup     CircleWalletLookup
	notifier         CircleDepositNotifier
	logger           *zap.Logger
	webhookSecret    string
	redis            CircleWebhookRedis
}

// CircleWebhookRedis is the subset of Redis needed for webhook idempotency.
type CircleWebhookRedis interface {
	Exists(ctx context.Context, key string) (bool, error)
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
}

// NewCircleWebhookHandler creates a new Circle webhook handler.
func NewCircleWebhookHandler(
	depositProcessor CircleDepositProcessor,
	walletLookup CircleWalletLookup,
	logger *zap.Logger,
	webhookSecret string,
	redis CircleWebhookRedis,
) *CircleWebhookHandler {
	return &CircleWebhookHandler{
		depositProcessor: depositProcessor,
		walletLookup:     walletLookup,
		logger:           logger,
		webhookSecret:    webhookSecret,
		redis:            redis,
	}
}

// SetNotifier wires the deposit notification sender.
func (h *CircleWebhookHandler) SetNotifier(n CircleDepositNotifier) { h.notifier = n }

// HandleWebhook is the Gin handler for POST /webhooks/circle.
func (h *CircleWebhookHandler) HandleWebhook(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.logger.Error("Failed to read Circle webhook body", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	// SECURITY: Always verify webhook signature. Reject if no secret is configured.
	if h.webhookSecret == "" {
		h.logger.Error("Circle webhook secret not configured — rejecting request")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "webhook verification not configured"})
		return
	}
	sig := c.GetHeader("X-Circle-Signature")
	if sig == "" {
		h.logger.Warn("Circle webhook missing signature header")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing signature"})
		return
	}
	mac := hmac.New(sha256.New, []byte(h.webhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		h.logger.Warn("Circle webhook signature mismatch")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	var event CircleWebhookEvent
	if err := json.Unmarshal(body, &event); err != nil {
		h.logger.Error("Failed to parse Circle webhook", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	// Idempotency: skip already-processed notifications via Redis (fail-closed)
	if h.redis == nil {
		h.logger.Error("Circle webhook Redis not configured — rejecting request")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "idempotency store unavailable"})
		return
	}
	redisKey := "circle_wh:" + event.NotificationID
	exists, err := h.redis.Exists(c.Request.Context(), redisKey)
	if err != nil {
		h.logger.Error("Circle webhook Redis check failed — rejecting", zap.Error(err))
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "idempotency check failed"})
		return
	}
	if exists {
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
		// Send "deposit detected" notification for confirmed (but not yet complete) deposits
		if event.Notification.State == "CONFIRMED" && h.notifier != nil && h.walletLookup != nil {
			if userID, err := h.walletLookup.GetUserByCircleWalletID(c.Request.Context(), event.Notification.WalletID); err == nil {
				_ = h.notifier.NotifyDepositDetected(c.Request.Context(), userID, event.Notification.Blockchain)
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "acknowledged"})
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

	// Mark as processed in Redis (24h TTL) — fail-closed: if store fails, log but deposit already processed
	if err := h.redis.Set(c.Request.Context(), "circle_wh:"+event.NotificationID, true, 24*time.Hour); err != nil {
		h.logger.Error("Failed to mark Circle webhook as processed in Redis", zap.Error(err), zap.String("notificationId", event.NotificationID))
	}
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
