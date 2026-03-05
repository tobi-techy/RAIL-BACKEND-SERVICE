package webhooks

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
)

// CircleDepositProcessor defines deposit-processing operations used by Circle webhook handlers.
type CircleDepositProcessor interface {
	ProcessChainDeposit(ctx context.Context, webhook *entities.ChainDepositWebhook) error
}

// CircleManagedWalletRepository resolves Circle wallet IDs to managed wallets.
type CircleManagedWalletRepository interface {
	GetByCircleWalletID(ctx context.Context, circleWalletID string) (*entities.ManagedWallet, error)
}

// CircleWithdrawalRepository resolves and updates withdrawals based on provider transfer IDs.
type CircleWithdrawalRepository interface {
	GetByBridgeTransferID(ctx context.Context, transferID string) (*entities.Withdrawal, error)
	UpdateTxHash(ctx context.Context, id uuid.UUID, txHash string) error
	MarkCompleted(ctx context.Context, id uuid.UUID) error
	MarkFailed(ctx context.Context, id uuid.UUID, errorMsg string) error
}

// CircleWithdrawalLedger posts ledger entries for completed withdrawals.
type CircleWithdrawalLedger interface {
	CreateTransaction(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, txType entities.TransactionType, amount decimal.Decimal, metadata map[string]interface{}) error
}

// CircleWebhookHandler handles Circle API webhook notifications
type CircleWebhookHandler struct {
	fundingService    CircleDepositProcessor
	managedWalletRepo CircleManagedWalletRepository
	withdrawalRepo    CircleWithdrawalRepository
	withdrawalLedger  CircleWithdrawalLedger
	logger            *logger.Logger
	circleAPIKey      string // For fetching public keys
	circleBaseURL     string
	devMode           bool // When true, skips signature verification (development only)
	failOpen          bool // When true, fails open on errors (not recommended for production)

	// Key cache with TTL support
	keyCache   map[string]cachedKey
	keyCacheMu sync.RWMutex
}

type cachedKey struct {
	key       string
	fetchedAt time.Time
}

const keyCacheTTL = 24 * time.Hour

// CircleWebhookConfig holds configuration for Circle webhook handler
type CircleWebhookConfig struct {
	CircleAPIKey  string
	CircleBaseURL string
	DevMode       bool // Skip all verification (development only)
	FailOpen      bool // Fail open on errors (security risk - use with caution)
}

// NewCircleWebhookHandler creates a new Circle webhook handler
func NewCircleWebhookHandler(
	fundingService CircleDepositProcessor,
	managedWalletRepo CircleManagedWalletRepository,
	withdrawalRepo CircleWithdrawalRepository,
	withdrawalLedger CircleWithdrawalLedger,
	logger *logger.Logger,
	circleAPIKey string,
	circleBaseURL string,
) *CircleWebhookHandler {
	return NewCircleWebhookHandlerWithConfig(
		fundingService,
		managedWalletRepo,
		withdrawalRepo,
		withdrawalLedger,
		logger,
		CircleWebhookConfig{
			CircleAPIKey:  circleAPIKey,
			CircleBaseURL: circleBaseURL,
			DevMode:       strings.TrimSpace(circleAPIKey) == "",
			FailOpen:      false, // Default to secure behavior
		},
	)
}

// NewCircleWebhookHandlerWithConfig creates a new Circle webhook handler with custom config
func NewCircleWebhookHandlerWithConfig(
	fundingService CircleDepositProcessor,
	managedWalletRepo CircleManagedWalletRepository,
	withdrawalRepo CircleWithdrawalRepository,
	withdrawalLedger CircleWithdrawalLedger,
	logger *logger.Logger,
	config CircleWebhookConfig,
) *CircleWebhookHandler {
	// Warn if failOpen is enabled in production-like environment
	devMode := config.DevMode
	if !devMode && config.FailOpen {
		logger.Warn("CRITICAL: Circle webhook handler is configured to FAIL-OPEN - this is a security risk in production!")
	}

	return &CircleWebhookHandler{
		fundingService:    fundingService,
		managedWalletRepo: managedWalletRepo,
		withdrawalRepo:    withdrawalRepo,
		withdrawalLedger:  withdrawalLedger,
		logger:            logger,
		circleAPIKey:      config.CircleAPIKey,
		circleBaseURL:     config.CircleBaseURL,
		devMode:           devMode,
		failOpen:          config.FailOpen,
		keyCache:          make(map[string]cachedKey),
	}
}

