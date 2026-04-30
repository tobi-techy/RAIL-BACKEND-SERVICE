package funding

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
)

var (
	chainrailsSessionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rail_chainrails_sessions_total",
			Help: "Total number of ChainRails sessions created",
		},
		[]string{"status"},
	)
	chainrailsWebhooksTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rail_chainrails_webhooks_total",
			Help: "Total number of ChainRails webhooks received",
		},
		[]string{"type", "status"},
	)
	chainrailsSessionDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name: "rail_chainrails_session_duration_seconds",
			Help: "Duration of ChainRails session creation",
		},
	)
)

// ChainRailsHandlers handles ChainRails deposit flow.
type ChainRailsHandlers struct {
	crClient          *chainrails.Client
	fundingService    *funding.Service
	withdrawalService ChainRailsWithdrawalService
	walletLookup      ChainRailsWalletLookup
	webhookSecret     string
	logger            *logger.Logger
}

// ChainRailsWalletLookup resolves a Circle wallet address for a user+chain.
type ChainRailsWalletLookup interface {
	GetWalletByUserAndChain(ctx context.Context, userID uuid.UUID, chain entities.WalletChain) (*entities.ManagedWallet, error)
	CreateWalletsForUser(ctx context.Context, userID uuid.UUID, chains []entities.WalletChain) error
}

// ChainRailsWithdrawalService is the subset of withdrawal service needed for webhook handling.
type ChainRailsWithdrawalService interface {
	CompleteChainRailsWithdrawal(ctx context.Context, intentID int, txHash string) error
	RefundChainRailsWithdrawal(ctx context.Context, intentID int) error
}

func NewChainRailsHandlers(
	crClient *chainrails.Client,
	fundingService *funding.Service,
	webhookSecret string,
	logger *logger.Logger,
) *ChainRailsHandlers {
	return &ChainRailsHandlers{
		crClient:       crClient,
		fundingService: fundingService,
		webhookSecret:  webhookSecret,
		logger:         logger,
	}
}

// SetWithdrawalService wires the withdrawal service for webhook handling.
func (h *ChainRailsHandlers) SetWithdrawalService(ws ChainRailsWithdrawalService) {
	h.withdrawalService = ws
}

// SetWalletLookup wires the wallet service for deposit address resolution.
func (h *ChainRailsHandlers) SetWalletLookup(wl ChainRailsWalletLookup) {
	h.walletLookup = wl
}

// --- POST /v1/funding/chainrails/session ---

type createSessionReq struct {
	Amount string `json:"amount" binding:"required"`
}

func (h *ChainRailsHandlers) CreateSession(c *gin.Context) {
	start := time.Now()
	defer func() {
		chainrailsSessionDuration.Observe(time.Since(start).Seconds())
	}()

	userID, err := common.GetUserID(c)
	if err != nil {
		chainrailsSessionsTotal.WithLabelValues("unauthorized").Inc()
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	var req createSessionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		chainrailsSessionsTotal.WithLabelValues("bad_request").Inc()
		common.RespondBadRequest(c, "Invalid request", nil)
		return
	}

	// Validate amount
	amt, err := decimal.NewFromString(req.Amount)
	if err != nil || amt.LessThanOrEqual(decimal.Zero) {
		chainrailsSessionsTotal.WithLabelValues("invalid_amount").Inc()
		common.SendBadRequest(c, "INVALID_AMOUNT", "Amount must be a positive number")
		return
	}

	// Look up user's Circle wallet address as the ChainRails recipient.
	// ChainRails will bridge funds to this address on the destination chain.
	var depositAddress string
	if h.walletLookup != nil {
		chain := entities.WalletChainBase
		w, wErr := h.walletLookup.GetWalletByUserAndChain(c.Request.Context(), userID, chain)
		if wErr != nil || w == nil || w.Address == "" {
			// Try creating the wallet
			if cErr := h.walletLookup.CreateWalletsForUser(c.Request.Context(), userID, []entities.WalletChain{chain}); cErr != nil {
				chainrailsSessionsTotal.WithLabelValues("wallet_error").Inc()
				h.logger.Error("Failed to create Circle wallet for ChainRails", "user_id", userID, "error", cErr)
				common.SendInternalError(c, "WALLET_ERROR", "Could not resolve deposit address")
				return
			}
			w, wErr = h.walletLookup.GetWalletByUserAndChain(c.Request.Context(), userID, chain)
			if wErr != nil || w == nil || w.Address == "" {
				chainrailsSessionsTotal.WithLabelValues("wallet_error").Inc()
				h.logger.Error("Failed to get wallet after creation", "user_id", userID, "error", wErr)
				common.SendInternalError(c, "WALLET_ERROR", "Could not resolve deposit address")
				return
			}
		}
		depositAddress = w.Address
	} else {
		chainrailsSessionsTotal.WithLabelValues("wallet_error").Inc()
		h.logger.Error("Wallet lookup not configured")
		common.SendInternalError(c, "WALLET_ERROR", "Wallet service not available")
		return
	}

	session, err := h.crClient.CreateSessionRaw(c.Request.Context(), &chainrails.CreateSessionRequest{
		Recipient:        depositAddress,
		TokenOut:         "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913", // USDC on Base
		DestinationChain: "BASE_MAINNET",
		Amount:           req.Amount,
	})
	if err != nil {
		chainrailsSessionsTotal.WithLabelValues("session_error").Inc()
		h.logger.Error("ChainRails session creation failed", "user_id", userID, "error", err)
		common.SendInternalError(c, "SESSION_ERROR", "Failed to create payment session")
		return
	}

	chainrailsSessionsTotal.WithLabelValues("success").Inc()
	c.Data(http.StatusOK, "application/json", session)
}

