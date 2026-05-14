package funding

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/pajfunding"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/paj"
	"go.uber.org/zap"
)

// PajHandlers handles Paj Cash NGN on/off ramp endpoints.
type PajHandlers struct {
	service *pajfunding.Service
	logger  *zap.Logger
}

// requireUserID extracts and validates the authenticated user ID.
func requireUserID(c *gin.Context) (uuid.UUID, bool) {
	val, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "AUTH_REQUIRED", "message": "Valid authentication required"})
		return uuid.Nil, false
	}
	switch v := val.(type) {
	case uuid.UUID:
		if v == uuid.Nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "AUTH_REQUIRED", "message": "Valid authentication required"})
			return uuid.Nil, false
		}
		return v, true
	case string:
		parsed, err := uuid.Parse(v)
		if err != nil || parsed == uuid.Nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "AUTH_REQUIRED", "message": "Valid authentication required"})
			return uuid.Nil, false
		}
		return parsed, true
	default:
		c.JSON(http.StatusUnauthorized, gin.H{"error": "AUTH_REQUIRED", "message": "Valid authentication required"})
		return uuid.Nil, false
	}
}

func NewPajHandlers(service *pajfunding.Service, logger *zap.Logger) *PajHandlers {
	return &PajHandlers{service: service, logger: logger}
}

// Initiate triggers a Paj OTP to the user's email.
// POST /v1/funding/paj/initiate
func (h *PajHandlers) Initiate(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	userEmail := c.GetString("user_email")
	if userEmail == "" {
		userEmail = c.GetString("email") // fallback: device-bound auth middleware uses "email"
	}
	if userEmail == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "MISSING_EMAIL", "message": "User email required"})
		return
	}

	alreadyVerified, err := h.service.Initiate(c.Request.Context(), userID, userEmail)
	if err != nil {
		h.logger.Error("paj initiate failed", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "PAJ_INITIATE_FAILED", "message": "Failed to send verification code"})
		return
	}

	if alreadyVerified {
		c.JSON(http.StatusOK, gin.H{"status": "already_verified"})
	} else {
		c.JSON(http.StatusOK, gin.H{"status": "otp_sent", "email": maskEmail(userEmail)})
	}
}

// Verify confirms the OTP and caches the Paj session.
// POST /v1/funding/paj/verify
func (h *PajHandlers) Verify(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	userEmail := c.GetString("user_email")
	if userEmail == "" {
		userEmail = c.GetString("email")
	}
	if userEmail == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "MISSING_EMAIL", "message": "User email required"})
		return
	}

	var req struct {
		OTP string `json:"otp" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": "OTP is required"})
		return
	}

	deviceID := c.GetString("device_id")
	if deviceID == "" {
		deviceID = c.GetHeader("X-Device-Fingerprint")
	}

	if err := h.service.Verify(c.Request.Context(), userID, userEmail, req.OTP, deviceID); err != nil {
		h.logger.Warn("paj verify failed", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusUnauthorized, gin.H{"error": "PAJ_VERIFY_FAILED", "message": "Invalid or expired verification code"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "verified"})
}

// GetRates returns current NGN/USD exchange rates.
// GET /v1/funding/paj/rates
func (h *PajHandlers) GetRates(c *gin.Context) {
	rates, err := h.service.GetRates(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "PAJ_RATES_FAILED", "message": "Failed to fetch rates"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"onRampRate":       rates.OnRampRate,
		"offRampRate":      rates.OffRampRate,
		"railFee":          pajfunding.RailNGNWithdrawalFee,
		"minWithdrawalNGN": pajfunding.MinNGNTransactionAmount,
	})
}

// GetBanks returns the list of Nigerian banks.
// GET /v1/funding/paj/banks
func (h *PajHandlers) GetBanks(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	banks, err := h.service.GetBanks(c.Request.Context(), userID)
	if err != nil {
		h.handleSessionError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"banks": banks})
}

// ResolveBankAccount verifies a bank account name.
// POST /v1/funding/paj/banks/resolve
func (h *PajHandlers) ResolveBankAccount(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	var req struct {
		BankID        string `json:"bankId" binding:"required"`
		AccountNumber string `json:"accountNumber" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": "bankId and accountNumber required"})
		return
	}

	resolved, err := h.service.ResolveBankAccount(c.Request.Context(), userID, req.BankID, req.AccountNumber)
	if err != nil {
		h.handleSessionError(c, err)
		return
	}
	c.JSON(http.StatusOK, resolved)
}