// HandleTransferNotification handles Circle transfer notifications
// POST /webhooks/circle/transfers
func (h *CircleWebhookHandler) HandleTransferNotification(c *gin.Context) {
	ctx := c.Request.Context()

	// Read raw body for signature verification
	var rawBody []byte
	if c.Request.Body != nil {
		rawBody, _ = c.GetRawData()
	}

	// Verify webhook signature using Circle's ECDSA verification.
	// Per Circle docs, headers X-Circle-Key-Id and X-Circle-Signature are present
	// on every notification. In dev mode we skip verification entirely.
	keyID := c.GetHeader("X-Circle-Key-Id")
	signature := c.GetHeader("X-Circle-Signature")

	if !h.devMode {
		if keyID == "" || signature == "" {
			h.logger.Warn("Missing Circle webhook headers")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing signature headers"})
			return
		}
		if !h.verifySignature(ctx, keyID, signature, rawBody) {
			h.logger.Error("Invalid Circle webhook signature")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
			return
		}
	} else {
		h.logger.Info("Skipping Circle webhook signature verification (dev mode)")
	}

	// Parse webhook payload
	var webhook CircleTransferWebhook
	if err := json.Unmarshal(rawBody, &webhook); err != nil {
		h.logger.Error("Failed to parse Circle webhook", "error", err)
		// Return 200 to stop Circle retries on malformed payloads we can't fix.
		c.JSON(http.StatusOK, gin.H{"status": "error", "message": "invalid payload"})
		return
	}

	h.logger.Info("Received Circle transfer webhook",
		"notification_type", webhook.NotificationType,
		"transfer_id", webhook.TransferID,
		"status", webhook.Transfer.Status)

	// Process based on notification type.
	// Circle can emit variants such as transfers.* and transactions.*.
	switch {
	case strings.HasPrefix(strings.ToLower(webhook.NotificationType), "transfers.") ||
		strings.HasPrefix(strings.ToLower(webhook.NotificationType), "transactions."):
		if err := h.processIncomingTransfer(ctx, &webhook); err != nil {
			h.logger.Error("Failed to process incoming transfer",
				"transfer_id", webhook.TransferID,
				"error", err)
		}

	default:
		h.logger.Info("Unhandled Circle notification type",
			"type", webhook.NotificationType)
	}

	// Always return 200 to prevent Circle retries (5-second timeout enforced).
	c.JSON(http.StatusOK, gin.H{"status": "received"})
}