// --- POST /v1/webhooks/chainrails ---

func (h *ChainRailsHandlers) HandleWebhook(c *gin.Context) {
	rawBody, err := c.GetRawData()
	if err != nil {
		chainrailsWebhooksTotal.WithLabelValues("unknown", "bad_request").Inc()
		common.RespondBadRequest(c, "Failed to read body", nil)
		return
	}

	// Check payload size limit (1MB)
	const maxPayloadSize = 1024 * 1024 // 1MB
	if len(rawBody) > maxPayloadSize {
		chainrailsWebhooksTotal.WithLabelValues("unknown", "payload_too_large").Inc()
		h.logger.Warn("ChainRails webhook payload too large", 
			"size", len(rawBody), 
			"max_size", maxPayloadSize,
			"client_ip", c.ClientIP(),
			"user_agent", c.GetHeader("User-Agent"))
		common.RespondBadRequest(c, "Payload too large", nil)
		return
	}

	sig := c.GetHeader("X-Chainrails-Signature")
	ts := c.GetHeader("X-Chainrails-Timestamp")

	if err := chainrails.VerifyWebhookSignature(rawBody, sig, ts, h.webhookSecret); err != nil {
		chainrailsWebhooksTotal.WithLabelValues("unknown", "unauthorized").Inc()
		h.logger.Warn("ChainRails webhook signature invalid", 
			"error", err,
			"client_ip", c.ClientIP(),
			"user_agent", c.GetHeader("User-Agent"))
		common.SendUnauthorized(c, "Invalid webhook signature")
		return
	}

	var event chainrails.WebhookEvent
	if err := json.Unmarshal(rawBody, &event); err != nil {
		chainrailsWebhooksTotal.WithLabelValues("unknown", "bad_request").Inc()
		common.RespondBadRequest(c, "Invalid payload", nil)
		return
	}

	switch event.Type {
	case "intent.completed":
		chainrailsWebhooksTotal.WithLabelValues("intent.completed", "received").Inc()
		// Check metadata to distinguish deposit vs withdrawal intents
		if event.Data.Metadata != nil {
			if _, isWithdrawal := event.Data.Metadata["withdrawal_id"]; isWithdrawal {
				h.handleWithdrawalIntentCompleted(c, &event)
				return
			}
		}
		h.handleIntentCompleted(c, &event)
	case "intent.refunded":
		chainrailsWebhooksTotal.WithLabelValues("intent.refunded", "received").Inc()
		if event.Data.Metadata != nil {
			if _, isWithdrawal := event.Data.Metadata["withdrawal_id"]; isWithdrawal {
				h.handleWithdrawalIntentRefunded(c, &event)
				return
			}
		}
		h.logger.Info("ChainRails deposit refund received", "event_id", event.ID)
		c.JSON(http.StatusOK, gin.H{"received": true})
	case "intent.funded", "intent.initiated":
		chainrailsWebhooksTotal.WithLabelValues(event.Type, "acknowledged").Inc()
		h.logger.Info("ChainRails intent progress", "type", event.Type, "event_id", event.ID,
			"intent_address", event.Data.IntentAddress)
		c.JSON(http.StatusOK, gin.H{"received": true})
	default:
		chainrailsWebhooksTotal.WithLabelValues("other", "ignored").Inc()
		h.logger.Info("ChainRails webhook ignored", "type", event.Type, "id", event.ID)
		c.JSON(http.StatusOK, gin.H{"received": true})
	}
}

