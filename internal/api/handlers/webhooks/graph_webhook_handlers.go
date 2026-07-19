package webhooks

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/graph"
	"go.uber.org/zap"
)

// GraphWebhookHandler handles inbound Graph (useoval.com) webhooks: NGN account
// activation (issuance events) and NGN deposits (transaction events).
type GraphWebhookHandler struct {
	service       *funding.GraphVirtualAccountService
	webhookSecret string
	sandbox       bool
	logger        *zap.Logger
}

// NewGraphWebhookHandler constructs a Graph webhook handler.
func NewGraphWebhookHandler(service *funding.GraphVirtualAccountService, webhookSecret string, sandbox bool, logger *zap.Logger) *GraphWebhookHandler {
	return &GraphWebhookHandler{service: service, webhookSecret: webhookSecret, sandbox: sandbox, logger: logger}
}

// HandleWebhook verifies the signature and routes Graph events.
// POST /webhooks/graph
func (h *GraphWebhookHandler) HandleWebhook(c *gin.Context) {
	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	signature := c.GetHeader(graph.WebhookHeader)
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

	// In production, reject sandbox events to protect real accounts.
	if !event.LiveMode && !h.sandbox {
		h.logger.Warn("Ignoring Graph sandbox event in production", zap.String("type", event.Type))
		c.JSON(http.StatusOK, gin.H{"status": "ignored"})
		return
	}

	kind := strings.ToLower(event.Type + "." + event.Event)
	accountID := firstNonEmpty(event.Data.BankAccountID, event.Data.AccountID, event.Data.ID)

	switch {
	case isAccountActivation(kind, event.Data.Status):
		if accountID == "" {
			c.JSON(http.StatusOK, gin.H{"status": "ignored"})
			return
		}
		if err := h.service.HandleAccountActivated(c.Request.Context(), accountID); err != nil {
			h.logger.Error("Failed to activate Graph NGN account", zap.Error(err), zap.String("account_id", accountID))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "activation_failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "processed"})

	case isDepositEvent(kind, event.Data.Direction):
		if accountID == "" {
			c.JSON(http.StatusOK, gin.H{"status": "ignored"})
			return
		}
		depEvent := &funding.GraphNGNDepositEvent{
			GraphAccountID: accountID,
			TransactionID:  firstNonEmpty(event.Data.ID, event.Data.Reference),
			AmountNGN:      event.Data.Amount,
			Reference:      event.Data.Reference,
			Direction:      event.Data.Direction,
		}
		if err := h.service.ProcessNGNDeposit(c.Request.Context(), depEvent); err != nil {
			h.logger.Error("Failed to process Graph NGN deposit", zap.Error(err), zap.String("account_id", accountID))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "deposit_processing_failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "processed"})

	default:
		h.logger.Info("Unhandled Graph event", zap.String("type", event.Type), zap.String("event", event.Event))
		c.JSON(http.StatusOK, gin.H{"status": "ignored"})
	}
}

func isAccountActivation(kind, status string) bool {
	if strings.Contains(kind, "issuance") || strings.Contains(kind, "account") || strings.Contains(kind, "bank_account") {
		s := strings.ToLower(strings.TrimSpace(status))
		return s == "active" || s == "activated" || strings.Contains(kind, "activat")
	}
	return false
}

func isDepositEvent(kind, direction string) bool {
	if strings.EqualFold(direction, "credit") || strings.Contains(kind, "deposit") {
		if strings.Contains(kind, "transaction") || strings.Contains(kind, "deposit") || strings.EqualFold(direction, "credit") {
			return true
		}
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