// processIncomingTransfer processes an incoming USDC transfer
func (h *CircleWebhookHandler) processIncomingTransfer(ctx context.Context, webhook *CircleTransferWebhook) error {
	notificationType := strings.ToLower(strings.TrimSpace(webhook.NotificationType))
	if strings.HasPrefix(notificationType, "transactions.") {
		return h.processIncomingTransactionNotification(ctx, webhook)
	}

	transfer := webhook.Transfer

	// Handle outbound transfer state transitions.
	if strings.EqualFold(transfer.Source.Type, "wallet") {
		return h.processOutboundTransferNotification(ctx, webhook)
	}

	// Only process inbound transfers (deposits)
	if !strings.EqualFold(transfer.Source.Type, "blockchain") {
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

	// Only process USDC deposits.
	if !strings.EqualFold(transfer.Amount.Currency, string(entities.StablecoinUSDC)) {
		h.logger.Debug("Ignoring non-USDC transfer",
			"transfer_id", webhook.TransferID,
			"currency", transfer.Amount.Currency)
		return nil
	}

	// Resolve destination wallet from Circle wallet ID.
	circleWalletID := transfer.Destination.ID
	if circleWalletID == "" {
		return fmt.Errorf("missing destination circle wallet ID")
	}
	managedWallet, err := h.managedWalletRepo.GetByCircleWalletID(ctx, circleWalletID)
	if err != nil {
		return fmt.Errorf("failed to resolve managed wallet for circle wallet %s: %w", circleWalletID, err)
	}

	// Determine chain from transfer payload, with wallet-chain fallback.
	chain := h.mapCircleChainToChain(transfer.Source.Chain)
	if chain == "" {
		chain = h.mapWalletChainToChain(managedWallet.Chain)
	}
	if chain == "" {
		return fmt.Errorf("unable to determine chain for deposit: circle_chain=%q, wallet_chain=%q",
			transfer.Source.Chain, managedWallet.Chain)
	}

	blockTime := time.Now()
	if parsed, parseErr := time.Parse(time.RFC3339, transfer.CreateDate); parseErr == nil {
		blockTime = parsed
	}

	chainWebhook := &entities.ChainDepositWebhook{
		Chain:     chain,
		Address:   managedWallet.Address,
		TxHash:    transfer.TransactionHash,
		Token:     entities.StablecoinUSDC,
		Amount:    amount.String(),
		BlockTime: blockTime,
	}

	// Process through the canonical funding deposit flow.
	if err := h.fundingService.ProcessChainDeposit(ctx, chainWebhook); err != nil {
		return fmt.Errorf("failed to process deposit: %w", err)
	}

	h.logger.Info("Circle transfer processed successfully",
		"transfer_id", webhook.TransferID,
		"user_id", managedWallet.UserID.String(),
		"amount", amount,
		"tx_hash", transfer.TransactionHash,
		"address", managedWallet.Address,
		"chain", chain)

	return nil
}

// processIncomingTransactionNotification handles Circle wallets notifications like transactions.inbound.
func (h *CircleWebhookHandler) processIncomingTransactionNotification(ctx context.Context, webhook *CircleTransferWebhook) error {
	if len(webhook.Notification) == 0 {
		return fmt.Errorf("missing notification payload for %s", webhook.NotificationType)
	}

	var n CircleTransactionNotification
	if err := json.Unmarshal(webhook.Notification, &n); err != nil {
		return fmt.Errorf("failed to parse transaction notification: %w", err)
	}

	// Route outbound notifications to withdrawal status sync.
	if strings.EqualFold(n.TransactionType, "OUTBOUND") {
		return h.processOutboundTransactionNotification(ctx, webhook, &n)
	}

	// Only inbound wallet deposits.
	if !strings.EqualFold(n.TransactionType, "INBOUND") {
		h.logger.Debug("Ignoring non-inbound transaction notification",
			"notification_type", webhook.NotificationType,
			"transaction_type", n.TransactionType)
		return nil
	}

	// Only process final successful states.
	state := strings.ToUpper(strings.TrimSpace(n.State))
	switch state {
	case "COMPLETE", "COMPLETED", "CONFIRMED":
		// Process final successful states.
	default:
		h.logger.Debug("Ignoring non-final transaction notification state",
			"notification_type", webhook.NotificationType,
			"state", n.State)
		return nil
	}

	if len(n.Amounts) == 0 {
		return fmt.Errorf("missing amount in transaction notification")
	}
	amount, err := decimal.NewFromString(n.Amounts[0])
	if err != nil {
		return fmt.Errorf("invalid amount in transaction notification: %w", err)
	}

	txHash := strings.TrimSpace(n.TxHash)
	if txHash == "" {
		txHash = strings.TrimSpace(n.TransactionHash)
	}
	if txHash == "" {
		return fmt.Errorf("missing tx hash in transaction notification")
	}

	chain := h.mapCircleChainToChain(n.Blockchain)
	if chain == "" {
		return fmt.Errorf("unable to determine chain for deposit: blockchain=%q", n.Blockchain)
	}

	// Map token ID to token symbol (default to USDC for now)
	token := h.mapTokenIDToToken(n.TokenID)

	address := strings.TrimSpace(n.DestinationAddress)
	walletID := strings.TrimSpace(n.WalletID)

	// Per Circle docs, walletId is the most reliable identifier for dev-controlled
	// wallets. Resolve via managedWalletRepo first, then fall back to destinationAddress.
	if walletID != "" {
		managedWallet, err := h.managedWalletRepo.GetByCircleWalletID(ctx, walletID)
		if err == nil && managedWallet != nil {
			if address == "" {
				address = managedWallet.Address
			}
		}
	}
	if address == "" {
		return fmt.Errorf("missing destination address in transaction notification")
	}

	blockTime := time.Now()
	if parsed, parseErr := time.Parse(time.RFC3339, n.CreateDate); parseErr == nil {
		blockTime = parsed
	}

	chainWebhook := &entities.ChainDepositWebhook{
		Chain:     chain,
		Address:   address,
		TxHash:    txHash,
		Token:     token,
		Amount:    amount.String(),
		BlockTime: blockTime,
	}

	if err := h.fundingService.ProcessChainDeposit(ctx, chainWebhook); err != nil {
		return fmt.Errorf("failed to process transaction notification deposit: %w", err)
	}

	h.logger.Info("Circle transaction notification processed successfully",
		"notification_type", webhook.NotificationType,
		"wallet_id", n.WalletID,
		"amount", amount.String(),
		"tx_hash", txHash,
		"address", address,
		"chain", chain)

	return nil
}

func (h *CircleWebhookHandler) processOutboundTransferNotification(ctx context.Context, webhook *CircleTransferWebhook) error {
	if h.withdrawalRepo == nil {
		return nil
	}

	transferID := strings.TrimSpace(webhook.TransferID)
	if transferID == "" {
		transferID = strings.TrimSpace(webhook.Transfer.ID)
	}
	if transferID == "" {
		h.logger.Warn("Skipping outbound transfer webhook without transfer ID",
			"notification_type", webhook.NotificationType)
		return nil
	}

	withdrawal, err := h.withdrawalRepo.GetByBridgeTransferID(ctx, transferID)
	if err != nil {
		h.logger.Warn("Failed to resolve withdrawal for outbound transfer webhook",
			"transfer_id", transferID,
			"notification_type", webhook.NotificationType,
			"error", err)
		return nil
	}
	if withdrawal == nil {
		h.logger.Warn("No withdrawal matched outbound transfer webhook",
			"transfer_id", transferID,
			"notification_type", webhook.NotificationType)
		return nil
	}
	if withdrawal.Status.IsTerminal() {
		h.logger.Info("Ignoring outbound transfer webhook for terminal withdrawal",
			"withdrawal_id", withdrawal.ID.String(),
			"transfer_id", transferID,
			"status", withdrawal.Status)
		return nil
	}

	txHash := strings.TrimSpace(webhook.Transfer.TransactionHash)
	if txHash != "" {
		_ = h.withdrawalRepo.UpdateTxHash(ctx, withdrawal.ID, txHash)
	}

	status := strings.ToUpper(strings.TrimSpace(webhook.Transfer.Status))
	switch status {
	case "COMPLETE", "COMPLETED", "CONFIRMED", "SUCCESS":
		if err := h.settleCompletedWithdrawal(ctx, withdrawal); err != nil {
			return err
		}
	case "FAILED", "REJECTED", "CANCELLED":
		reason := strings.TrimSpace(webhook.Transfer.ErrorCode)
		if reason == "" {
			reason = "Circle outbound transfer failed"
		}
		_ = h.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, reason)
	}

	return nil
}

