package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// BridgeWebhookService defines operations for processing Bridge events
type BridgeWebhookService interface {
	ProcessFiatDeposit(ctx *gin.Context, event *BridgeDepositEvent) error
	ProcessTransferCompleted(ctx *gin.Context, transferID string, amount decimal.Decimal) error
	ProcessCustomerStatusChanged(ctx *gin.Context, customerID string, status string) error
	// Card transaction methods
	ProcessCardAuthorization(ctx *gin.Context, cardID string, amount decimal.Decimal, merchantName, merchantCategory string) error
	ProcessCardTransaction(ctx *gin.Context, cardID, transID string, amount decimal.Decimal, merchantName, merchantCategory, status string) error
	ProcessCardTransactionDeclined(ctx *gin.Context, cardID, transID, declineReason string) error
	ProcessCardStatusChanged(ctx *gin.Context, cardID, status string) error
}

// BridgeWebhookHandler handles Bridge API webhook notifications
type BridgeWebhookHandler struct {
	service                    BridgeWebhookService
	logger                     *zap.Logger
	webhookSecret              string
	skipWebhookVerification    bool // Explicit opt-out flag for development/testing only
}

// NewBridgeWebhookHandler creates a new Bridge webhook handler
// skipWebhookVerification should only be true in development/testing environments
func NewBridgeWebhookHandler(service BridgeWebhookService, logger *zap.Logger, webhookSecret string, skipWebhookVerification bool) *BridgeWebhookHandler {
	return &BridgeWebhookHandler{
		service:                 service,
		logger:                  logger,
		webhookSecret:           webhookSecret,
		skipWebhookVerification: skipWebhookVerification,
	}
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
	signature := c.GetHeader("Bridge-Signature")
	if !h.verifySignature(signature, rawBody) {
		h.logger.Warn("Invalid Bridge webhook signature")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
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
		zap.String("event_category", payload.EventCategory))

	// Route by event type
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
		h.logger.Info("Unhandled Bridge event type", zap.String("event_type", payload.EventType))
		c.JSON(http.StatusOK, gin.H{"status": "ignored"})
		return
	}
}

func (h *BridgeWebhookHandler) handleDepositReceived(c *gin.Context, payload BridgeWebhookPayload) {
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
		// Return 200 to prevent retries for business logic errors
		c.JSON(http.StatusOK, gin.H{"status": "error", "message": err.Error()})
		return
	}

	h.logger.Info("Fiat deposit processed",
		zap.String("virtual_account_id", event.VirtualAccountID),
		zap.String("amount", event.Amount))

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *BridgeWebhookHandler) handleTransferCompleted(c *gin.Context, payload BridgeWebhookPayload) {
	transferID := payload.EventObjectID

	var amount decimal.Decimal
	if amountStr, ok := payload.EventObject["amount"].(string); ok {
		var err error
		amount, err = decimal.NewFromString(amountStr)
		if err != nil {
			h.logger.Error("Failed to parse transfer amount",
				zap.String("transfer_id", transferID),
				zap.String("raw_amount", amountStr),
				zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid amount format"})
			return
		}
	}

	if err := h.service.ProcessTransferCompleted(c, transferID, amount); err != nil {
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

	// Log for monitoring, but acknowledge receipt
	c.JSON(http.StatusOK, gin.H{"status": "acknowledged"})
}

func (h *BridgeWebhookHandler) handleCustomerStatusChanged(c *gin.Context, payload BridgeWebhookPayload) {
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
			h.logger.Warn("Bridge webhook secret not configured - SKIPPING VERIFICATION (development mode)")
			return true
		}
		h.logger.Error("Bridge webhook secret not configured - rejecting webhook for security")
		return false
	}

	mac := hmac.New(sha256.New, []byte(h.webhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(signature))
}

// BridgeWebhookServiceImpl implements BridgeWebhookService
type BridgeWebhookServiceImpl struct {
	virtualAccountService BridgeVirtualAccountProcessor
	customerService       BridgeCustomerProcessor
	cardService           BridgeCardProcessor
	notifier              BridgeWebhookNotifier
	logger                *zap.Logger
}

// BridgeVirtualAccountProcessor processes virtual account events
type BridgeVirtualAccountProcessor interface {
	ProcessFiatDeposit(ctx *gin.Context, event *BridgeDepositEvent) error
}

// BridgeCustomerProcessor processes customer events
type BridgeCustomerProcessor interface {
	UpdateCustomerStatus(ctx *gin.Context, customerID string, status string) error
}

// BridgeCardProcessor processes card events
type BridgeCardProcessor interface {
	ProcessAuthorization(ctx *gin.Context, cardID string, amount decimal.Decimal, merchantName, merchantCategory string) error
	RecordTransaction(ctx *gin.Context, cardID, transactionID string, amount decimal.Decimal, merchantName, merchantCategory, status string) error
	RecordDeclinedTransaction(ctx *gin.Context, cardID, transactionID, declineReason string) error
	SyncCardStatus(ctx *gin.Context, cardID, status string) error
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
	notifier BridgeWebhookNotifier,
	logger *zap.Logger,
) *BridgeWebhookServiceImpl {
	return &BridgeWebhookServiceImpl{
		virtualAccountService: virtualAccountService,
		customerService:       customerService,
		cardService:           cardService,
		notifier:              notifier,
		logger:                logger,
	}
}

func (s *BridgeWebhookServiceImpl) ProcessFiatDeposit(ctx *gin.Context, event *BridgeDepositEvent) error {
	return s.virtualAccountService.ProcessFiatDeposit(ctx, event)
}

func (s *BridgeWebhookServiceImpl) ProcessTransferCompleted(ctx *gin.Context, transferID string, amount decimal.Decimal) error {
	s.logger.Info("Transfer completed", zap.String("transfer_id", transferID), zap.String("amount", amount.String()))
	return nil
}

func (s *BridgeWebhookServiceImpl) ProcessCustomerStatusChanged(ctx *gin.Context, customerID string, status string) error {
	if s.customerService != nil {
		return s.customerService.UpdateCustomerStatus(ctx, customerID, status)
	}
	return nil
}

// Card processing methods - wired to CardService

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
