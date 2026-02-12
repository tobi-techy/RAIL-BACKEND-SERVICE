package wallet

import (
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"context"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
	"go.uber.org/zap"
)

// WithdrawalServiceInterface defines the interface for withdrawal operations
type WithdrawalServiceInterface interface {
	InitiateWithdrawal(ctx context.Context, req *entities.InitiateWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error)
	GetWithdrawal(ctx context.Context, withdrawalID uuid.UUID) (*entities.Withdrawal, error)
	GetUserWithdrawals(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Withdrawal, error)
	CancelWithdrawal(ctx context.Context, withdrawalID uuid.UUID, userID uuid.UUID) error
}

// BalanceChecker interface for balance validation (defense-in-depth)
type BalanceChecker interface {
	GetBuyingPower(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
}

// WithdrawalHandlers handles withdrawal-related operations
type WithdrawalHandlers struct {
	withdrawalService WithdrawalServiceInterface
	balanceChecker    BalanceChecker
	validator         *validator.Validate
	logger            *logger.Logger
}

// NewWithdrawalHandlers creates a new WithdrawalHandlers instance
func NewWithdrawalHandlers(withdrawalService WithdrawalServiceInterface, balanceChecker BalanceChecker, logger *logger.Logger) *WithdrawalHandlers {
	return &WithdrawalHandlers{
		withdrawalService: withdrawalService,
		balanceChecker:    balanceChecker,
		validator:         validator.New(),
		logger:            logger,
	}
}

// InitiateWithdrawal handles POST /api/v1/funding/withdraw
func (h *WithdrawalHandlers) InitiateWithdrawal(c *gin.Context) {
	var req entities.InitiateWithdrawalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, "Invalid request format")
		return
	}

	userID, ok := h.extractUserID(c)
	if !ok {
		return // Error already sent
	}

	req.UserID = userID

	if err := h.validateWithdrawalRequest(&req); err != nil {
		common.SendBadRequest(c, err.code, err.message)
		return
	}

	// Defense-in-depth: Check balance at handler level before service call
	if h.balanceChecker != nil {
		buyingPower, err := h.balanceChecker.GetBuyingPower(c.Request.Context(), userID)
		if err != nil {
			h.logger.Error("Failed to check buying power",
				"error", err,
				"user_id", userID)
			common.SendInternalError(c, "BALANCE_CHECK_ERROR", "Failed to verify balance")
			return
		}

		if buyingPower.LessThan(req.Amount) {
			h.logger.Warn("Withdrawal rejected - insufficient balance (handler check)",
				"user_id", userID,
				"requested", req.Amount.String(),
				"available", buyingPower.String())
			common.SendBadRequest(c, common.ErrCodeInsufficientFunds, "Insufficient buying power for withdrawal")
			return
		}
	}

	response, err := h.withdrawalService.InitiateWithdrawal(c.Request.Context(), &req)
	if err != nil {
		h.handleWithdrawalError(c, err, userID, req.Amount.String())
		return
	}

	common.SendSuccess(c, response)
}

// GetWithdrawal handles GET /api/v1/funding/withdrawals/:withdrawalId
func (h *WithdrawalHandlers) GetWithdrawal(c *gin.Context) {
	withdrawalIDStr := c.Param("withdrawalId")
	withdrawalID, err := uuid.Parse(withdrawalIDStr)
	if err != nil {
		common.SendBadRequest(c, "INVALID_WITHDRAWAL_ID", "Invalid withdrawal ID format")
		return
	}

	withdrawal, err := h.withdrawalService.GetWithdrawal(c.Request.Context(), withdrawalID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			common.SendNotFound(c, common.ErrCodeWithdrawalNotFound, "Withdrawal not found")
			return
		}

		h.logger.Error("Failed to get withdrawal",
			"error", err,
			"withdrawal_id", withdrawalID)
		common.SendInternalError(c, "WITHDRAWAL_ERROR", "Failed to retrieve withdrawal")
		return
	}

	common.SendSuccess(c, withdrawal)
}

