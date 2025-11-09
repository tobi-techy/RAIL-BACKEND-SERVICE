package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/internal/domain/services/funding"
	"github.com/stack-service/stack_service/internal/domain/services/investing"
	"github.com/stack-service/stack_service/pkg/logger"
	"github.com/stack-service/stack_service/pkg/retry"
)


// FundingHandlers contains funding service handlers
type FundingHandlers struct {
	fundingService     *funding.Service
	withdrawalService  FundingWithdrawalService
	logger             *logger.Logger
}

// FundingWithdrawalService interface for withdrawal operations
type FundingWithdrawalService interface {
	InitiateWithdrawal(ctx context.Context, req *entities.InitiateWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error)
	GetWithdrawal(ctx context.Context, withdrawalID uuid.UUID) (*entities.Withdrawal, error)
	GetUserWithdrawals(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Withdrawal, error)
}

// InvestingHandlers contains investing service handlers
type InvestingHandlers struct {
	investingService *investing.Service
	logger           *logger.Logger
}

// NewFundingHandlers creates new funding handlers
func NewFundingHandlers(fundingService *funding.Service, withdrawalService FundingWithdrawalService, logger *logger.Logger) *FundingHandlers {
	return &FundingHandlers{
		fundingService:    fundingService,
		withdrawalService: withdrawalService,
		logger:            logger,
	}
}

// NewInvestingHandlers creates new investing handlers
func NewInvestingHandlers(investingService *investing.Service, logger *logger.Logger) *InvestingHandlers {
	return &InvestingHandlers{
		investingService: investingService,
		logger:           logger,
	}
}

// IsWebhookRetryableError determines if a webhook processing error should be retried
func IsWebhookRetryableError(err error) bool {
	if err == nil {
		return false
	}
	
	errorMsg := err.Error()
	
	// Don't retry client errors or validation errors
	if strings.Contains(errorMsg, "invalid") || 
		 strings.Contains(errorMsg, "malformed") ||
		 strings.Contains(errorMsg, "already processed") ||
		 strings.Contains(errorMsg, "duplicate") {
		return false
	}
	
	// Retry on temporary failures
	if strings.Contains(errorMsg, "timeout") ||
		 strings.Contains(errorMsg, "connection") ||
		 strings.Contains(errorMsg, "temporary") ||
		 strings.Contains(errorMsg, "unavailable") {
		return true
	}
	
	// By default, retry server errors (5xx equivalent)
	return true
}

// === Funding Handlers ===

// CreateDepositAddress creates a deposit address for a specific chain
// @Summary Create deposit address
// @Description Generate or retrieve a deposit address for a specific blockchain
// @Tags funding
// @Accept json
// @Produce json
// @Param request body entities.DepositAddressRequest true "Deposit address request"
// @Success 200 {object} entities.DepositAddressResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/funding/deposit-address [post]
func (h *FundingHandlers) CreateDepositAddress(c *gin.Context) {
	var req entities.DepositAddressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "Invalid request format", map[string]interface{}{"error": err.Error()})
		return
	}

	userUUID, err := getUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		respondUnauthorized(c, "User not authenticated")
		return
	}

	response, err := h.fundingService.CreateDepositAddress(c.Request.Context(), userUUID, req.Chain)
	if err != nil {
		h.logger.Error("Failed to create deposit address", "error", err, "user_id", userUUID, "chain", req.Chain)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "DEPOSIT_ADDRESS_ERROR",
			Message: "Failed to create deposit address",
		})
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetFundingConfirmations lists recent funding confirmations
// @Summary Get funding confirmations
// @Description Retrieve recent funding confirmations for the authenticated user
// @Tags funding
// @Produce json
// @Param limit query int false "Number of results to return" default(20)
// @Param offset query int false "Number of results to skip" default(0)
// @Success 200 {array} entities.FundingConfirmation
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/funding/confirmations [get]
func (h *FundingHandlers) GetFundingConfirmations(c *gin.Context) {
	userUUID, err := getUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		respondUnauthorized(c, "User not authenticated")
		return
	}

	// Parse query parameters
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit > 100 {
		limit = 100
	}
	if limit < 1 {
		limit = 20
	}
	
	offset := 0
	if cursor := c.Query("cursor"); cursor != "" {
		if o, err := strconv.Atoi(cursor); err == nil && o >= 0 {
			offset = o
		}
	}

	confirmations, err := h.fundingService.GetFundingConfirmations(c.Request.Context(), userUUID, limit, offset)
	if err != nil {
		h.logger.Error("Failed to get funding confirmations", "error", err, "user_id", userUUID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "CONFIRMATIONS_ERROR",
			Message: "Failed to retrieve funding confirmations",
		})
		return
	}

	// Prepare paginated response as per OpenAPI spec
	response := entities.FundingConfirmationsPage{
		Items:      confirmations,
		NextCursor: nil,
	}
	
	// Add next cursor if we have more results
	if len(confirmations) == limit {
		nextCursor := strconv.Itoa(offset + limit)
		response.NextCursor = &nextCursor
	}

	c.JSON(http.StatusOK, response)
}