func (h *CircleWebhookHandler) processOutboundTransactionNotification(ctx context.Context, webhook *CircleTransferWebhook, n *CircleTransactionNotification) error {
	if h.withdrawalRepo == nil {
		return nil
	}

	transferID := strings.TrimSpace(n.ID)
	if transferID == "" {
		transferID = strings.TrimSpace(webhook.TransferID)
	}
	if transferID == "" {
		transferID = strings.TrimSpace(webhook.Transfer.ID)
	}
	if transferID == "" {
		h.logger.Warn("Skipping outbound transaction webhook without transaction ID",
			"notification_type", webhook.NotificationType)
		return nil
	}

	withdrawal, err := h.withdrawalRepo.GetByBridgeTransferID(ctx, transferID)
	if err != nil {
		h.logger.Warn("Failed to resolve withdrawal for outbound transaction webhook",
			"transfer_id", transferID,
			"notification_type", webhook.NotificationType,
			"error", err)
		return nil
	}
	if withdrawal == nil {
		h.logger.Warn("No withdrawal matched outbound transaction webhook",
			"transfer_id", transferID,
			"notification_type", webhook.NotificationType)
		return nil
	}
	if withdrawal.Status.IsTerminal() {
		h.logger.Info("Ignoring outbound transaction webhook for terminal withdrawal",
			"withdrawal_id", withdrawal.ID.String(),
			"transfer_id", transferID,
			"status", withdrawal.Status)
		return nil
	}

	txHash := strings.TrimSpace(n.TxHash)
	if txHash == "" {
		txHash = strings.TrimSpace(n.TransactionHash)
	}
	if txHash != "" {
		_ = h.withdrawalRepo.UpdateTxHash(ctx, withdrawal.ID, txHash)
	}

	notificationType := strings.ToLower(strings.TrimSpace(webhook.NotificationType))
	state := strings.ToUpper(strings.TrimSpace(n.State))
	isFailed := strings.HasSuffix(notificationType, ".failed") ||
		state == "FAILED" || state == "REJECTED" || state == "CANCELLED"
	isSuccess := state == "COMPLETE" || state == "COMPLETED" || state == "CONFIRMED" || state == "SUCCESS"

	if isFailed {
		code := strings.TrimSpace(n.ErrorCode)
		reasonText := strings.TrimSpace(n.ErrorReason)
		if code == "" {
			code = strings.TrimSpace(webhook.Transfer.ErrorCode)
		}
		reason := reasonText
		if reason == "" {
			reason = code
		}
		if reason != "" && code != "" && !strings.Contains(reason, code) {
			reason = code + ": " + reason
		}
		if reason == "" {
			reason = "Circle outbound transfer failed"
		}
		_ = h.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, reason)
		return nil
	}

	if isSuccess {
		if err := h.settleCompletedWithdrawal(ctx, withdrawal); err != nil {
			return err
		}
	}

	return nil
}