// GetUserWithdrawals handles GET /api/v1/withdrawals
func (h *WithdrawalHandlers) GetUserWithdrawals(c *gin.Context) {
	userID, ok := h.extractUserID(c)
	if !ok {
		return // Error already sent
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	withdrawals, err := h.withdrawalService.GetUserWithdrawals(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.logger.Error("Failed to get user withdrawals",
			"error", err,
			"user_id", userID)
		common.SendInternalError(c, "WITHDRAWAL_ERROR", "Failed to retrieve withdrawals")
		return
	}

	common.SendSuccess(c, withdrawals)
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

	if err := h.withdrawalService.CancelWithdrawal(c.Request.Context(), withdrawalID, userID); err != nil {
		errMsg := err.Error()
		switch {
		case strings.Contains(errMsg, "not found"):
			common.SendNotFound(c, common.ErrCodeWithdrawalNotFound, "Withdrawal not found")
		case strings.Contains(errMsg, "cannot cancel"):
			common.SendBadRequest(c, "CANCEL_NOT_ALLOWED", errMsg)
		default:
			h.logger.Error("Failed to cancel withdrawal", "error", err, "withdrawal_id", withdrawalID)
			common.SendInternalError(c, "WITHDRAWAL_ERROR", "Failed to cancel withdrawal")
		}
		return
	}

	common.SendSuccess(c, gin.H{"message": "Withdrawal cancelled"})
}

// Helper types and methods

type validationError struct {
	code    string
	message string
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

func (h *WithdrawalHandlers) validateWithdrawalRequest(req *entities.InitiateWithdrawalRequest) *validationError {
	if req.Amount.IsZero() || req.Amount.IsNegative() {
		return &validationError{
			code:    common.ErrCodeInvalidAmount,
			message: "Amount must be positive",
		}
	}

	if req.DestinationAddress == "" {
		return &validationError{
			code:    "INVALID_ADDRESS",
			message: "Destination address is required",
		}
	}

	if req.DestinationChain == "" {
		return &validationError{
			code:    common.ErrCodeInvalidChain,
			message: "Destination chain is required",
		}
	}

	return nil
}

func (h *WithdrawalHandlers) handleWithdrawalError(c *gin.Context, err error, userID uuid.UUID, amount string) {
	h.logger.Error("Failed to initiate withdrawal",
		"error", err,
		"user_id", userID,
		"amount", amount)

	errMsg := err.Error()

	switch {
	case strings.Contains(errMsg, "insufficient"):
		common.SendBadRequest(c, common.ErrCodeInsufficientFunds, "Insufficient buying power for withdrawal")
	case strings.Contains(errMsg, "not active"):
		common.SendBadRequest(c, common.ErrCodeAccountInactive, "Alpaca account is not active")
	case strings.Contains(errMsg, "minimum"):
		common.SendBadRequest(c, common.ErrCodeInvalidAmount, "Withdrawal amount below minimum")
	case strings.Contains(errMsg, "daily limit"):
		common.SendBadRequest(c, "DAILY_LIMIT_EXCEEDED", "Daily withdrawal limit exceeded")
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

	// Parse optional user_id filter
	var userID *uuid.UUID
	if userIDStr := c.Query("user_id"); userIDStr != "" {
		parsed, err := uuid.Parse(userIDStr)
		if err != nil {
			common.SendBadRequest(c, common.ErrCodeInvalidUserID, "Invalid user ID format")
			return
		}
		userID = &parsed
	}

	var withdrawals []*entities.Withdrawal
	var err error

	if userID != nil {
		withdrawals, err = h.withdrawalService.GetUserWithdrawals(c.Request.Context(), *userID, limit, offset)
	} else {
		// For admin, we might want to add a method to get all withdrawals
		// For now, we return an empty list without user filter
		common.SendSuccess(c, gin.H{
			"items": []interface{}{},
			"count": 0,
			"note":  "Please provide user_id filter to view withdrawals",
		})
		return
	}

	if err != nil {
		h.logger.Error("Failed to get withdrawals", zap.Error(err))
		common.SendInternalError(c, "WITHDRAWAL_ERROR", "Failed to retrieve withdrawals")
		return
	}

	common.SendSuccess(c, gin.H{
		"items": withdrawals,
		"count": len(withdrawals),
	})
}

// AdminGetWithdrawal handles GET /api/v1/admin/withdrawals/:withdrawalId
func (h *AdminWithdrawalHandlers) AdminGetWithdrawal(c *gin.Context) {
	withdrawalIDStr := c.Param("withdrawalId")
	withdrawalID, err := uuid.Parse(withdrawalIDStr)
	if err != nil {
		common.SendBadRequest(c, "INVALID_WITHDRAWAL_ID", "Invalid withdrawal ID format")
		return
	}

	withdrawal, err := h.withdrawalService.GetWithdrawal(c.Request.Context(), withdrawalID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			common.SendNotFound(c, common.ErrCodeWithdrawalNotFound, "Withdrawal not found")
			return
		}

		h.logger.Error("Failed to get withdrawal", zap.Error(err), zap.String("withdrawal_id", withdrawalID.String()))
		common.SendInternalError(c, "WITHDRAWAL_ERROR", "Failed to retrieve withdrawal")
		return
	}

	common.SendSuccess(c, withdrawal)
}
