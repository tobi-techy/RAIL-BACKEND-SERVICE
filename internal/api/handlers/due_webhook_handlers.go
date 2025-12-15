package handlers

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

// DueWebhookService defines operations for processing Due events
type DueWebhookService interface {
	ProcessTransferCompleted(ctx gin.Context, transferID string, status string, amount decimal.Decimal) error
	ProcessTransferFailed(ctx gin.Context, transferID string, reason string) error
	ProcessDepositReceived(ctx gin.Context, virtualAccountID string, amount decimal.Decimal, txRef string) error
}

// DueWebhookHandler handles Due API webhook notifications
type DueWebhookHandler struct {
	service       DueWebhookService
	logger        *zap.Logger
	webhookSecret string
}

// NewDueWebhookHandler creates a new Due webhook handler
func NewDueWebhookHandler(service DueWebhookService, logger *zap.Logger, webhookSecret string) *DueWebhookHandler {
	return &DueWebhookHandler{service: service, logger: logger, webhookSecret: webhookSecret}
}

// DueWebhookPayload represents the Due webhook payload structure
type DueWebhookPayload struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// DueTransferEvent represents a transfer event from Due
type DueTransferEvent struct {
	ID          string `json:"id"`
	OwnerID     string `json:"ownerId"`
	Status      string `json:"status"`
	Source      DueLeg `json:"source"`
	Destination DueLeg `json:"destination"`
	CreatedAt   string `json:"createdAt"`
}

// DueLeg represents source or destination in a transfer
type DueLeg struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
	Rail     string `json:"rail"`
	ID       string `json:"id,omitempty"`
}

// DueDepositEvent represents a deposit event from Due
type DueDepositEvent struct {
	VirtualAccountID string `json:"virtualAccountId"`
	Amount           string `json:"amount"`
	Currency         string `json:"currency"`
	Reference        string `json:"reference"`
	TxRef            string `json:"txRef"`
	Status           string `json:"status"`
}