func (h *CircleWebhookHandler) settleCompletedWithdrawal(ctx context.Context, withdrawal *entities.Withdrawal) error {
	if withdrawal == nil {
		return nil
	}

	if h.withdrawalLedger != nil {
		accountType := entities.AccountTypeSpendingBalance
		switch withdrawal.SourceAccount {
		case entities.WithdrawalSourceSpendingBalance:
			accountType = entities.AccountTypeSpendingBalance
		case entities.WithdrawalSourceStashBalance:
			accountType = entities.AccountTypeStashBalance
		}

		metadata := map[string]interface{}{
			"withdrawal_id":   withdrawal.ID.String(),
			"withdrawal_type": string(withdrawal.WithdrawalType),
			"source_account":  string(withdrawal.SourceAccount),
		}
		if withdrawal.DestinationAddress != nil {
			metadata["destination_address"] = *withdrawal.DestinationAddress
		}
		if withdrawal.BridgeTransferID != nil {
			metadata["provider_transfer_id"] = *withdrawal.BridgeTransferID
		}

		if err := h.withdrawalLedger.CreateTransaction(
			ctx,
			withdrawal.UserID,
			accountType,
			entities.TransactionTypeWithdrawal,
			withdrawal.Amount,
			metadata,
		); err != nil {
			return fmt.Errorf("failed to post withdrawal ledger transaction: %w", err)
		}
	}

	if err := h.withdrawalRepo.MarkCompleted(ctx, withdrawal.ID); err != nil {
		return fmt.Errorf("failed to mark withdrawal completed: %w", err)
	}

	return nil
}

// verifySignature verifies the Circle webhook signature using ECDSA-SHA256
func (h *CircleWebhookHandler) verifySignature(ctx context.Context, keyID, signature string, body []byte) bool {
	// Skip ECDSA verification in dev mode or when API key is not configured.
	// Circle's testnet does not expose the /v2/notifications/publicKey endpoint reliably,
	// so attempting to fetch it causes 5XX responses that trigger endless retries.
	if h.devMode || h.circleAPIKey == "" {
		h.logger.Warn("Skipping Circle webhook signature verification (dev/testnet mode)")
		return true
	}

	// Fetch the public key from Circle API (cached by keyID)
	publicKeyBase64, err := h.fetchPublicKeyCached(ctx, keyID)
	if err != nil {
		// Handle based on failOpen configuration
		if h.failOpen {
			// Fail open: allow webhook but log warning
			// WARNING: This is a security risk - only use in development/testnet
			h.logger.Warn("Failed to fetch Circle public key — allowing webhook (fail-open enabled)",
				"error", err, "key_id", keyID)
			return true
		}
		// Fail closed (secure default): reject webhook
		h.logger.Error("Failed to fetch Circle public key — rejecting webhook (fail-open disabled)",
			"error", err, "key_id", keyID)
		return false
	}

	// Decode the base64 public key
	publicKeyBytes, err := base64.StdEncoding.DecodeString(publicKeyBase64)
	if err != nil {
		h.logger.Error("Failed to decode Circle public key", "error", err)
		return false
	}

	// Parse the DER-encoded public key
	publicKeyInterface, err := x509.ParsePKIXPublicKey(publicKeyBytes)
	if err != nil {
		h.logger.Error("Failed to parse Circle public key", "error", err)
		return false
	}

	publicKey, ok := publicKeyInterface.(*ecdsa.PublicKey)
	if !ok {
		h.logger.Error("Circle public key is not ECDSA")
		return false
	}

	// Decode the base64 signature
	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		h.logger.Error("Failed to decode Circle signature", "error", err)
		return false
	}

	// Hash the message body
	hash := sha256.Sum256(body)

	// Verify the signature using ECDSA
	if !ecdsa.VerifyASN1(publicKey, hash[:], signatureBytes) {
		h.logger.Warn("Circle webhook signature verification failed")
		return false
	}

	return true
}

