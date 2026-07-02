package funding

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/domain/services/ramp"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/ramphub"
	"go.uber.org/zap"
)

// RampHandlers exposes the unified (RampHub primary, Paj fallback) NGN on/off
// ramp endpoints. Users never pick a provider — the service routes to the best
// available rate automatically.
type RampHandlers struct {
	service *ramp.Service
	logger  *zap.Logger
}

func NewRampHandlers(service *ramp.Service, logger *zap.Logger) *RampHandlers {
	return &RampHandlers{service: service, logger: logger}
}

// GetQuote returns the single best rate for a corridor.
// GET /v1/funding/ramp/quote?side=onramp&amount=10000&currency=NGN
func (h *RampHandlers) GetQuote(c *gin.Context) {
	side := strings.ToLower(c.Query("side"))
	if side != "onramp" && side != "offramp" && side != "buy" && side != "sell" {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": "side must be onramp or offramp"})
		return
	}
	currency := c.Query("currency")
	if currency == "" {
		currency = "NGN"
	}
	amount := parseFloat(c.Query("amount"))
	if amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": "amount must be > 0"})
		return
	}

	// Both sides are NGN-denominated (the user enters the fiat amount). The
	// service derives the USDC side from the rate, so no token estimate is needed.
	quote, err := h.service.GetBestQuote(c.Request.Context(), side, amount, 0, currency)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "RATE_UNAVAILABLE", "message": "Exchange rate temporarily unavailable, please try again"})
		return
	}
	c.JSON(http.StatusOK, quote)
}

// GetBanks returns supported payout banks.
// GET /v1/funding/ramp/banks
func (h *RampHandlers) GetBanks(c *gin.Context) {
	banks, err := h.service.GetBanks(c.Request.Context())
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"banks": banks})
}

// ResolveBankAccount verifies a bank account name before an offramp. bankName
// is optional but improves matching because RampHub bank codes are
// provider-scoped.
// POST /v1/funding/ramp/banks/resolve
func (h *RampHandlers) ResolveBankAccount(c *gin.Context) {
	var req struct {
		BankCode      string `json:"bankCode" binding:"required"`
		AccountNumber string `json:"accountNumber" binding:"required"`
		BankName      string `json:"bankName"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": "bankCode and accountNumber required"})
		return
	}
	resolved, err := h.service.ResolveBankAccount(c.Request.Context(), req.BankCode, req.AccountNumber, req.BankName)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, resolved)
}

// CreateOnramp creates an NGN → USDC deposit order.
// POST /v1/funding/ramp/onramp
func (h *RampHandlers) CreateOnramp(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	var req struct {
		Amount   float64 `json:"amount" binding:"required,gt=0"`
		Currency string  `json:"currency"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": "amount is required"})
		return
	}

	res, err := h.service.CreateOnramp(c.Request.Context(), userID, req.Amount, req.Currency)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"transactionId": res.TransactionID,
		"provider":      res.Provider,
		"accountNumber": res.AccountNumber,
		"accountName":   res.AccountName,
		"bank":          res.Bank,
		"fiatAmount":    res.FiatAmount,
		"rate":          res.Rate,
		"asset":         res.Asset,
		"instructions":  "Transfer the exact amount to the bank account above. Your deposit will be credited automatically.",
	})
}