func (h *ChainRailsHandlers) handleIntentCompleted(c *gin.Context, event *chainrails.WebhookEvent) {
	data := event.Data

	// Parse block time from event data or fall back to event creation time
	var blockTime time.Time
	if data.Metadata != nil {
		if completedAt, exists := data.Metadata["completed_at"]; exists {
			if parsed, err := time.Parse(time.RFC3339, completedAt); err == nil {
				blockTime = parsed
			}
		}
	}
	// Fall back to event creation time if no block timestamp available
	if blockTime.IsZero() {
		if parsed, err := time.Parse(time.RFC3339, event.CreatedAt); err == nil {
			blockTime = parsed
		} else {
			// Last resort: use current time but log the limitation
			blockTime = time.Now()
			h.logger.Warn("Using server time for block timestamp - no timestamp in ChainRails event",
				"event_id", event.ID)
		}
	}

	// Map ChainRails intent to a standard chain deposit webhook
	// so it flows through the existing deposit → allocation pipeline.
	deposit := &entities.ChainDepositWebhook{
		CorrelationID: event.ID, // ChainRails event ID for idempotency tracing
		Chain:         mapChainRailsChain(data.DestinationChain),
		Address:       data.Recipient,
		Token:         mapToken(data.TokenOut),
		Amount:        data.Amount,
		TxHash:        data.TxHash,
		BlockTime:     blockTime,
	}

	if err := h.fundingService.ProcessChainDeposit(c.Request.Context(), deposit); err != nil {
		h.logger.Error("Failed to process ChainRails deposit",
			"event_id", event.ID,
			"intent_address", data.IntentAddress,
			"tx_hash", data.TxHash,
			"error", err,
		)
		if isAlreadyProcessed(err) {
			chainrailsWebhooksTotal.WithLabelValues("intent.completed", "already_processed").Inc()
			c.JSON(http.StatusOK, gin.H{"received": true, "status": "already_processed"})
			return
		}
		chainrailsWebhooksTotal.WithLabelValues("intent.completed", "processing_error").Inc()
		common.SendInternalError(c, "PROCESSING_ERROR", "Failed to process deposit")
		return
	}

	chainrailsWebhooksTotal.WithLabelValues("intent.completed", "success").Inc()
	h.logger.Info("ChainRails deposit processed",
		"event_id", event.ID,
		"intent_address", data.IntentAddress,
		"source_chain", data.SourceChain,
		"amount", data.Amount,
		"tx_hash", data.TxHash,
	)
	c.JSON(http.StatusOK, gin.H{"received": true})
}

func isAlreadyProcessed(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "already processed") || strings.Contains(msg, "deposit already exists")
}

