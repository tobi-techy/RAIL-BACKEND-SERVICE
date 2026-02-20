package wallet

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// WithdrawalServiceInterface defines the interface for withdrawal operations
type WithdrawalServiceInterface interface {
	InitiateCryptoWithdrawal(ctx context.Context, req *entities.InitiateCryptoWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error)
	InitiateFiatWithdrawal(ctx context.Context, req *entities.InitiateFiatWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error)
	GetWithdrawal(ctx context.Context, userID, withdrawalID uuid.UUID) (*entities.Withdrawal, error)
	GetUserWithdrawals(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Withdrawal, error)
	CancelWithdrawal(ctx context.Context, userID, withdrawalID uuid.UUID) error
	GetWithdrawalFee(ctx context.Context, withdrawalType entities.WithdrawalType, amount decimal.Decimal, currency entities.WithdrawalCurrency) (*entities.WithdrawalFee, error)
}

// WalletProvider interface for getting user's Circle wallet
type WalletProvider interface {
	GetUserWalletByChain(ctx context.Context, userID uuid.UUID, chain string) (*entities.ManagedWallet, error)
}

// WithdrawalHandlers handles withdrawal-related operations
type WithdrawalHandlers struct {
	withdrawalService WithdrawalServiceInterface
	walletProvider    WalletProvider
	validator         *validator.Validate
	logger            *logger.Logger
}

// NewWithdrawalHandlers creates a new WithdrawalHandlers instance
func NewWithdrawalHandlers(withdrawalService WithdrawalServiceInterface, walletProvider WalletProvider, logger *logger.Logger) *WithdrawalHandlers {
	return &WithdrawalHandlers{
		withdrawalService: withdrawalService,
		walletProvider:    walletProvider,
		validator:         validator.New(),
		logger:            logger,
	}
}

// CryptoWithdrawalRequest represents the HTTP request for crypto withdrawal
// Only amount and destination_address required - chain defaults to Solana, wallet fetched from backend
type CryptoWithdrawalRequest struct {
	Amount             float64 `json:"amount" binding:"required,gt=0"`
	DestinationAddress string  `json:"destination_address" binding:"required"`
}

// FiatWithdrawalRequest represents the HTTP request for fiat withdrawal
// Only requires routing number - bank account created during withdrawal process
type FiatWithdrawalRequest struct {
	Amount        float64 `json:"amount" binding:"required,gt=0"`
	Currency      string  `json:"currency" binding:"required,oneof=USD EUR"`
	RoutingNumber string  `json:"routing_number" binding:"required,len=9"`
}

// WithdrawalFeeRequest represents the HTTP request for fee calculation
type WithdrawalFeeRequest struct {
	WithdrawalType string  `form:"type" binding:"required,oneof=crypto fiat"`
	Amount         float64 `form:"amount" binding:"required,gt=0"`
	Currency       string  `form:"currency" binding:"required,oneof=USDC USD EUR"`
}

// InitiateCryptoWithdrawal handles POST /api/v1/withdrawals/crypto
func (h *WithdrawalHandlers) InitiateCryptoWithdrawal(c *gin.Context) {
	var req CryptoWithdrawalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Invalid request format: "+err.Error())
		return
	}

	userID, ok := h.extractUserID(c)
	if !ok {
		return
	}

	// Get user's Solana wallet from backend (only Solana supported for now)
	wallet, err := h.walletProvider.GetUserWalletByChain(c.Request.Context(), userID, "SOL")
	if err != nil {
		h.logger.Error("Failed to get user wallet", "error", err, "user_id", userID)
		common.SendBadRequest(c, "NO_WALLET", "No Solana wallet found for user")
		return
	}

	serviceReq := &entities.InitiateCryptoWithdrawalRequest{
		UserID:             userID,
		Amount:             decimal.NewFromFloat(req.Amount),
		DestinationAddress: req.DestinationAddress,
		DestinationChain:   string(wallet.Chain),
		SourceAccount:      entities.WithdrawalSourceSpendingBalance, // Default to spending
		CircleWalletID:     wallet.CircleWalletID,
	}

	response, err := h.withdrawalService.InitiateCryptoWithdrawal(c.Request.Context(), serviceReq)
	if err != nil {
		h.handleWithdrawalError(c, err, userID, req.Amount)
		return
	}

	c.JSON(http.StatusOK, response)
}