// AddBankAccount saves a bank account for future withdrawals.
// POST /v1/funding/paj/banks/add
func (h *PajHandlers) AddBankAccount(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	var req struct {
		BankID        string `json:"bankId" binding:"required"`
		AccountNumber string `json:"accountNumber" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": "bankId and accountNumber required"})
		return
	}

	account, err := h.service.AddBankAccount(c.Request.Context(), userID, req.BankID, req.AccountNumber)
	if err != nil {
		h.handleSessionError(c, err)
		return
	}
	c.JSON(http.StatusOK, account)
}

// GetBankAccounts returns the user's saved bank accounts.
// GET /v1/funding/paj/banks/saved
func (h *PajHandlers) GetBankAccounts(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	accounts, err := h.service.GetBankAccounts(c.Request.Context(), userID)
	if err != nil {
		h.handleSessionError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"accounts": accounts})
}

// CreateOnramp creates an NGN → USDC deposit order.
// POST /v1/funding/paj/onramp
func (h *PajHandlers) CreateOnramp(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	var req struct {
		Amount   float64 `json:"amount" binding:"required,gt=0"`
		Currency string  `json:"currency"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": "amount is required"})
		return
	}
	if req.Currency == "" {
		req.Currency = "NGN"
	}

	order, err := h.service.CreateOnrampOrder(c.Request.Context(), userID, req.Amount, req.Currency)
	if err != nil {
		h.handleSessionError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"orderId":       order.ID,
		"accountNumber": order.AccountNumber,
		"accountName":   order.AccountName,
		"bank":          order.Bank,
		"fiatAmount":    order.FiatAmount,
		"tokenAmount":   order.Amount,
		"fee":           order.Fee,
		"instructions":  "Transfer the exact amount to the bank account above. Your deposit will be credited automatically.",
	})
}