// GetBalances returns user's current balance
// @Summary Get user balances
// @Description Get the authenticated user's current buying power and pending deposits
// @Tags funding
// @Produce json
// @Success 200 {object} entities.BalancesResponse
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/funding/balances [get]
func (h *FundingHandlers) GetBalances(c *gin.Context) {
	userUUID, err := getUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		respondUnauthorized(c, "User not authenticated")
		return
	}

	balances, err := h.fundingService.GetBalance(c.Request.Context(), userUUID)
	if err != nil {
		h.logger.Error("Failed to get balances", "error", err, "user_id", userUUID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "BALANCES_ERROR",
			Message: "Failed to retrieve balances",
		})
		return
	}

	c.JSON(http.StatusOK, balances)
}

// CreateVirtualAccount creates a virtual account linked to an Alpaca brokerage account
// @Summary Create virtual account
// @Description Create a virtual account for funding a brokerage account with stablecoins
// @Tags funding
// @Accept json
// @Produce json
// @Param request body entities.CreateVirtualAccountRequest true "Virtual account creation request"
// @Success 201 {object} entities.CreateVirtualAccountResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/funding/virtual-account [post]
func (h *FundingHandlers) CreateVirtualAccount(c *gin.Context) {
	var req entities.CreateVirtualAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "Invalid request format", map[string]interface{}{"error": err.Error()})
		return
	}

	userUUID, err := getUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		respondUnauthorized(c, "User not authenticated")
		return
	}

	// Set user ID from context
	req.UserID = userUUID

	// Validate Alpaca account ID
	if req.AlpacaAccountID == "" {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:  "INVALID_REQUEST",
			Message: "Alpaca account ID is required",
		})
		return
	}

	response, err := h.fundingService.CreateVirtualAccount(c.Request.Context(), &req)
	if err != nil {
		h.logger.Error("Failed to create virtual account",
			"error", err,
			"user_id", userUUID,
			"alpaca_account_id", req.AlpacaAccountID)

		// Handle specific error cases
		if strings.Contains(err.Error(), "already exists") {
			c.JSON(http.StatusConflict, entities.ErrorResponse{
				Code:  "VIRTUAL_ACCOUNT_EXISTS",
				Message: "Virtual account already exists for this Alpaca account",
			})
			return
		}

		if strings.Contains(err.Error(), "not active") {
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{
				Code:  "ALPACA_ACCOUNT_INACTIVE",
				Message: "Alpaca account is not active",
			})
			return
		}

		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:  "VIRTUAL_ACCOUNT_ERROR",
			Message: "Failed to create virtual account",
		})
		return
	}

	c.JSON(http.StatusCreated, response)
}

// === Investing Handlers ===

// GetBaskets lists all available investment baskets
// @Summary Get investment baskets
// @Description Retrieve all available curated investment baskets
// @Tags investing
// @Produce json
// @Success 200 {array} entities.Basket
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/investing/baskets [get]
func (h *InvestingHandlers) GetBaskets(c *gin.Context) {
	baskets, err := h.investingService.ListBaskets(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to get baskets", "error", err)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "BASKETS_ERROR",
			Message: "Failed to retrieve baskets",
		})
		return
	}

	c.JSON(http.StatusOK, baskets)
}

// GetBasket returns details of a specific basket
// @Summary Get basket details
// @Description Retrieve details of a specific investment basket
// @Tags investing
// @Produce json
// @Param basketId path string true "Basket ID"
// @Success 200 {object} entities.Basket
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/investing/baskets/{basketId} [get]
func (h *InvestingHandlers) GetBasket(c *gin.Context) {
	basketIDStr := c.Param("basketId")
	basketID, err := uuid.Parse(basketIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_BASKET_ID",
			Message: "Invalid basket ID format",
		})
		return
	}

	basket, err := h.investingService.GetBasket(c.Request.Context(), basketID)
	if err != nil {
		if err == investing.ErrBasketNotFound {
			c.JSON(http.StatusNotFound, entities.ErrorResponse{
				Code:    "BASKET_NOT_FOUND",
				Message: "Basket not found",
			})
			return
		}
		h.logger.Error("Failed to get basket", "error", err, "basket_id", basketID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "BASKET_ERROR",
			Message: "Failed to retrieve basket",
		})
		return
	}

	c.JSON(http.StatusOK, basket)
}

