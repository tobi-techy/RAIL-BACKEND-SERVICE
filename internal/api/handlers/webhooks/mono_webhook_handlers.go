package webhooks

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	monosvc "github.com/rail-service/rail_service/internal/domain/services/mono"
	monoadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/mono"
	"go.uber.org/zap"
)

// MonoWebhookHandler handles inbound Mono webhooks for account lifecycle
// events, income analysis results, and DirectPay payment notifications.
//
// Mono sends events with the following names:
//   - mono.events.account_connected    — user linked their bank (account ID available, data may be PROCESSING)
//   - mono.events.account_updated       — data is ready (balance + transactions available)
//   - mono.events.account_income        — income analysis completed (streams, stability, employer)
//   - mono.events.account_reauthorized  — re-auth after session expiry
//   - mono.events.account_unlinked      — user or bank disconnected
//   - direct_debit.payment_successful   — DirectPay payment succeeded
//   - direct_debit.payment_failed        — DirectPay payment failed
type MonoWebhookHandler struct {
	service       *monosvc.Service
	webhookSecret string
	logger        *zap.Logger
}

func NewMonoWebhookHandler(service *monosvc.Service, webhookSecret string, logger *zap.Logger) *MonoWebhookHandler {
	return &MonoWebhookHandler{service: service, webhookSecret: webhookSecret, logger: logger}
}

// HandleWebhook processes inbound Mono webhook events.
// POST /webhooks/mono
func (h *MonoWebhookHandler) HandleWebhook(c *gin.Context) {
	rawBody, err := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBodySize))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	// Reject webhooks when no secret is configured — never process
	// unauthenticated payloads.
	if h.webhookSecret == "" {
		h.logger.Error("Mono webhook secret not configured; rejecting webhook")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "webhook verification not configured"})
		return
	}

	// Mono sends the dashboard-configured secret in the mono-webhook-secret
	// header. Compare in constant time to prevent timing oracles.
	provided := c.GetHeader("mono-webhook-secret")
	if subtle.ConstantTimeCompare([]byte(provided), []byte(h.webhookSecret)) != 1 {
		h.logger.Warn("Invalid Mono webhook signature")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	var event monoadapter.WebhookEvent
	if err := json.Unmarshal(rawBody, &event); err != nil {
		h.logger.Error("Failed to parse Mono webhook", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	if event.Event == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing event type"})
		return
	}

	h.logger.Info("Mono webhook received",
		zap.String("event", event.Event),
		zap.String("event_id", event.EventID))

	ctx := c.Request.Context()

	switch event.Event {

	// --- Account linking lifecycle ---

	case "mono.events.account_connected":
		// Account ID is in data.id. Data may still be PROCESSING.
		// The account_updated webhook will fire when data is ready.
		monoAccountID := event.Data.ID
		dataStatus := "unknown"
		if event.Data.Meta != nil {
			dataStatus = event.Data.Meta.DataStatus
		}
		h.logger.Info("Mono account connected",
			zap.String("mono_account_id", monoAccountID),
			zap.String("data_status", dataStatus))

	case "mono.events.account_updated":
		// Data is now available — log the account details and data status.
		acct := event.Data.AccountObject()
		if acct != nil {
			dataStatus := "unknown"
			if event.Data.Meta != nil {
				dataStatus = event.Data.Meta.DataStatus
			}
			institutionName := ""
			if acct.Institution != nil {
				institutionName = acct.Institution.Name
			}
			h.logger.Info("Mono account data ready",
				zap.String("mono_account_id", acct.ID),
				zap.String("data_status", dataStatus),
				zap.String("institution", institutionName),
				zap.Int64("balance", acct.Balance))
		}

	case "mono.events.account_income":
		// Income analysis completed — log the summary for now.
		// Future: persist income streams for Miriam's coaching context.
		accountName := event.Data.AccountName
		annualIncome := event.Data.AnnualIncome
		monthlyIncome := event.Data.MonthlyIncome
		streamCount := len(event.Data.IncomeStreams)
		h.logger.Info("Mono income analysis received",
			zap.String("account_name", accountName),
			zap.Int64("annual_income", annualIncome),
			zap.Int64("monthly_income", monthlyIncome),
			zap.Int("income_streams", streamCount))

	case "mono.events.account_reauthorized":
		monoAccountID := ""
		if acct := event.Data.AccountObject(); acct != nil {
			monoAccountID = acct.ID
		} else {
			monoAccountID = event.Data.AccountIDStr()
		}
		if monoAccountID == "" {
			monoAccountID = event.Data.ID
		}
		if err := h.service.HandleWebhook(ctx, "account_reauthorized", monoAccountID); err != nil {
			h.logger.Error("Failed to handle Mono reauth webhook", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "processing failed"})
			return
		}

	case "mono.events.account_unlinked":
		monoAccountID := ""
		if acct := event.Data.AccountObject(); acct != nil {
			monoAccountID = acct.ID
		} else {
			monoAccountID = event.Data.AccountIDStr()
		}
		if monoAccountID == "" {
			monoAccountID = event.Data.ID
		}
		if err := h.service.HandleWebhook(ctx, "account_unlinked", monoAccountID); err != nil {
			h.logger.Error("Failed to handle Mono unlink webhook", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "processing failed"})
			return
		}

	// --- DirectPay payment events ---

	case "direct_debit.payment_successful":
		if event.Data.Reference != "" {
			// Webhook is the trusted system caller (secret-verified above), so
			// use the reference-only variant — there is no user context here.
			if _, err := h.service.VerifyDepositByReference(ctx, event.Data.Reference); err != nil {
				h.logger.Error("Failed to verify Mono payment on webhook",
					zap.Error(err), zap.String("reference", event.Data.Reference))
			}
		}

	case "direct_debit.payment_failed":
		if event.Data.Reference != "" {
			if _, err := h.service.VerifyDepositByReference(ctx, event.Data.Reference); err != nil {
				h.logger.Error("Failed to verify failed Mono payment",
					zap.Error(err), zap.String("reference", event.Data.Reference))
			}
		}

	default:
		h.logger.Debug("Unhandled Mono webhook event", zap.String("event", event.Event))
	}

	c.JSON(http.StatusOK, gin.H{"status": "processed"})
}