// InitiateFiatWithdrawal handles POST /api/v1/withdrawals/fiat
func (h *WithdrawalHandlers) InitiateFiatWithdrawal(c *gin.Context) {
	var req FiatWithdrawalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Invalid request format: "+err.Error())
		return
	}

	userID, ok := h.extractUserID(c)
	if !ok {
		return
	}

	currency := entities.WithdrawalCurrencyUSD
	if req.Currency == "EUR" {
		currency = entities.WithdrawalCurrencyEUR
	}

	serviceReq := &entities.InitiateFiatWithdrawalRequest{
		UserID:        userID,
		Amount:        decimal.NewFromFloat(req.Amount),
		Currency:      currency,
		RoutingNumber: req.RoutingNumber,
		SourceAccount: entities.WithdrawalSourceSpendingBalance, // Default to spending
	}

	response, err := h.withdrawalService.InitiateFiatWithdrawal(c.Request.Context(), serviceReq)
	if err != nil {
		h.handleWithdrawalError(c, err, userID, req.Amount)
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetWithdrawalFees handles GET /api/v1/withdrawals/fees
func (h *WithdrawalHandlers) GetWithdrawalFees(c *gin.Context) {
	var req WithdrawalFeeRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Invalid request: "+err.Error())
		return
	}

	withdrawalType := entities.WithdrawalTypeCrypto
	if req.WithdrawalType == "fiat" {
		withdrawalType = entities.WithdrawalTypeFiat
	}

	currency := entities.WithdrawalCurrencyUSDC
	switch req.Currency {
	case "USD":
		currency = entities.WithdrawalCurrencyUSD
	case "EUR":
		currency = entities.WithdrawalCurrencyEUR
	}

	fee, err := h.withdrawalService.GetWithdrawalFee(
		c.Request.Context(),
		withdrawalType,
		decimal.NewFromFloat(req.Amount),
		currency,
	)
	if err != nil {
		h.logger.Error("Failed to get withdrawal fee", "error", err)
		common.SendInternalError(c, "FEE_CALCULATION_ERROR", "Failed to calculate fee")
		return
	}

	c.JSON(http.StatusOK, fee)
}

// GetWithdrawal handles GET /api/v1/withdrawals/:withdrawalId
func (h *WithdrawalHandlers) GetWithdrawal(c *gin.Context) {
	userID, ok := h.extractUserID(c)
	if !ok {
		return
	}

	withdrawalID, err := uuid.Parse(c.Param("withdrawalId"))
	if err != nil {
		common.SendBadRequest(c, "INVALID_WITHDRAWAL_ID", "Invalid withdrawal ID format")
		return
	}

	withdrawal, err := h.withdrawalService.GetWithdrawal(c.Request.Context(), userID, withdrawalID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			common.SendNotFound(c, common.ErrCodeWithdrawalNotFound, "Withdrawal not found")
			return
		}
		if strings.Contains(err.Error(), "does not belong") {
			common.SendNotFound(c, common.ErrCodeWithdrawalNotFound, "Withdrawal not found")
			return
		}
		h.logger.Error("Failed to get withdrawal", "error", err, "withdrawal_id", withdrawalID)
		common.SendInternalError(c, "WITHDRAWAL_ERROR", "Failed to retrieve withdrawal")
		return
	}

	c.JSON(http.StatusOK, withdrawal)
}

// GetUserWithdrawals handles GET /api/v1/withdrawals
func (h *WithdrawalHandlers) GetUserWithdrawals(c *gin.Context) {
	userID, ok := h.extractUserID(c)
	if !ok {
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	if limit > 100 {
		limit = 100
	}

	withdrawals, err := h.withdrawalService.GetUserWithdrawals(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.logger.Error("Failed to get user withdrawals", "error", err, "user_id", userID)
		common.SendInternalError(c, "WITHDRAWAL_ERROR", "Failed to retrieve withdrawals")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"withdrawals": withdrawals,
		"count":       len(withdrawals),
	})
}

