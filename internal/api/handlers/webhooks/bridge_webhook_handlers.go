package webhooks

import (
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
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
		zap.String("event_category", payload.EventCategory),
		zap.String("event_object_status", payload.EventObjectStatus))

	// Route by event category (new Bridge format) or event type (legacy)
	switch payload.EventCategory {
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

	default:
		// Fallback to legacy event_type routing for backwards compatibility
		h.handleLegacyEventType(c, payload)
	}
}

// handleVirtualAccountActivity processes virtual_account.activity events (fiat deposits)
func (h *BridgeWebhookHandler) handleVirtualAccountActivity(c *gin.Context, payload BridgeWebhookPayload) {
	eventType := payload.EventObject["type"]
	h.logger.Info("Virtual account activity",
		zap.String("activity_type", fmt.Sprintf("%v", eventType)),
		zap.String("event_type", payload.EventType))

	// Extract deposit details
	event := &BridgeDepositEvent{
		VirtualAccountID: getStringField(payload.EventObject, "virtual_account_id"),
		CustomerID:       getStringField(payload.EventObject, "customer_id"),
		Amount:           getStringField(payload.EventObject, "amount"),
		Currency:         getStringField(payload.EventObject, "currency"),
		TransactionRef:   getStringField(payload.EventObject, "deposit_id"),
		Status:           fmt.Sprintf("%v", eventType),
	}

	if err := h.service.ProcessFiatDeposit(c, event); err != nil {
		h.logger.Error("Failed to process virtual account activity",
			zap.String("virtual_account_id", event.VirtualAccountID),
			zap.Error(err))
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

	switch state {
	case "payment_processed", "funds_received":
		var amount decimal.Decimal
		if amountStr := getStringField(payload.EventObject, "amount"); amountStr != "" {
			amount, _ = decimal.NewFromString(amountStr)
		}
		if err := h.service.ProcessTransferCompleted(c, transferID, amount); err != nil {
			h.logger.Error("Failed to process transfer completed", zap.Error(err))
		}
	case "failed", "returned":
		h.logger.Warn("Transfer failed/returned",
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

	if err := h.service.ProcessCustomerStatusChanged(c, customerID, status); err != nil {
		h.logger.Error("Failed to process customer event", zap.Error(err))
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// handleKYCLinkEvent processes kyc_link events
func (h *BridgeWebhookHandler) handleKYCLinkEvent(c *gin.Context, payload BridgeWebhookPayload) {
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
	transactionID := payload.EventObjectID
	status := payload.EventObjectStatus
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
		declineReason := getStringField(payload.EventObject, "decline_reason")
		if err := h.service.ProcessCardTransactionDeclined(c, cardAccountID, transactionID, declineReason); err != nil {
			h.logger.Error("Failed to process declined transaction", zap.Error(err))
		}
	case "pending":
		if err := h.service.ProcessCardAuthorization(c, cardAccountID, amount, merchantName, merchantCategory); err != nil {
			h.logger.Error("Failed to process card authorization", zap.Error(err))
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
			h.logger.Warn("Bridge webhook public key not configured - SKIPPING VERIFICATION (development mode)")
			return true
		}
		h.logger.Error("Bridge webhook public key not configured - rejecting webhook for security")
		return false
	}

	// Bridge uses RSA-SHA256 signatures with PEM-encoded public key
	if strings.Contains(h.webhookSecret, "BEGIN PUBLIC KEY") {
		return h.verifyRSASignature(signature, body)
	}
	
	// Fallback to HMAC for backwards compatibility
	mac := hmac.New(sha256.New, []byte(h.webhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(signature))
}

// verifyRSASignature verifies Bridge webhook using RSA public key
func (h *BridgeWebhookHandler) verifyRSASignature(signatureHeader string, body []byte) bool {
	// Parse signature header: "t=<timestamp>,v1=<base64-signature>"
	var timestamp, sig string
	parts := strings.Split(signatureHeader, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "t=") {
			timestamp = strings.TrimPrefix(part, "t=")
		} else if strings.HasPrefix(part, "v1=") {
			sig = strings.TrimPrefix(part, "v1=")
		}
	}

	if sig == "" {
		// Try treating entire header as signature (some formats)
		sig = signatureHeader
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

	// Decode base64 signature
	sigBytes, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		// Try URL-safe base64
		sigBytes, err = base64.URLEncoding.DecodeString(sig)
		if err != nil {
			h.logger.Error("Failed to decode signature", zap.Error(err))
			return false
		}
	}

	// Bridge signs: timestamp + "." + body (or just body)
	var signedPayload []byte
	if timestamp != "" {
		signedPayload = []byte(timestamp + "." + string(body))
	} else {
		signedPayload = body
	}

	// Hash the payload
	hashed := sha256.Sum256(signedPayload)

	// Verify RSA-SHA256 signature
	err = rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, hashed[:], sigBytes)
	if err != nil {
		h.logger.Warn("Bridge webhook signature verification failed", zap.Error(err))
		return false
	}

	// Optional: Check timestamp to prevent replay attacks (within 5 minutes)
	if timestamp != "" {
		ts, err := strconv.ParseInt(timestamp, 10, 64)
		if err == nil {
			eventTime := time.Unix(ts, 0)
			if time.Since(eventTime) > 5*time.Minute {
				h.logger.Warn("Bridge webhook timestamp too old", zap.Time("event_time", eventTime))
				return false
			}
		}
	}

	return true
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


// Helper functions for extracting fields from event objects

// getStringField safely extracts a string field from a map
func getStringField(obj map[string]interface{}, key string) string {
	if val, ok := obj[key]; ok {
		if str, ok := val.(string); ok {
			return str
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
