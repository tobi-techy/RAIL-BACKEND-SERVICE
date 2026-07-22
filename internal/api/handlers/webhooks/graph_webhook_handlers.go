package webhooks

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/graph"
	"go.uber.org/zap"
)

const maxWebhookBodySize = 1 << 20 // 1MB

// GraphWebhookHandler handles inbound Graph (useoval.com) webhooks: NGN account
// activation (issuance events) and NGN deposits (transaction events).
type GraphWebhookHandler struct {
	service       *funding.GraphVirtualAccountService
	webhookSecret string
	logger        *zap.Logger
}

// NewGraphWebhookHandler constructs a Graph webhook handler.
func NewGraphWebhookHandler(service *funding.GraphVirtualAccountService, webhookSecret string, logger *zap.Logger) *GraphWebhookHandler {
	return &GraphWebhookHandler{service: service, webhookSecret: webhookSecret, logger: logger}
}

// HandleWebhook verifies the signature and routes Graph events.
// POST /webhooks/graph
func (h *GraphWebhookHandler) HandleWebhook(c *gin.Context) {
	rawBody, err := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBodySize))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	signature := h.extractSignature(c)
	if err := graph.VerifyWebhookSignature(rawBody, signature, h.webhookSecret); err != nil {
		h.logger.Warn("Invalid Graph webhook signature", zap.Error(err))
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	var event graph.WebhookEvent
	if err := json.Unmarshal(rawBody, &event); err != nil {
		h.logger.Error("Failed to parse Graph webhook", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	h.logger.Info("Graph webhook received",
		zap.String("event_type", event.EventType),
		zap.String("entity", event.Entity),
		zap.String("data_id", event.Data.ID))

	switch event.Entity {
	case "bank_account":
		h.handleBankAccountEvent(c, &event)
	case "transaction":
		h.handleTransactionEvent(c, &event)
	case "card":
		h.handleCardEvent(c, &event)
	case "address":
		h.handleAddressEvent(c, &event)
	default:
		h.logger.Info("Unhandled Graph entity", zap.String("entity", event.Entity), zap.String("event_type", event.EventType))
		c.JSON(http.StatusOK, gin.H{"status": "ignored"})
	}
}

// extractSignature tries multiple known Graph webhook header names.
// Graph may use x-graph-signature, x-webhook-signature, or X-Signature
// depending on their API version. We try all known candidates.
func (h *GraphWebhookHandler) extractSignature(c *gin.Context) string {
	candidates := []string{
		"X-Graph-Signature",
		"X-Webhook-Signature",
		"X-Signature",
	}
	for _, name := range candidates {
		if sig := c.GetHeader(name); sig != "" {
			return sig
		}
	}
	return ""
}

func (h *GraphWebhookHandler) handleBankAccountEvent(c *gin.Context, event *graph.WebhookEvent) {
	switch event.EventType {
	case "account.created":
		if event.Data.ID == "" {
			c.JSON(http.StatusOK, gin.H{"status": "ignored"})
			return
		}
		if event.Data.AccountNumber == "" {
			h.logger.Info("Graph account.created without account number — ignoring (bank details not yet ready)",
				zap.String("account_id", event.Data.ID))
			c.JSON(http.StatusOK, gin.H{"status": "ignored"})
			return
		}
		// Use webhook data directly — no extra API call to Graph needed.
		var bankAddr *graph.BankAddress
		if event.Data.BankAddress != nil {
			bankAddr = &graph.BankAddress{
				City:       event.Data.BankAddress.City,
				Country:    event.Data.BankAddress.Country,
				Line1:      event.Data.BankAddress.Line1,
				Line2:      event.Data.BankAddress.Line2,
				PostalCode: event.Data.BankAddress.PostalCode,
				State:      event.Data.BankAddress.State,
			}
		}
		bankAcct := &graph.BankAccount{
			ID:            event.Data.ID,
			HolderID:      event.Data.HolderID,
			HolderType:    event.Data.HolderType,
			Label:         event.Data.Label,
			AccountName:   event.Data.AccountName,
			AccountNumber: event.Data.AccountNumber,
			RoutingNumber: event.Data.RoutingNumber,
			BankName:      event.Data.BankName,
			BankCode:      event.Data.BankCode,
			BankAddress:   bankAddr,
			Currency:      event.Data.Currency,
			Balance:       event.Data.Balance,
			Type:          event.Data.Type,
			Status:        event.Data.Status,
		}
		if err := h.service.HandleAccountActivatedWithData(c.Request.Context(), event.Data.ID, bankAcct); err != nil {
			h.logger.Error("Failed to activate Graph NGN account",
				zap.Error(err), zap.String("account_id", event.Data.ID))
			// Return 200 to prevent Graph retry storms on non-recoverable errors.
			c.JSON(http.StatusOK, gin.H{"status": "error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "processed"})

	case "account.issuance.failed":
		if event.Data.ID == "" {
			c.JSON(http.StatusOK, gin.H{"status": "ignored"})
			return
		}
		if err := h.service.HandleAccountIssuanceFailed(c.Request.Context(), event.Data.ID); err != nil {
			h.logger.Error("Failed to mark Graph account as failed",
				zap.Error(err), zap.String("account_id", event.Data.ID))
			// Return 200 to prevent Graph retry storms on non-recoverable errors.
			c.JSON(http.StatusOK, gin.H{"status": "error"})
			return
		}
		h.logger.Warn("Graph account issuance failed — account marked as failed",
			zap.String("account_id", event.Data.ID))
		c.JSON(http.StatusOK, gin.H{"status": "processed"})

	case "account.migrated":
		h.logger.Info("Graph account migrated",
			zap.String("account_id", event.Data.ID))
		c.JSON(http.StatusOK, gin.H{"status": "logged"})

	case "account.closed":
		if event.Data.ID == "" {
			c.JSON(http.StatusOK, gin.H{"status": "ignored"})
			return
		}
		if err := h.service.HandleAccountClosed(c.Request.Context(), event.Data.ID); err != nil {
			h.logger.Error("Failed to mark Graph account as closed",
				zap.Error(err), zap.String("account_id", event.Data.ID))
		}
		h.logger.Warn("Graph account closed",
			zap.String("account_id", event.Data.ID))
		c.JSON(http.StatusOK, gin.H{"status": "processed"})

	default:
		h.logger.Info("Unhandled bank_account event", zap.String("event_type", event.EventType))
		c.JSON(http.StatusOK, gin.H{"status": "ignored"})
	}
}

func (h *GraphWebhookHandler) handleTransactionEvent(c *gin.Context, event *graph.WebhookEvent) {
	switch event.EventType {
	case "account.credit":
		h.handleAccountCredit(c, event)

	case "payout.success":
		h.logger.Info("Graph payout success",
			zap.String("account_id", event.Data.AccountID),
			zap.Float64("amount", event.Data.Amount),
			zap.String("payout_id", event.Data.PayoutID))
		c.JSON(http.StatusOK, gin.H{"status": "logged"})

	case "payout.failed":
		h.logger.Warn("Graph payout failed",
			zap.String("account_id", event.Data.AccountID),
			zap.Float64("amount", event.Data.Amount),
			zap.String("payout_id", event.Data.PayoutID))
		c.JSON(http.StatusOK, gin.H{"status": "logged"})

	case "conversion.success":
		h.logger.Info("Graph conversion success",
			zap.String("conversion_id", event.Data.ConversionID),
			zap.String("account_id", event.Data.AccountID),
			zap.Float64("amount", event.Data.Amount))
		c.JSON(http.StatusOK, gin.H{"status": "logged"})

	case "conversion.failed":
		h.logger.Warn("Graph conversion failed",
			zap.String("conversion_id", event.Data.ConversionID),
			zap.String("account_id", event.Data.AccountID),
			zap.Float64("amount", event.Data.Amount))
		c.JSON(http.StatusOK, gin.H{"status": "logged"})

	default:
		h.logger.Info("Unhandled transaction event", zap.String("event_type", event.EventType))
		c.JSON(http.StatusOK, gin.H{"status": "ignored"})
	}
}

func (h *GraphWebhookHandler) handleCardEvent(c *gin.Context, event *graph.WebhookEvent) {
	switch event.EventType {
	case "card.created":
		h.logger.Info("Graph card created",
			zap.String("card_id", event.Data.ID),
			zap.String("holder_id", event.Data.HolderID))
	case "card.issuance.failed":
		h.logger.Warn("Graph card issuance failed",
			zap.String("card_id", event.Data.ID))
	case "card.frozen":
		h.logger.Warn("Graph card frozen",
			zap.String("card_id", event.Data.ID))
	case "card.closed":
		h.logger.Warn("Graph card closed",
			zap.String("card_id", event.Data.ID))
	default:
		h.logger.Info("Unhandled card event", zap.String("event_type", event.EventType))
	}
	c.JSON(http.StatusOK, gin.H{"status": "logged"})
}

func (h *GraphWebhookHandler) handleAddressEvent(c *gin.Context, event *graph.WebhookEvent) {
	switch event.EventType {
	case "address.migrated":
		h.logger.Warn("Graph deposit address migrated — old address replaced",
			zap.String("address_id", event.Data.ID))
	default:
		h.logger.Info("Unhandled address event", zap.String("event_type", event.EventType))
	}
	c.JSON(http.StatusOK, gin.H{"status": "logged"})
}

func (h *GraphWebhookHandler) handleAccountCredit(c *gin.Context, event *graph.WebhookEvent) {
	accountID := event.Data.AccountID
	if accountID == "" {
		h.logger.Warn("account.credit missing account_id")
		c.JSON(http.StatusOK, gin.H{"status": "ignored"})
		return
	}

	amount := event.Data.Amount
	if amount <= 0 {
		h.logger.Warn("account.credit with zero amount", zap.String("account_id", accountID))
		c.JSON(http.StatusOK, gin.H{"status": "ignored"})
		return
	}

	txRef := event.Data.ID
	if event.Data.Deposit != nil && event.Data.Deposit.ID != "" {
		txRef = event.Data.Deposit.ID
	}

	amountNGN := fmt.Sprintf("%.2f", amount/100.0)

	depEvent := &funding.GraphNGNDepositEvent{
		GraphAccountID: accountID,
		TransactionID:  txRef,
		AmountNGN:      amountNGN,
		Reference:      event.Data.Description,
		Direction:      "credit",
	}
	if err := h.service.ProcessNGNDeposit(c.Request.Context(), depEvent); err != nil {
		h.logger.Error("Failed to process Graph NGN deposit",
			zap.Error(err),
			zap.String("account_id", accountID),
			zap.String("tx_ref", txRef))
		// Return 200 to prevent Graph retry storms on non-recoverable errors.
		c.JSON(http.StatusOK, gin.H{"status": "error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "processed"})
}