// CreateOfframp creates a USDC → NGN withdrawal order.
// POST /v1/funding/paj/offramp
func (h *PajHandlers) CreateOfframp(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	var req struct {
		BankID        string  `json:"bankId" binding:"required"`
		AccountNumber string  `json:"accountNumber" binding:"required"`
		Amount        float64 `json:"amount" binding:"required,gt=0"`
		Currency      string  `json:"currency"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": "bankId, accountNumber, and amount required"})
		return
	}
	if req.Currency == "" {
		req.Currency = "NGN"
	}

	order, err := h.service.CreateOfframpOrder(c.Request.Context(), userID, req.BankID, req.AccountNumber, req.Amount, req.Currency)
	if err != nil {
		h.handleSessionError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"orderId":    order.Order.ID,
		"fiatAmount": order.Order.FiatAmount,
		"amount":     order.Order.Amount,
		"rate":       order.Order.Rate,
		"fee":        order.Order.Fee,
		"railFee":    order.RailFee,
		"status":     "pending",
	})
}

// HandleWebhook processes Paj order status webhooks.
// Since Paj doesn't sign webhooks, the service verifies by polling Paj's API.
// POST /v1/webhooks/paj
func (h *PajHandlers) HandleWebhook(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	var payload paj.WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	if payload.ID == "" || payload.Status == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing required fields"})
		return
	}

	if err := h.service.HandleWebhook(c.Request.Context(), &payload); err != nil {
		h.logger.Error("paj webhook processing failed", zap.Error(err), zap.String("paj_order_id", payload.ID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "processing failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// GetOrders returns the user's Paj order history.
// GET /v1/funding/paj/orders
func (h *PajHandlers) GetOrders(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	orders, err := h.service.GetOrders(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("failed to get paj orders", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "FETCH_FAILED", "message": "Failed to fetch order history"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"orders": orders})
}

// GetOrderStatus polls Paj for current order status.
// GET /v1/funding/paj/orders/:id/status
func (h *PajHandlers) GetOrderStatus(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	orderID := c.Param("id")
	if orderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": "order ID required"})
		return
	}

	tx, err := h.service.PollOrderStatus(c.Request.Context(), userID, orderID)
	if err != nil {
		h.handleSessionError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"orderId":    tx.ID,
		"status":     tx.Status,
		"amount":     tx.Amount,
		"fiatAmount": tx.FiatAmount,
		"rate":       tx.Rate,
		"type":       tx.TransactionType,
	})
}

// handleSessionError classifies service errors into appropriate HTTP responses.
// Uses {"code", "message"} format so the frontend transformError extracts err.code correctly.
func (h *PajHandlers) handleSessionError(c *gin.Context, err error) {
	errMsg := err.Error()
	errLower := strings.ToLower(errMsg)

	// Auth / session errors → 403
	var pajErr *paj.APIError
	if strings.Contains(errLower, "no paj session") ||
		strings.Contains(errLower, "paj session expired") ||
		(errors.As(err, &pajErr) && pajErr.StatusCode == http.StatusUnauthorized) {
		c.JSON(http.StatusForbidden, gin.H{
			"code":    "PAJ_VERIFICATION_REQUIRED",
			"message": "Please verify your identity to enable NGN transactions",
		})
		return
	}

	// Not found → 404
	if strings.Contains(errMsg, "order not found") {
		c.JSON(http.StatusNotFound, gin.H{
			"code":    "ORDER_NOT_FOUND",
			"message": "Order not found",
		})
		return
	}

	// User-actionable / validation errors → 400
	switch {
	case errors.Is(err, entities.ErrDailyDepositExceeded),
		errors.Is(err, entities.ErrMonthlyDepositExceeded),
		strings.Contains(errLower, "daily deposit limit exceeded"),
		strings.Contains(errLower, "monthly deposit limit exceeded"),
		strings.Contains(errLower, "deposit limit exceeded"):
		c.JSON(http.StatusBadRequest, gin.H{"code": "LIMIT_EXCEEDED", "message": errMsg})
		return
	case errors.Is(err, entities.ErrDailyWithdrawalExceeded),
		errors.Is(err, entities.ErrMonthlyWithdrawalExceeded),
		strings.Contains(errLower, "daily withdrawal limit exceeded"),
		strings.Contains(errLower, "monthly withdrawal limit exceeded"),
		strings.Contains(errLower, "withdrawal limit exceeded"):
		c.JSON(http.StatusBadRequest, gin.H{"code": "LIMIT_EXCEEDED", "message": errMsg})
		return
	case strings.Contains(errLower, "insufficient balance"):
		c.JSON(http.StatusBadRequest, gin.H{"code": "INSUFFICIENT_BALANCE", "message": "Insufficient balance for this withdrawal"})
		return
	case strings.Contains(errLower, "minimum withdrawal") || strings.Contains(errLower, "minimum deposit"):
		c.JSON(http.StatusBadRequest, gin.H{"code": "BELOW_MINIMUM", "message": errMsg})
		return
	case strings.Contains(errLower, "duplicate withdrawal") || strings.Contains(errLower, "withdrawal in progress"):
		c.JSON(http.StatusConflict, gin.H{"code": "DUPLICATE_REQUEST", "message": errMsg})
		return
	case strings.Contains(errLower, "deposit already in progress"):
		c.JSON(http.StatusConflict, gin.H{"code": "DUPLICATE_REQUEST", "message": errMsg})
		return
	case strings.Contains(errLower, "please wait a few seconds"):
		c.JSON(http.StatusTooManyRequests, gin.H{"code": "RATE_LIMITED", "message": errMsg})
		return
	case strings.Contains(errLower, "unable to verify deposit limits"):
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "RATE_UNAVAILABLE", "message": errMsg})
		return
	case strings.Contains(errLower, "failed to create deposit order"):
		c.JSON(http.StatusInternalServerError, gin.H{"code": "ORDER_FAILED", "message": errMsg})
		return
	case strings.Contains(errLower, "offramp rate unavailable") || strings.Contains(errLower, "offramp rate out of bounds"):
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "RATE_UNAVAILABLE", "message": "Exchange rate temporarily unavailable, please try again"})
		return
	}

	// Bridge 4xx errors are user/config problems, not upstream failures.
	var bridgeErr *bridge.ErrorResponse
	if errors.As(err, &bridgeErr) && bridgeErr.StatusCode >= 400 && bridgeErr.StatusCode < 500 {
		h.logger.Warn("paj operation failed: bridge client error", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"code": "WITHDRAWAL_REJECTED", "message": "Withdrawal could not be processed, please check your details and try again"})
		return
	}

	// Everything else is a genuine upstream failure → 502
	h.logger.Error("paj operation failed", zap.Error(err))
	c.JSON(http.StatusBadGateway, gin.H{"code": "PAJ_ERROR", "message": "NGN service temporarily unavailable"})
}

func maskEmail(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 1 {
		return email
	}
	return email[:1] + "***" + email[at:]
}
