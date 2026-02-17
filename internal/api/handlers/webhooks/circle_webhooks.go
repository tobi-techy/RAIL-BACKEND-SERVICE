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
	"time"

	"github.com/gin-gonic/gin"
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

// CircleWebhookHandler handles Circle API webhook notifications
type CircleWebhookHandler struct {
	fundingService    CircleDepositProcessor
	managedWalletRepo CircleManagedWalletRepository
	logger            *logger.Logger
	circleAPIKey      string // For fetching public keys
	circleBaseURL     string
}

// NewCircleWebhookHandler creates a new Circle webhook handler
func NewCircleWebhookHandler(
	fundingService CircleDepositProcessor,
	managedWalletRepo CircleManagedWalletRepository,
	logger *logger.Logger,
	circleAPIKey string,
	circleBaseURL string,
) *CircleWebhookHandler {
	return &CircleWebhookHandler{
		fundingService:    fundingService,
		managedWalletRepo: managedWalletRepo,
		logger:            logger,
		circleAPIKey:      circleAPIKey,
		circleBaseURL:     circleBaseURL,
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

	// Verify webhook signature using Circle's ECDSA verification
	keyID := c.GetHeader("X-Circle-Key-Id")
	signature := c.GetHeader("X-Circle-Signature")

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

	// Process based on notification type.
	// Circle can emit variants such as transfers.* and transactions.*.
	switch {
	case (strings.HasPrefix(strings.ToLower(webhook.NotificationType), "transfers.") ||
		strings.HasPrefix(strings.ToLower(webhook.NotificationType), "transactions.")) &&
		!strings.HasSuffix(strings.ToLower(webhook.NotificationType), ".failed"):
		if err := h.processIncomingTransfer(ctx, &webhook); err != nil {
			h.logger.Error("Failed to process incoming transfer",
				"transfer_id", webhook.TransferID,
				"error", err)
			// Return 200 to prevent retries for processing errors
			// Store failure for manual review
			c.JSON(http.StatusOK, gin.H{"status": "error", "message": err.Error()})
			return
		}

	case strings.HasSuffix(strings.ToLower(webhook.NotificationType), ".failed"):
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
	notificationType := strings.ToLower(strings.TrimSpace(webhook.NotificationType))
	if strings.HasPrefix(notificationType, "transactions.") {
		return h.processIncomingTransactionNotification(ctx, webhook)
	}

	transfer := webhook.Transfer

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
		chain = entities.ChainSolana
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
		chain = entities.ChainSolana
	}

	// Map token ID to token symbol (default to USDC for now)
	token := h.mapTokenIDToToken(n.TokenID)

	address := strings.TrimSpace(n.DestinationAddress)
	if address == "" && strings.TrimSpace(n.WalletID) != "" {
		managedWallet, err := h.managedWalletRepo.GetByCircleWalletID(ctx, n.WalletID)
		if err == nil {
			address = managedWallet.Address
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

// verifySignature verifies the Circle webhook signature using ECDSA-SHA256
func (h *CircleWebhookHandler) verifySignature(ctx context.Context, keyID, signature string, body []byte) bool {
	// Skip verification in dev mode if API key is not configured
	if h.circleAPIKey == "" {
		h.logger.Warn("Circle API key not configured - skipping signature verification")
		return true
	}

	// Fetch the public key from Circle API
	publicKeyBase64, err := h.fetchPublicKey(ctx, keyID)
	if err != nil {
		h.logger.Error("Failed to fetch Circle public key", "error", err, "key_id", keyID)
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

// fetchPublicKey fetches the public key from Circle API
func (h *CircleWebhookHandler) fetchPublicKey(ctx context.Context, keyID string) (string, error) {
	url := fmt.Sprintf("%s/v2/notifications/publicKey/%s", h.circleBaseURL, keyID)

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
	case "SOL", "SOLANA", "SOL-DEVNET":
		return entities.ChainSolana
	case "MATIC", "POLYGON":
		return entities.ChainMATIC
	case "ETH", "ETHEREUM", "ETH-SEPOLIA":
		return entities.ChainETH
	case "AVAX", "AVALANCHE":
		return entities.ChainAVAX
	case "BASE":
		return entities.ChainBASE
	case "ARB", "ARBITRUM":
		return entities.ChainARB
	case "OP", "OPTIMISM":
		return entities.ChainOP
	default:
		if circleChain != "" {
			h.logger.Warn("Unknown Circle chain", "chain", circleChain)
		}
		return ""
	}
}

func (h *CircleWebhookHandler) mapWalletChainToChain(chain entities.WalletChain) entities.Chain {
	switch chain {
	case entities.WalletChainSOLDevnet, entities.WalletChainSolana:
		return entities.ChainSolana
	case entities.WalletChainPolygon:
		return entities.ChainMATIC
	case entities.WalletChainEthereum:
		return entities.ChainETH
	case entities.WalletChainAvalanche:
		return entities.ChainAVAX
	case entities.WalletChainBase:
		return entities.ChainBASE
	case entities.WalletChainArbitrum:
		return entities.ChainARB
	case entities.WalletChainOptimism:
		return entities.ChainOP
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
	TxHash             string   `json:"txHash"`
	TransactionHash    string   `json:"transactionHash"`
	CreateDate         string   `json:"createDate"`
	UpdateDate         string   `json:"updateDate"`
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