// CancelWithdrawal handles DELETE /api/v1/withdrawals/:withdrawalId
func (h *WithdrawalHandlers) CancelWithdrawal(c *gin.Context) {
	userID, ok := h.extractUserID(c)
	if !ok {
		return
	}

	withdrawalID, err := uuid.Parse(c.Param("withdrawalId"))
	if err != nil {
		common.SendBadRequest(c, "INVALID_WITHDRAWAL_ID", "Invalid withdrawal ID format")
		return
	}

	if err := h.withdrawalService.CancelWithdrawal(c.Request.Context(), userID, withdrawalID); err != nil {
		errMsg := err.Error()
		switch {
		case strings.Contains(errMsg, "not found"):
			common.SendNotFound(c, common.ErrCodeWithdrawalNotFound, "Withdrawal not found")
		case strings.Contains(errMsg, "does not belong"):
			common.SendNotFound(c, common.ErrCodeWithdrawalNotFound, "Withdrawal not found")
		case strings.Contains(errMsg, "cannot cancel"):
			common.SendBadRequest(c, "CANCEL_NOT_ALLOWED", errMsg)
		default:
			h.logger.Error("Failed to cancel withdrawal", "error", err, "withdrawal_id", withdrawalID)
			common.SendInternalError(c, "WITHDRAWAL_ERROR", "Failed to cancel withdrawal")
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Withdrawal cancelled"})
}

func (h *WithdrawalHandlers) extractUserID(c *gin.Context) (uuid.UUID, bool) {
	userID, exists := c.Get("user_id")
	if !exists {
		common.SendUnauthorized(c, "User not authenticated")
		return uuid.Nil, false
	}

	userUUID, ok := userID.(uuid.UUID)
	if !ok {
		common.SendInternalError(c, common.ErrCodeInternalError, "Invalid user ID format")
		return uuid.Nil, false
	}

	return userUUID, true
}

func (h *WithdrawalHandlers) handleWithdrawalError(c *gin.Context, err error, userID uuid.UUID, amount float64) {
	h.logger.Error("Failed to initiate withdrawal",
		"error", err,
		"user_id", userID,
		"amount", amount)

	errMsg := err.Error()

	switch {
	case strings.Contains(errMsg, "insufficient"):
		common.SendBadRequest(c, common.ErrCodeInsufficientFunds, "Insufficient balance for withdrawal")
	case strings.Contains(errMsg, "minimum"):
		common.SendBadRequest(c, common.ErrCodeInvalidAmount, "Withdrawal amount below minimum")
	case strings.Contains(strings.ToLower(errMsg), "circle validation error 400"),
		strings.Contains(strings.ToLower(errMsg), "api parameter invalid"):
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Invalid withdrawal parameters")
	case strings.Contains(strings.ToLower(errMsg), "token"),
		strings.Contains(strings.ToLower(errMsg), "entity secret"):
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Withdrawal provider configuration is invalid")
	case strings.Contains(errMsg, "limit exceeded"):
		common.SendBadRequest(c, "LIMIT_EXCEEDED", errMsg)
	case strings.Contains(errMsg, "bank account"):
		common.SendBadRequest(c, "BANK_ACCOUNT_ERROR", errMsg)
	case strings.Contains(errMsg, "not verified"):
		common.SendBadRequest(c, "BANK_ACCOUNT_NOT_VERIFIED", "Bank account must be verified before withdrawal")
	case strings.Contains(errMsg, "currency"):
		common.SendBadRequest(c, "CURRENCY_MISMATCH", errMsg)
	default:
		common.SendInternalError(c, "WITHDRAWAL_ERROR", "Failed to initiate withdrawal")
	}
}

// AdminWithdrawalHandlers handles admin withdrawal operations
type AdminWithdrawalHandlers struct {
	withdrawalService WithdrawalServiceInterface
	logger            *zap.Logger
}

// NewAdminWithdrawalHandlers creates a new AdminWithdrawalHandlers instance
func NewAdminWithdrawalHandlers(withdrawalService WithdrawalServiceInterface, logger *zap.Logger) *AdminWithdrawalHandlers {
	return &AdminWithdrawalHandlers{
		withdrawalService: withdrawalService,
		logger:            logger,
	}
}

// AdminGetWithdrawals handles GET /api/v1/admin/withdrawals
func (h *AdminWithdrawalHandlers) AdminGetWithdrawals(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	userIDStr := c.Query("user_id")
	if userIDStr == "" {
		c.JSON(http.StatusOK, gin.H{
			"items": []interface{}{},
			"count": 0,
			"note":  "Please provide user_id filter to view withdrawals",
		})
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidUserID, "Invalid user ID format")
		return
	}

	withdrawals, err := h.withdrawalService.GetUserWithdrawals(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.logger.Error("Failed to get withdrawals", zap.Error(err))
		common.SendInternalError(c, "WITHDRAWAL_ERROR", "Failed to retrieve withdrawals")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"items": withdrawals,
		"count": len(withdrawals),
	})
}