func (h *ChainRailsHandlers) handleWithdrawalIntentCompleted(c *gin.Context, event *chainrails.WebhookEvent) {
	if h.withdrawalService == nil {
		h.logger.Error("Withdrawal service not configured for ChainRails webhook")
		c.JSON(http.StatusOK, gin.H{"received": true})
		return
	}

	if err := h.withdrawalService.CompleteChainRailsWithdrawal(c.Request.Context(), event.Data.IntentID, event.Data.TxHash); err != nil {
		if isAlreadyProcessed(err) {
			chainrailsWebhooksTotal.WithLabelValues("intent.completed", "already_processed").Inc()
			c.JSON(http.StatusOK, gin.H{"received": true, "status": "already_processed"})
			return
		}
		if strings.Contains(err.Error(), "not found") {
			chainrailsWebhooksTotal.WithLabelValues("intent.completed", "not_found").Inc()
			h.logger.Warn("ChainRails withdrawal not found — acknowledging to prevent retries",
				"event_id", event.ID, "intent_id", event.Data.IntentID, "error", err)
			c.JSON(http.StatusOK, gin.H{"received": true, "status": "not_found"})
			return
		}
		h.logger.Error("Failed to complete ChainRails withdrawal",
			"event_id", event.ID, "intent_id", event.Data.IntentID, "error", err)
		chainrailsWebhooksTotal.WithLabelValues("intent.completed", "processing_error").Inc()
		common.SendInternalError(c, "PROCESSING_ERROR", "Failed to process withdrawal completion")
		return
	}

	chainrailsWebhooksTotal.WithLabelValues("intent.completed", "withdrawal_success").Inc()
	c.JSON(http.StatusOK, gin.H{"received": true})
}

func (h *ChainRailsHandlers) handleWithdrawalIntentRefunded(c *gin.Context, event *chainrails.WebhookEvent) {
	if h.withdrawalService == nil {
		h.logger.Error("Withdrawal service not configured for ChainRails webhook")
		c.JSON(http.StatusOK, gin.H{"received": true})
		return
	}

	if err := h.withdrawalService.RefundChainRailsWithdrawal(c.Request.Context(), event.Data.IntentID); err != nil {
		if strings.Contains(err.Error(), "not found") {
			chainrailsWebhooksTotal.WithLabelValues("intent.refunded", "not_found").Inc()
			h.logger.Warn("ChainRails withdrawal not found for refund — acknowledging",
				"event_id", event.ID, "intent_id", event.Data.IntentID)
			c.JSON(http.StatusOK, gin.H{"received": true, "status": "not_found"})
			return
		}
		h.logger.Error("Failed to refund ChainRails withdrawal",
			"event_id", event.ID, "intent_id", event.Data.IntentID, "error", err)
		chainrailsWebhooksTotal.WithLabelValues("intent.refunded", "processing_error").Inc()
		common.SendInternalError(c, "PROCESSING_ERROR", "Failed to process withdrawal refund")
		return
	}

	chainrailsWebhooksTotal.WithLabelValues("intent.refunded", "withdrawal_refunded").Inc()
	c.JSON(http.StatusOK, gin.H{"received": true})
}

// mapToken converts a token contract address or symbol to Rail's Stablecoin type.
func mapToken(tokenOut string) entities.Stablecoin {
	lower := strings.ToLower(tokenOut)
	switch {
	case strings.Contains(lower, "usdt"):
		return entities.StablecoinUSDT
	case strings.Contains(lower, "eurc"):
		return entities.StablecoinEURC
	case strings.Contains(lower, "pyusd"):
		return entities.StablecoinPYUSD
	case strings.Contains(lower, "usdg"):
		return entities.StablecoinUSDG
	default:
		return entities.StablecoinUSDC // default — Rail settles in USDC
	}
}

// mapChainRailsChain converts ChainRails chain names to Rail's Chain type.
func mapChainRailsChain(cr string) entities.Chain {
	switch cr {
	case "ETHEREUM_MAINNET", "ETHEREUM_TESTNET":
		return entities.ChainETH
	case "BASE_MAINNET", "BASE_TESTNET":
		return entities.ChainBase
	case "ARBITRUM_MAINNET", "ARBITRUM_TESTNET":
		return entities.ChainArbitrum
	case "POLYGON_MAINNET":
		return entities.ChainMATIC
	case "OPTIMISM_MAINNET", "OPTIMISM_TESTNET":
		return entities.ChainOptimism
	case "AVALANCHE_MAINNET", "AVALANCHE_TESTNET":
		return entities.ChainAvalanche
	case "STARKNET_MAINNET", "STARKNET_TESTNET":
		return entities.ChainStarknet
	case "BNB_MAINNET", "BSC_MAINNET":
		return entities.ChainBNB
	default:
		return entities.ChainBase
	}
}