// fetchPublicKeyCached returns the Circle public key for keyID, using an in-memory cache with TTL.
// Uses double-checked locking to prevent race conditions.
func (h *CircleWebhookHandler) fetchPublicKeyCached(ctx context.Context, keyID string) (string, error) {
	// Check cache first with read lock
	h.keyCacheMu.RLock()
	if entry, ok := h.keyCache[keyID]; ok {
		if time.Since(entry.fetchedAt) < keyCacheTTL {
			h.keyCacheMu.RUnlock()
			return entry.key, nil
		}
	}
	h.keyCacheMu.RUnlock()

	// Acquire write lock for fetch
	h.keyCacheMu.Lock()
	defer h.keyCacheMu.Unlock()

	// Double-check after acquiring write lock (another goroutine may have fetched it)
	if entry, ok := h.keyCache[keyID]; ok {
		if time.Since(entry.fetchedAt) < keyCacheTTL {
			return entry.key, nil
		}
	}

	// Fetch from Circle API
	key, err := h.fetchPublicKey(ctx, keyID)
	if err != nil {
		return "", err
	}

	// Update cache
	h.keyCache[keyID] = cachedKey{key: key, fetchedAt: time.Now()}
	return key, nil
}

// fetchPublicKey fetches the public key from Circle API
func (h *CircleWebhookHandler) fetchPublicKey(ctx context.Context, keyID string) (string, error) {
	// Per Circle docs, the notification signature endpoint is always at api.circle.com
	// regardless of whether wallets are on testnet or mainnet.
	url := fmt.Sprintf("https://api.circle.com/v2/notifications/publicKey/%s", keyID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", h.circleAPIKey))
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch public key: status %d", resp.StatusCode)
	}

	var result struct {
		Data struct {
			PublicKey string `json:"publicKey"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.Data.PublicKey, nil
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
	switch strings.ToUpper(strings.TrimSpace(circleChain)) {
	case "SOL", "SOLANA":
		return entities.ChainSOL
	case "SOL-DEVNET":
		return entities.ChainSOLDevnet
	case "MATIC", "POLYGON":
		return entities.ChainMATIC
	case "MATIC-AMOY":
		return entities.ChainMATICAmoy
	case "AVAX", "AVALANCHE":
		return entities.ChainAVAX
	case "AVAX-FUJI":
		return entities.ChainAVAXFuji
	case "BASE":
		return entities.ChainBASE
	case "BASE-SEPOLIA":
		return entities.ChainBASESepolia
	default:
		if circleChain != "" {
			h.logger.Warn("Unknown Circle chain", "chain", circleChain)
		}
		return ""
	}
}

// mapWalletChainToChain maps our internal WalletChain to Chain type
// Uses consolidated mapping logic
func (h *CircleWebhookHandler) mapWalletChainToChain(walletChain entities.WalletChain) entities.Chain {
	return mapWalletChainToChainType(walletChain)
}

// mapWalletChainToChainType is a standalone function for WalletChain to Chain mapping
func mapWalletChainToChainType(walletChain entities.WalletChain) entities.Chain {
	switch walletChain {
	case entities.WalletChainSolana:
		return entities.ChainSOL
	case entities.WalletChainSOLDevnet:
		return entities.ChainSOLDevnet
	case entities.WalletChainPolygon:
		return entities.ChainMATIC
	case entities.WalletChainMATICAmoy:
		return entities.ChainMATICAmoy
	case entities.WalletChainAvalanche:
		return entities.ChainAVAX
	case entities.WalletChainAVAXFuji:
		return entities.ChainAVAXFuji
	case entities.WalletChainBase:
		return entities.ChainBASE
	case entities.WalletChainBASESepolia:
		return entities.ChainBASESepolia
	default:
		return ""
	}
}

// mapTokenIDToToken maps Circle's token ID to our token type
func (h *CircleWebhookHandler) mapTokenIDToToken(tokenID string) entities.Stablecoin {
	// Default to USDC - Circle's primary stablecoin for deposits
	// Token ID is a UUID from Circle, but we only support USDC currently
	h.logger.Debug("Mapping token ID to USDC", "token_id", tokenID)
	return entities.StablecoinUSDC
}

// ============================================================================
// WEBHOOK PAYLOAD TYPES
// ============================================================================

// CircleTransferWebhook represents a Circle transfer notification
type CircleTransferWebhook struct {
	NotificationType string          `json:"notificationType"`
	TransferID       string          `json:"transferId"`
	Transfer         CircleTransfer  `json:"transfer"`
	Notification     json.RawMessage `json:"notification"`
	Timestamp        string          `json:"timestamp"`
}

// CircleTransfer represents the transfer details
type CircleTransfer struct {
	ID              string                 `json:"id"`
	Source          CircleTransferEndpoint `json:"source"`
	Destination     CircleTransferEndpoint `json:"destination"`
	Amount          CircleAmount           `json:"amount"`
	TransactionHash string                 `json:"transactionHash"`
	Status          string                 `json:"status"`
	CreateDate      string                 `json:"createDate"`
	ErrorCode       string                 `json:"errorCode,omitempty"`
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

// CircleTransactionNotification represents a Circle wallets transaction webhook payload.
type CircleTransactionNotification struct {
	ID                 string   `json:"id"`
	Blockchain         string   `json:"blockchain"`
	WalletID           string   `json:"walletId"`
	TokenID            string   `json:"tokenId"`
	DestinationAddress string   `json:"destinationAddress"`
	Amounts            []string `json:"amounts"`
	State              string   `json:"state"`
	TransactionType    string   `json:"transactionType"`
	ErrorCode          string   `json:"errorCode"`
	ErrorReason        string   `json:"errorReason"`
	TxHash             string   `json:"txHash"`
	TransactionHash    string   `json:"transactionHash"`
	CreateDate         string   `json:"createDate"`
	UpdateDate         string   `json:"updateDate"`
}

// ============================================================================
// ADDITIONAL WEBHOOK HANDLERS
// ============================================================================

// HandleWalletNotification handles Circle wallet notifications (dev-controlled wallets)
// POST /webhooks/circle/wallets
func (h *CircleWebhookHandler) HandleWalletNotification(c *gin.Context) {
	ctx := c.Request.Context()

	var rawBody []byte
	if c.Request.Body != nil {
		rawBody, _ = c.GetRawData()
	}

	if !h.devMode {
		keyID := c.GetHeader("X-Circle-Key-Id")
		signature := c.GetHeader("X-Circle-Signature")
		if keyID != "" && signature != "" {
			if !h.verifySignature(ctx, keyID, signature, rawBody) {
				h.logger.Error("Invalid Circle wallet webhook signature")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
				return
			}
		}
	}

	var webhook CircleTransferWebhook
	if err := json.Unmarshal(rawBody, &webhook); err != nil {
		h.logger.Error("Failed to parse Circle wallet webhook", "error", err)
		c.JSON(http.StatusOK, gin.H{"status": "error", "message": "invalid payload"})
		return
	}

	h.logger.Info("Received Circle wallet webhook",
		"notification_type", webhook.NotificationType)

	notificationType := strings.ToLower(strings.TrimSpace(webhook.NotificationType))
	if strings.HasPrefix(notificationType, "transactions.") || strings.HasPrefix(notificationType, "transfers.") {
		if err := h.processIncomingTransfer(ctx, &webhook); err != nil {
			h.logger.Error("Failed to process wallet notification", "error", err)
		}
	}

	// Always return 200 per Circle's 5-second timeout requirement.
	c.JSON(http.StatusOK, gin.H{"status": "received"})
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