// CreateOrder creates a new investment order
// @Summary Create investment order
// @Description Place a buy or sell order for a basket
// @Tags investing
// @Accept json
// @Produce json
// @Param request body entities.OrderCreateRequest true "Order creation request"
// @Success 201 {object} entities.Order
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 403 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/investing/orders [post]
func (h *InvestingHandlers) CreateOrder(c *gin.Context) {
	var req entities.OrderCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "Invalid request format", map[string]interface{}{"error": err.Error()})
		return
	}

	userUUID, err := getUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		respondUnauthorized(c, "User not authenticated")
		return
	}

	order, err := h.investingService.CreateOrder(c.Request.Context(), userUUID, &req)
	if err != nil {
		switch err {
		case investing.ErrBasketNotFound:
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{
				Code:    "BASKET_NOT_FOUND",
				Message: "Specified basket does not exist",
			})
			return
		case investing.ErrInvalidAmount:
			c.JSON(http.StatusBadRequest, entities.ErrorResponse{
				Code:    "INVALID_AMOUNT",
				Message: "Invalid order amount",
			})
			return
		case investing.ErrInsufficientFunds:
			c.JSON(http.StatusForbidden, entities.ErrorResponse{
				Code:    "INSUFFICIENT_FUNDS",
				Message: "Insufficient buying power for this order",
			})
			return
		case investing.ErrInsufficientPosition:
			c.JSON(http.StatusForbidden, entities.ErrorResponse{
				Code:    "INSUFFICIENT_POSITION",
				Message: "Insufficient position for sell order",
			})
			return
		default:
			h.logger.Error("Failed to create order", "error", err, "user_id", userUUID)
			c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
				Code:    "ORDER_ERROR",
				Message: "Failed to create order",
			})
			return
		}
	}

	c.JSON(http.StatusCreated, order)
}

// GetOrders lists user's orders
// @Summary Get user orders
// @Description Retrieve orders for the authenticated user
// @Tags investing
// @Produce json
// @Param limit query int false "Number of results to return" default(20)
// @Param offset query int false "Number of results to skip" default(0)
// @Param status query string false "Filter by order status"
// @Success 200 {array} entities.Order
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/investing/orders [get]
func (h *InvestingHandlers) GetOrders(c *gin.Context) {
	userUUID, err := getUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		respondUnauthorized(c, "User not authenticated")
		return
	}

	// Parse query parameters
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	var statusFilter *entities.OrderStatus
	if statusStr := c.Query("status"); statusStr != "" {
		status := entities.OrderStatus(statusStr)
		statusFilter = &status
	}

	orders, err := h.investingService.ListOrders(c.Request.Context(), userUUID, limit, offset, statusFilter)
	if err != nil {
		h.logger.Error("Failed to get orders", "error", err, "user_id", userUUID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "ORDERS_ERROR",
			Message: "Failed to retrieve orders",
		})
		return
	}

	c.JSON(http.StatusOK, orders)
}

// GetOrder returns details of a specific order
// @Summary Get order details
// @Description Retrieve details of a specific order
// @Tags investing
// @Produce json
// @Param orderId path string true "Order ID"
// @Success 200 {object} entities.Order
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/investing/orders/{orderId} [get]
func (h *InvestingHandlers) GetOrder(c *gin.Context) {
	userUUID, err := getUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		respondUnauthorized(c, "User not authenticated")
		return
	}

	orderIDStr := c.Param("orderId")
	orderID, err := uuid.Parse(orderIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_ORDER_ID",
			Message: "Invalid order ID format",
		})
		return
	}

	order, err := h.investingService.GetOrder(c.Request.Context(), userUUID, orderID)
	if err != nil {
		if err == investing.ErrOrderNotFound {
			c.JSON(http.StatusNotFound, entities.ErrorResponse{
				Code:    "ORDER_NOT_FOUND",
				Message: "Order not found",
			})
			return
		}
		h.logger.Error("Failed to get order", "error", err, "user_id", userUUID, "order_id", orderID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "ORDER_ERROR",
			Message: "Failed to retrieve order",
		})
		return
	}

	c.JSON(http.StatusOK, order)
}

