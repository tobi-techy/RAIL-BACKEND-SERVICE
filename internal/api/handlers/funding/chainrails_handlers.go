package funding

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
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
	crClient       *chainrails.Client
	fundingService *funding.Service
	webhookSecret  string
	logger         *logger.Logger
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

	// Look up user's Bridge custody wallet address as the ChainRails recipient.
	// ChainRails will bridge funds to this address on the destination chain.
	depositAddr, err := h.fundingService.CreateDepositAddress(c.Request.Context(), userID, entities.ChainBase)
	if err != nil {
		chainrailsSessionsTotal.WithLabelValues("wallet_error").Inc()
		h.logger.Error("Failed to get user deposit address", "user_id", userID, "error", err)
		common.SendInternalError(c, "WALLET_ERROR", "Could not resolve deposit address")
		return
	}

	session, err := h.crClient.CreateSession(c.Request.Context(), &chainrails.CreateSessionRequest{
		Amount:    req.Amount,
		Recipient: depositAddr.Address,
	})
	if err != nil {
		chainrailsSessionsTotal.WithLabelValues("session_error").Inc()
		h.logger.Error("ChainRails session creation failed", "user_id", userID, "error", err)
		common.SendInternalError(c, "SESSION_ERROR", "Failed to create payment session")
		return
	}

	chainrailsSessionsTotal.WithLabelValues("success").Inc()
	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"session_token": session.SessionToken,
			"expires_at":    session.ExpiresAt,
		},
	})
}

// --- POST /v1/webhooks/chainrails ---

func (h *ChainRailsHandlers) HandleWebhook(c *gin.Context) {
	rawBody, err := c.GetRawData()
	if err != nil {
		chainrailsWebhooksTotal.WithLabelValues("unknown", "bad_request").Inc()
		common.RespondBadRequest(c, "Failed to read body", nil)
		return
	}

	sig := c.GetHeader("X-Chainrails-Signature")
	ts := c.GetHeader("X-Chainrails-Timestamp")

	if err := chainrails.VerifyWebhookSignature(rawBody, sig, ts, h.webhookSecret); err != nil {
		chainrailsWebhooksTotal.WithLabelValues("unknown", "unauthorized").Inc()
		h.logger.Warn("ChainRails webhook signature invalid", "error", err)
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
		h.handleIntentCompleted(c, &event)
	default:
		// Acknowledge but ignore other events
		chainrailsWebhooksTotal.WithLabelValues(event.Type, "ignored").Inc()
		h.logger.Info("ChainRails webhook ignored", "type", event.Type, "id", event.ID)
		c.JSON(http.StatusOK, gin.H{"received": true})
	}
}

func (h *ChainRailsHandlers) handleIntentCompleted(c *gin.Context, event *chainrails.WebhookEvent) {
	data := event.Data

	// Map ChainRails intent to a standard chain deposit webhook
	// so it flows through the existing deposit → allocation pipeline.
	deposit := &entities.ChainDepositWebhook{
		CorrelationID: event.ID, // ChainRails event ID for idempotency tracing
		Chain:         mapChainRailsChain(data.DestinationChain),
		Address:       data.Recipient,
		Token:         mapToken(data.TokenOut),
		Amount:        data.Amount,
		TxHash:        data.TxHash,
		BlockTime:     time.Now(),
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

// mapToken converts a token contract address or symbol to Rail's Stablecoin type.
func mapToken(tokenOut string) entities.Stablecoin {
	lower := strings.ToLower(tokenOut)
	if strings.Contains(lower, "usdt") {
		return entities.StablecoinUSDT
	}
	return entities.StablecoinUSDC // default — Rail settles in USDC
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
	case "BNB_MAINNET":
		return entities.ChainBNB
	default:
		return entities.ChainBase
	}
}
