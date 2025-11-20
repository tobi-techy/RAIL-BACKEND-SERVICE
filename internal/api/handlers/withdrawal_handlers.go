package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/pkg/logger"
)

// WithdrawalHandlers contains withdrawal service handlers
type WithdrawalHandlers struct {
	withdrawalService WithdrawalService
	logger            *logger.Logger
}

// WithdrawalService interface for withdrawal operations
type WithdrawalService interface {
	InitiateWithdrawal(ctx context.Context, req *entities.InitiateWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error)
	GetWithdrawal(ctx context.Context, withdrawalID uuid.UUID) (*entities.Withdrawal, error)
	GetUserWithdrawals(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Withdrawal, error)
}

// NewWithdrawalHandlers creates new withdrawal handlers
func NewWithdrawalHandlers(withdrawalService WithdrawalService, logger *logger.Logger) *WithdrawalHandlers {
	return &WithdrawalHandlers{
		withdrawalService: withdrawalService,
		logger:            logger,
	}
}

// InitiateWithdrawal initiates a USD to USDC withdrawal
func (h *WithdrawalHandlers) InitiateWithdrawal(c *gin.Context) {
	var req entities.InitiateWithdrawalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_REQUEST",
			Message: "Invalid request format",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:  "UNAUTHORIZED",
			Message: "User not authenticated",
		})
		return
	}

	userUUID, ok := userID.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:  "INTERNAL_ERROR",
			Message: "Invalid user ID format",
		})
		return
	}

	req.UserID = userUUID

	if req.Amount.IsZero() || req.Amount.IsNegative() {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:  "INVALID_AMOUNT",
			Message: "Amount must be positive",
		})
		return
	}

	if req.DestinationAddress == "" {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:  "INVALID_ADDRESS",
			Message: "Destination address is required",
		})
		return
	}

	if req.DestinationChain == "" {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:  "INVALID_CHAIN",
			Message: "Destination chain is required",
		})
		return
	}

	response, err := h.withdrawalService.InitiateWithdrawal(c.Request.Context(), &req)
	if err != nil {
		h.logger.Error("Failed to initiate withdrawal",
			"error", err,
			"user_id", userUUID,
			"amount", req.Amount.String())

		if strings.Contains(err.Error(), "insufficient") {
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{
				Code:  "INSUFFICIENT_FUNDS",
			   Message: "Insufficient buying power for withdrawal",
			})
			return
		}

		if strings.Contains(err.Error(), "not active") {
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{
				Code:  "ACCOUNT_INACTIVE",
				Message: "Alpaca account is not active",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:  "WITHDRAWAL_ERROR",
			Message: "Failed to initiate withdrawal",
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetWithdrawal retrieves a withdrawal by ID
func (h *WithdrawalHandlers) GetWithdrawal(c *gin.Context) {
	withdrawalIDStr := c.Param("withdrawalId")
	withdrawalID, err := uuid.Parse(withdrawalIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:  "INVALID_WITHDRAWAL_ID",
			Message: "Invalid withdrawal ID format",
		})
		return
	}

	withdrawal, err := h.withdrawalService.GetWithdrawal(c.Request.Context(), withdrawalID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, entities.ErrorResponse{
				Code:  "WITHDRAWAL_NOT_FOUND",
				Message: "Withdrawal not found",
			})
			return
		}

		h.logger.Error("Failed to get withdrawal", "error", err, "withdrawal_id", withdrawalID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:  "WITHDRAWAL_ERROR",
			Message: "Failed to retrieve withdrawal",
		})
		return
	}

	c.JSON(http.StatusOK, withdrawal)
}

// GetUserWithdrawals retrieves withdrawals for the authenticated user
func (h *WithdrawalHandlers) GetUserWithdrawals(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:  "UNAUTHORIZED",
			Message: "User not authenticated",
		})
		return
	}

	userUUID, ok := userID.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:  "INTERNAL_ERROR",
			Message: "Invalid user ID format",
		})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	withdrawals, err := h.withdrawalService.GetUserWithdrawals(c.Request.Context(), userUUID, limit, offset)
	if err != nil {
		h.logger.Error("Failed to get user withdrawals", "error", err, "user_id", userUUID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:  "WITHDRAWAL_ERROR",
			Message: "Failed to retrieve withdrawals",
		})
		return
	}

	c.JSON(http.StatusOK, withdrawals)
}