// CreateOfframp creates a USDC → NGN withdrawal order.
// POST /v1/funding/ramp/offramp
func (h *RampHandlers) CreateOfframp(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	var req struct {
		BankCode      string  `json:"bankCode" binding:"required"`
		AccountNumber string  `json:"accountNumber" binding:"required"`
		BankName      string  `json:"bankName"`
		Amount        float64 `json:"amount" binding:"required,gt=0"`
		Currency      string  `json:"currency"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": "bankCode, accountNumber, and amount required"})
		return
	}

	res, err := h.service.CreateOfframp(c.Request.Context(), userID, req.BankCode, req.AccountNumber, req.BankName, req.Amount, req.Currency)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"transactionId": res.TransactionID,
		"provider":      res.Provider,
		"fiatAmount":    res.FiatAmount,
		"rate":          res.Rate,
		"railFee":       res.RailFee,
		"status":        res.Status,
	})
}

// GetOrders returns the user's RampHub order history.
// GET /v1/funding/ramp/orders
func (h *RampHandlers) GetOrders(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	orders, err := h.service.GetOrders(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("failed to get ramphub orders", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": "FETCH_FAILED", "message": "Failed to fetch order history"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"orders": orders})
}

// GetOrderStatus polls RampHub for the latest order status.
// GET /v1/funding/ramp/orders/:id/status
func (h *RampHandlers) GetOrderStatus(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	txID := c.Param("id")
	if txID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": "order ID required"})
		return
	}
	tx, err := h.service.PollOrderStatus(c.Request.Context(), userID, txID)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"transactionId": tx.TransactionID,
		"status":        tx.MappedStatus(), // normalized (matches /orders history endpoint)
		"side":          tx.Side,
		"fiatAmount":    tx.FiatAmount,
		"tokenAmount":   tx.TokenAmount,
		"rate":          tx.Rate,
	})
}

// HandleWebhook verifies the RampHub signature and processes the event.
// POST /v1/webhooks/ramphub
func (h *RampHandlers) HandleWebhook(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	signature := c.GetHeader("x-ramphub-signature")
	if verr := ramphub.VerifyWebhookSignature(body, signature, h.service.WebhookSecret()); verr != nil {
		h.logger.Warn("ramphub webhook signature verification failed", zap.Error(verr))
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	var event ramphub.WebhookEvent
	if err := json.Unmarshal(body, &event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	if event.Data.TransactionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing transactionId"})
		return
	}

	// Reject sandbox events in non-sandbox mode to prevent test traffic from
	// crediting or reversing real accounts. Return 200 so RampHub does not retry.
	if !event.LiveMode && !h.service.IsSandbox() {
		h.logger.Warn("ramphub webhook: ignoring sandbox event in production mode",
			zap.String("event_id", event.ID),
			zap.String("event_type", event.Type),
			zap.String("ramphub_tx_id", event.Data.TransactionID))
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}

	deliveryID := c.GetHeader("x-ramphub-delivery")
	if err := h.service.HandleWebhook(c.Request.Context(), deliveryID, &event); err != nil {
		h.logger.Error("ramphub webhook processing failed", zap.Error(err), zap.String("ramphub_tx_id", event.Data.TransactionID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "processing failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// handleError maps service errors to API responses. Mirrors the classification
// in PajHandlers.handleSessionError.
func (h *RampHandlers) handleError(c *gin.Context, err error) {
	errMsg := err.Error()
	l := strings.ToLower(errMsg)

	switch {
	case errors.Is(err, ramphub.ErrAccountResolveFailed), strings.Contains(l, "unable to verify bank account"):
		c.JSON(http.StatusBadRequest, gin.H{"code": "BANK_RESOLVE_FAILED", "message": "We couldn't verify this account. Please check the account number and bank."})
	case strings.Contains(l, "account number must be"), strings.Contains(l, "invalid bank code"):
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": errMsg})
	case strings.Contains(l, "order not found"):
		c.JSON(http.StatusNotFound, gin.H{"code": "ORDER_NOT_FOUND", "message": "Order not found"})
	case strings.Contains(l, "limit exceeded"):
		c.JSON(http.StatusBadRequest, gin.H{"code": "LIMIT_EXCEEDED", "message": errMsg})
	case strings.Contains(l, "insufficient balance"):
		c.JSON(http.StatusBadRequest, gin.H{"code": "INSUFFICIENT_BALANCE", "message": "Insufficient balance for this withdrawal"})
	case strings.Contains(l, "minimum withdrawal"), strings.Contains(l, "minimum deposit"):
		c.JSON(http.StatusBadRequest, gin.H{"code": "BELOW_MINIMUM", "message": errMsg})
	case strings.Contains(l, "duplicate"), strings.Contains(l, "in progress"), strings.Contains(l, "already in progress"):
		c.JSON(http.StatusConflict, gin.H{"code": "DUPLICATE_REQUEST", "message": errMsg})
	case strings.Contains(l, "please wait a few seconds"):
		c.JSON(http.StatusTooManyRequests, gin.H{"code": "RATE_LIMITED", "message": errMsg})
	case strings.Contains(l, "rate unavailable"), strings.Contains(l, "rate out of bounds"), strings.Contains(l, "verify deposit limits"):
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "RATE_UNAVAILABLE", "message": "Exchange rate temporarily unavailable, please try again"})
	case strings.Contains(l, "no wallet available"):
		c.JSON(http.StatusBadRequest, gin.H{"code": "NO_WALLET", "message": "No wallet available to receive deposit"})
	default:
		var apiErr *ramphub.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			c.JSON(http.StatusBadRequest, gin.H{"code": "REQUEST_REJECTED", "message": "Request could not be processed, please check your details and try again"})
			return
		}
		h.logger.Error("ramphub operation failed", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"code": "RAMP_ERROR", "message": "Ramp service temporarily unavailable"})
	}
}

func parseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}