// HandleWebhook handles all Due webhook events
// POST /webhooks/due
func (h *DueWebhookHandler) HandleWebhook(c *gin.Context) {
	// Read raw body
	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.logger.Error("Failed to read webhook body", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	// Verify signature
	signature := c.GetHeader("X-Due-Signature")
	if !h.verifySignature(signature, rawBody) {
		h.logger.Warn("Invalid Due webhook signature")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	// Parse payload
	var payload DueWebhookPayload
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		h.logger.Error("Failed to parse webhook payload", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	h.logger.Info("Received Due webhook", zap.String("type", payload.Type))

	// Route by event type
	switch payload.Type {
	case "transfer.completed":
		h.handleTransferCompleted(c, payload.Data)
	case "transfer.failed":
		h.handleTransferFailed(c, payload.Data)
	case "deposit.received":
		h.handleDepositReceived(c, payload.Data)
	case "deposit.confirmed":
		h.handleDepositConfirmed(c, payload.Data)
	default:
		h.logger.Info("Unhandled Due event type", zap.String("type", payload.Type))
		c.JSON(http.StatusOK, gin.H{"status": "ignored"})
		return
	}
}

func (h *DueWebhookHandler) handleTransferCompleted(c *gin.Context, data json.RawMessage) {
	var event DueTransferEvent
	if err := json.Unmarshal(data, &event); err != nil {
		h.logger.Error("Failed to parse transfer event", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid transfer data"})
		return
	}

	amount, _ := decimal.NewFromString(event.Destination.Amount)

	if err := h.service.ProcessTransferCompleted(*c, event.ID, event.Status, amount); err != nil {
		h.logger.Error("Failed to process transfer completed",
			zap.String("transfer_id", event.ID),
			zap.Error(err))
		c.JSON(http.StatusOK, gin.H{"status": "error", "message": err.Error()})
		return
	}

	h.logger.Info("Transfer completed processed",
		zap.String("transfer_id", event.ID),
		zap.String("amount", amount.String()))

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *DueWebhookHandler) handleTransferFailed(c *gin.Context, data json.RawMessage) {
	var event DueTransferEvent
	if err := json.Unmarshal(data, &event); err != nil {
		h.logger.Error("Failed to parse transfer event", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid transfer data"})
		return
	}

	if err := h.service.ProcessTransferFailed(*c, event.ID, event.Status); err != nil {
		h.logger.Error("Failed to process transfer failed",
			zap.String("transfer_id", event.ID),
			zap.Error(err))
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *DueWebhookHandler) handleDepositReceived(c *gin.Context, data json.RawMessage) {
	var event DueDepositEvent
	if err := json.Unmarshal(data, &event); err != nil {
		h.logger.Error("Failed to parse deposit event", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid deposit data"})
		return
	}

	amount, _ := decimal.NewFromString(event.Amount)

	if err := h.service.ProcessDepositReceived(*c, event.VirtualAccountID, amount, event.TxRef); err != nil {
		h.logger.Error("Failed to process deposit received",
			zap.String("virtual_account_id", event.VirtualAccountID),
			zap.Error(err))
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *DueWebhookHandler) handleDepositConfirmed(c *gin.Context, data json.RawMessage) {
	// Same as deposit received but with confirmed status
	h.handleDepositReceived(c, data)
}

func (h *DueWebhookHandler) verifySignature(signature string, body []byte) bool {
	if h.webhookSecret == "" {
		h.logger.Warn("Due webhook secret not configured - skipping verification")
		return true
	}

	mac := hmac.New(sha256.New, []byte(h.webhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(signature))
}

// DueWebhookServiceImpl implements DueWebhookService
type DueWebhookServiceImpl struct {
	withdrawalRepo WithdrawalRepoForWebhook
	depositRepo    DepositRepoForWebhook
	notifier       WebhookNotifier
	logger         *zap.Logger
}

// WithdrawalRepoForWebhook defines withdrawal repository operations needed by webhook
type WithdrawalRepoForWebhook interface {
	GetByDueTransferID(ctx gin.Context, transferID string) (*WithdrawalRecord, error)
	MarkCompleted(ctx gin.Context, id uuid.UUID) error
	MarkFailed(ctx gin.Context, id uuid.UUID, reason string) error
}

// DepositRepoForWebhook defines deposit repository operations needed by webhook
type DepositRepoForWebhook interface {
	GetByVirtualAccountID(ctx gin.Context, vaID string) (*DepositRecord, error)
	UpdateStatus(ctx gin.Context, id uuid.UUID, status string) error
}

// WebhookNotifier defines notification operations
type WebhookNotifier interface {
	NotifyWithdrawalCompleted(ctx gin.Context, userID uuid.UUID, amount, address string) error
	NotifyWithdrawalFailed(ctx gin.Context, userID uuid.UUID, amount, reason string) error
	NotifyDepositReceived(ctx gin.Context, userID uuid.UUID, amount string) error
}

// WithdrawalRecord represents a withdrawal for webhook processing
type WithdrawalRecord struct {
	ID                 uuid.UUID
	UserID             uuid.UUID
	Amount             decimal.Decimal
	DestinationAddress string
}

// DepositRecord represents a deposit for webhook processing
type DepositRecord struct {
	ID     uuid.UUID
	UserID uuid.UUID
	Amount decimal.Decimal
}

// NewDueWebhookService creates a new Due webhook service
func NewDueWebhookService(
	withdrawalRepo WithdrawalRepoForWebhook,
	depositRepo DepositRepoForWebhook,
	notifier WebhookNotifier,
	logger *zap.Logger,
) *DueWebhookServiceImpl {
	return &DueWebhookServiceImpl{
		withdrawalRepo: withdrawalRepo,
		depositRepo:    depositRepo,
		notifier:       notifier,
		logger:         logger,
	}
}

func (s *DueWebhookServiceImpl) ProcessTransferCompleted(ctx gin.Context, transferID string, status string, amount decimal.Decimal) error {
	withdrawal, err := s.withdrawalRepo.GetByDueTransferID(ctx, transferID)
	if err != nil {
		return err
	}
	if withdrawal == nil {
		s.logger.Warn("Withdrawal not found for transfer", zap.String("transfer_id", transferID))
		return nil
	}

	if err := s.withdrawalRepo.MarkCompleted(ctx, withdrawal.ID); err != nil {
		return err
	}

	if s.notifier != nil {
		_ = s.notifier.NotifyWithdrawalCompleted(ctx, withdrawal.UserID, withdrawal.Amount.String(), withdrawal.DestinationAddress)
	}

	return nil
}

func (s *DueWebhookServiceImpl) ProcessTransferFailed(ctx gin.Context, transferID string, reason string) error {
	withdrawal, err := s.withdrawalRepo.GetByDueTransferID(ctx, transferID)
	if err != nil {
		return err
	}
	if withdrawal == nil {
		return nil
	}

	if err := s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, reason); err != nil {
		return err
	}

	if s.notifier != nil {
		_ = s.notifier.NotifyWithdrawalFailed(ctx, withdrawal.UserID, withdrawal.Amount.String(), reason)
	}

	return nil
}

func (s *DueWebhookServiceImpl) ProcessDepositReceived(ctx gin.Context, virtualAccountID string, amount decimal.Decimal, txRef string) error {
	deposit, err := s.depositRepo.GetByVirtualAccountID(ctx, virtualAccountID)
	if err != nil {
		return err
	}
	if deposit == nil {
		s.logger.Warn("Deposit not found for virtual account", zap.String("va_id", virtualAccountID))
		return nil
	}

	if err := s.depositRepo.UpdateStatus(ctx, deposit.ID, "confirmed"); err != nil {
		return err
	}

	if s.notifier != nil {
		_ = s.notifier.NotifyDepositReceived(ctx, deposit.UserID, amount.String())
	}

	return nil
}
