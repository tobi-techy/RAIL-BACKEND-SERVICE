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
	SubscriptionID   string                 `json:"subscriptionId"`
	NotificationID   string                 `json:"notificationId"`
	NotificationType string                 `json:"notificationType"`
	Notification     CircleTransactionEvent `json:"notification"`
	Timestamp        string                 `json:"timestamp"`
	Version          int                    `json:"version"`
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
	ProcessCircleDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, token entities.Stablecoin, chain, txHash, circleWalletID string) error
}

// CircleWalletLookup finds a user by their Circle wallet ID.
type CircleWalletLookup interface {
	GetUserByCircleWalletID(ctx context.Context, circleWalletID string) (uuid.UUID, error)
}

// CircleDepositNotifier sends deposit-related push notifications.
type CircleDepositNotifier interface {
	NotifyDepositDetected(ctx context.Context, userID uuid.UUID, chain string) error
}

// CircleUnsupportedAssetService identifies and returns assets Rail does not support.
type CircleUnsupportedAssetService interface {
	GetTokenSymbol(ctx context.Context, walletID, tokenID string) (string, error)
	ReturnUnsupportedToken(ctx context.Context, walletID, tokenID, destinationAddress string, amounts []string, idempotencyKey string) error
}

// CircleWithdrawalCompleter marks outbound Circle withdrawals as completed or failed.
type CircleWithdrawalCompleter interface {
	CompleteWithdrawalByTransferID(ctx context.Context, transferID string) error
	FailWithdrawalByTransferID(ctx context.Context, transferID, reason string) error
}