// GetPortfolio returns user's portfolio
// @Summary Get user portfolio
// @Description Retrieve the authenticated user's complete portfolio
// @Tags investing
// @Produce json
// @Success 200 {object} entities.Portfolio
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/investing/portfolio [get]
func (h *InvestingHandlers) GetPortfolio(c *gin.Context) {
	userUUID, err := getUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		respondUnauthorized(c, "User not authenticated")
		return
	}

	portfolio, err := h.investingService.GetPortfolio(c.Request.Context(), userUUID)
	if err != nil {
		h.logger.Error("Failed to get portfolio", "error", err, "user_id", userUUID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "PORTFOLIO_ERROR",
			Message: "Failed to retrieve portfolio",
		})
		return
	}

	c.JSON(http.StatusOK, portfolio)
}

// === Webhook Handlers ===

// ChainDepositWebhook handles incoming chain deposit confirmations
// @Summary Chain deposit webhook
// @Description Handle blockchain deposit confirmations
// @Tags webhooks
// @Accept json
// @Produce json
// @Param request body entities.ChainDepositWebhook true "Chain deposit webhook payload"
// @Success 200 {object} map[string]string
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/webhooks/chain-deposit [post]
func (h *FundingHandlers) ChainDepositWebhook(c *gin.Context) {
	var webhook entities.ChainDepositWebhook
	if err := c.ShouldBindJSON(&webhook); err != nil {
		respondBadRequest(c, "Invalid webhook payload", map[string]interface{}{"error": err.Error()})
		return
	}

	// TODO: Verify webhook signature for security
	// Basic validation
	if webhook.TxHash == "" {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_WEBHOOK",
			Message: "Missing transaction hash",
		})
		return
	}
	
	if webhook.Amount == "" || webhook.Amount == "0" {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_WEBHOOK",
			Message: "Invalid amount",
		})
		return
	}

	// Process webhook with retry logic for resilience
	retryConfig := retry.RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   500 * time.Millisecond,
		MaxDelay:    5 * time.Second,
		Multiplier:  2.0,
	}
	
	err := retry.WithExponentialBackoff(
		c.Request.Context(),
		retryConfig,
		func() error {
			return h.fundingService.ProcessChainDeposit(c.Request.Context(), &webhook)
		},
		IsWebhookRetryableError,
	)
	
	if err != nil {
		h.logger.Error("Failed to process chain deposit webhook after retries", 
			"error", err, 
			"tx_hash", webhook.TxHash,
			"amount", webhook.Amount,
			"chain", webhook.Chain)
			
		// Check if it's a duplicate (idempotency case)
		if strings.Contains(err.Error(), "already processed") {
			h.logger.Info("Webhook already processed (idempotent)", "tx_hash", webhook.TxHash)
			c.JSON(http.StatusOK, gin.H{"status": "already_processed"})
			return
		}
			
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "WEBHOOK_PROCESSING_ERROR",
			Message: "Failed to process deposit webhook",
			Details: map[string]interface{}{"tx_hash": webhook.TxHash},
		})
		return
	}

	h.logger.Info("Webhook processed successfully", 
		"tx_hash", webhook.TxHash,
		"amount", webhook.Amount,
		"chain", webhook.Chain)
		
	c.JSON(http.StatusOK, gin.H{"status": "processed"})
}

// BrokerageFillWebhook handles brokerage order fill notifications
// @Summary Brokerage fill webhook
// @Description Handle brokerage order fill notifications
// @Tags webhooks
// @Accept json
// @Produce json
// @Param request body entities.BrokerageFillWebhook true "Brokerage fill webhook payload"
// @Success 200 {object} map[string]string
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/webhooks/brokerage-fill [post]
func (h *InvestingHandlers) BrokerageFillWebhook(c *gin.Context) {
	var webhook entities.BrokerageFillWebhook
	if err := c.ShouldBindJSON(&webhook); err != nil {
		respondBadRequest(c, "Invalid webhook payload", map[string]interface{}{"error": err.Error()})
		return
	}

	// TODO: Verify webhook signature for security
	
	if err := h.investingService.ProcessBrokerageFill(c.Request.Context(), &webhook); err != nil {
		h.logger.Error("Failed to process brokerage fill webhook", "error", err, "order_id", webhook.OrderID)
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "WEBHOOK_PROCESSING_ERROR",
			Message: "Failed to process fill webhook",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "processed"})
}