// CircleWebhookHandler handles Circle webhook notifications for inbound deposits.
type CircleWebhookHandler struct {
	depositProcessor    CircleDepositProcessor
	walletLookup        CircleWalletLookup
	notifier            CircleDepositNotifier
	assetService        CircleUnsupportedAssetService
	withdrawalCompleter CircleWithdrawalCompleter
	logger              *zap.Logger
	webhookSecret       string
	redis               CircleWebhookRedis
	isBlendWallet       func(ctx context.Context, walletID string) (bool, error)
	// isSweepDeposit returns true if the incoming USDC is from a Blend
	// redemption sweep that already credited the user via the ledger. Without
	// this check, the sweep arrival on Solana would be treated as a fresh
	// deposit and double-credit the user (ledger move + deposit detection).
	isSweepDeposit func(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (bool, error)
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

// SetUnsupportedAssetService wires Circle token validation and same-token return transfers.
func (h *CircleWebhookHandler) SetUnsupportedAssetService(s CircleUnsupportedAssetService) {
	h.assetService = s
}

// SetWithdrawalCompleter wires the withdrawal completion handler for outbound transactions.
func (h *CircleWebhookHandler) SetWithdrawalCompleter(w CircleWithdrawalCompleter) {
	h.withdrawalCompleter = w
}

// SetBlendWalletChecker wires a function that checks if a wallet ID belongs to a Blend intermediary.
func (h *CircleWebhookHandler) SetBlendWalletChecker(fn func(ctx context.Context, walletID string) (bool, error)) {
	h.isBlendWallet = fn
}

// SetSweepSuppressor wires a function that checks if an incoming deposit is from
// a Blend redemption sweep that already credited the user via the ledger. This
// prevents double-crediting when sweep-bridged USDC arrives on Solana.
func (h *CircleWebhookHandler) SetSweepSuppressor(fn func(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (bool, error)) {
	h.isSweepDeposit = fn
}

// HandleWebhook is the Gin handler for POST /webhooks/circle.
func (h *CircleWebhookHandler) HandleWebhook(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.logger.Error("Failed to read Circle webhook body", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	// Circle v2 webhooks do not use shared-secret HMAC signatures.
	// Security is enforced via: HTTPS-only endpoint, Redis-based idempotency
	// dedup (notificationId), and ledger-level idempotency keys.
	if len(body) == 0 {
		// Circle endpoint verification ping — return 200
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
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
		// Handle outbound transaction completions (withdrawal status updates)
		if event.NotificationType == "transactions.outbound" {
			if err := h.processOutboundTransaction(c.Request.Context(), &event); err != nil {
				h.logger.Error("Circle outbound webhook processing failed, returning 500 for retry",
					zap.String("txId", event.Notification.ID), zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "processing failed"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"status": "processed"})
			// Mark as processed in Redis
			if err := h.redis.Set(c.Request.Context(), redisKey, true, 24*time.Hour); err != nil {
				h.logger.Error("Failed to mark Circle outbound webhook processed", zap.Error(err))
			}
			return
		}
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

	// Skip inbound deposits on Blend intermediary wallets. These wallets receive
	// USDC from ChainRails bridge then immediately forward to the Blend Safe.
	// Processing them as user deposits would double-count funds.
	// Blend wallets are on Base chain and registered in blend_user_accounts.
	if strings.EqualFold(tx.Blockchain, "BASE") || strings.EqualFold(tx.Blockchain, "BASE-SEPOLIA") {
		if h.isBlendWallet != nil {
			isBlend, _ := h.isBlendWallet(ctx, tx.WalletID)
			if isBlend {
				h.logger.Info("Skipping inbound on Blend intermediary wallet",
					zap.String("walletId", tx.WalletID), zap.String("txId", tx.ID))
				return nil
			}
		}
	}

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

	tokenSymbol, err := h.circleTokenSymbol(ctx, tx.WalletID, tx.TokenID)
	if err != nil {
		return fmt.Errorf("circle token validation failed for wallet %s token %s: %w", tx.WalletID, tx.TokenID, err)
	}

	// SOL deposits are native gas tokens — they stay in the wallet for gas fees
	// and must NOT be treated as unsupported assets (which would trigger a return).
	if isNativeGasToken(tokenSymbol) {
		h.logger.Info("SOL gas deposit accepted — stays in wallet for gas",
			zap.String("userID", userID.String()),
			zap.String("amount", amount.String()),
			zap.String("chain", chain),
			zap.String("tokenSymbol", tokenSymbol),
			zap.String("txHash", tx.TxHash))
		return nil
	}

	token := entities.Stablecoin(tokenSymbol)
	if !token.IsValid() {
		return h.handleUnsupportedInboundAsset(ctx, &event.Notification, userID, tokenSymbol)
	}

	h.logger.Info("Processing Circle inbound deposit",
		zap.String("userID", userID.String()),
		zap.String("amount", amount.String()),
		zap.String("chain", chain),
		zap.String("tokenSymbol", tokenSymbol),
		zap.String("txHash", tx.TxHash))

	// Sweep suppression: if this deposit is from a Blend redemption sweep that
	// already moved funds through the ledger, suppress it. The sweep bridges
	// USDC from Base EOA → Solana wallet, where Circle sees it as a new inbound.
	// Without suppression, it would be credited as a fresh deposit on top of the
	// ledger move the redemption already backed (double-credit).
	if h.isSweepDeposit != nil {
		isSweep, sweepErr := h.isSweepDeposit(ctx, userID, amount)
		if sweepErr != nil {
			h.logger.Warn("Sweep suppression check failed, processing deposit normally",
				zap.String("userID", userID.String()), zap.Error(sweepErr))
		} else if isSweep {
			h.logger.Warn("Suppressing sweep arrival — already credited via Blend redemption ledger",
				zap.String("userID", userID.String()),
				zap.String("amount", amount.String()),
				zap.String("chain", chain),
				zap.String("txHash", tx.TxHash))
			return nil
		}
	}

	return h.depositProcessor.ProcessCircleDeposit(ctx, userID, amount, token, chain, tx.TxHash, tx.WalletID)
}

func (h *CircleWebhookHandler) circleTokenSymbol(ctx context.Context, walletID, tokenID string) (string, error) {
	if strings.TrimSpace(tokenID) == "" {
		return "", fmt.Errorf("missing tokenId")
	}
	if h.assetService == nil {
		return "", fmt.Errorf("unsupported asset service not configured")
	}

	symbol, err := h.assetService.GetTokenSymbol(ctx, walletID, tokenID)
	if err != nil {
		return "", err
	}
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		return "", fmt.Errorf("empty token symbol")
	}
	return symbol, nil
}

func (h *CircleWebhookHandler) handleUnsupportedInboundAsset(ctx context.Context, tx *CircleTransactionEvent, userID uuid.UUID, tokenSymbol string) error {
	h.logger.Warn("Unsupported Circle inbound asset detected; skipping ledger credit",
		zap.String("userID", userID.String()),
		zap.String("walletId", tx.WalletID),
		zap.String("tokenId", tx.TokenID),
		zap.String("tokenSymbol", tokenSymbol),
		zap.String("sourceAddress", tx.SourceAddress),
		zap.String("destinationAddress", tx.DestinationAddress),
		zap.String("txHash", tx.TxHash),
		zap.Strings("amounts", tx.Amounts))

	if h.assetService == nil {
		return fmt.Errorf("unsupported asset service not configured")
	}
	if strings.TrimSpace(tx.TokenID) == "" {
		h.logger.Error("Cannot auto-return unsupported Circle asset without tokenId",
			zap.String("walletId", tx.WalletID),
			zap.String("txHash", tx.TxHash))
		return nil
	}
	if strings.TrimSpace(tx.SourceAddress) == "" {
		h.logger.Error("Cannot auto-return unsupported Circle asset without sourceAddress",
			zap.String("walletId", tx.WalletID),
			zap.String("tokenId", tx.TokenID),
			zap.String("txHash", tx.TxHash))
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(tx.SourceAddress), strings.TrimSpace(tx.DestinationAddress)) {
		h.logger.Error("Refusing unsupported Circle asset return to the same wallet address",
			zap.String("walletId", tx.WalletID),
			zap.String("tokenId", tx.TokenID),
			zap.String("txHash", tx.TxHash))
		return nil
	}

	idempotencyKey := circleUnsupportedRefundIdempotencyKey(tx.ID, tx.TxHash, tx.TokenID)
	if err := h.assetService.ReturnUnsupportedToken(ctx, tx.WalletID, tx.TokenID, tx.SourceAddress, tx.Amounts, idempotencyKey); err != nil {
		return fmt.Errorf("return unsupported Circle asset: %w", err)
	}

	h.logger.Info("Unsupported Circle inbound asset return submitted",
		zap.String("userID", userID.String()),
		zap.String("walletId", tx.WalletID),
		zap.String("tokenId", tx.TokenID),
		zap.String("tokenSymbol", tokenSymbol),
		zap.String("destinationAddress", tx.SourceAddress),
		zap.String("idempotencyKey", idempotencyKey),
		zap.String("txHash", tx.TxHash))
	return nil
}

func circleUnsupportedRefundIdempotencyKey(txID, txHash, tokenID string) string {
	for _, candidate := range []string{txID, txHash} {
		if parsed, err := uuid.Parse(strings.TrimSpace(candidate)); err == nil {
			return parsed.String()
		}
	}
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("circle:unsupported-refund:"+txHash+":"+tokenID)).String()
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

// isNativeGasToken returns true if the token symbol represents a native gas
// token (e.g. SOL). These deposits stay in the wallet for gas fees and must
// not be treated as unsupported assets or credited to the user's ledger.
func isNativeGasToken(symbol string) bool {
	return strings.EqualFold(strings.TrimSpace(symbol), "SOL")
}

// processOutboundTransaction handles completed/failed outbound Circle transfers (withdrawals).
// Returns an error when the DB update fails so the caller can return 500 and Circle retries.
func (h *CircleWebhookHandler) processOutboundTransaction(ctx context.Context, event *CircleWebhookEvent) error {
	tx := event.Notification
	if h.withdrawalCompleter == nil {
		h.logger.Warn("Circle outbound webhook received but no withdrawal completer configured",
			zap.String("txId", tx.ID), zap.String("state", tx.State))
		return nil
	}

	state := strings.ToUpper(strings.TrimSpace(tx.State))
	switch state {
	case "COMPLETE", "COMPLETED", "CONFIRMED":
		if err := h.withdrawalCompleter.CompleteWithdrawalByTransferID(ctx, tx.ID); err != nil {
			h.logger.Error("Failed to complete Circle withdrawal",
				zap.String("txId", tx.ID), zap.Error(err))
			return fmt.Errorf("complete withdrawal %s: %w", tx.ID, err)
		}
		h.logger.Info("Circle outbound withdrawal completed",
			zap.String("txId", tx.ID), zap.String("txHash", tx.TxHash))
	case "FAILED", "DENIED", "CANCELLED", "CANCELED":
		reason := fmt.Sprintf("circle transfer %s", strings.ToLower(state))
		if err := h.withdrawalCompleter.FailWithdrawalByTransferID(ctx, tx.ID, reason); err != nil {
			h.logger.Error("Failed to mark Circle withdrawal as failed",
				zap.String("txId", tx.ID), zap.Error(err))
			return fmt.Errorf("fail withdrawal %s: %w", tx.ID, err)
		}
		h.logger.Info("Circle outbound withdrawal failed",
			zap.String("txId", tx.ID), zap.String("state", state))
	default:
		h.logger.Debug("Circle outbound transaction in non-terminal state",
			zap.String("txId", tx.ID), zap.String("state", state))
	}
	return nil
}
