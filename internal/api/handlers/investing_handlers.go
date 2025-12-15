package handlers

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	"github.com/rail-service/rail_service/pkg/logger"
)

// InvestingHandlers handles investment-related operations
type InvestingHandlers struct {
	investingService *investing.Service
	validator        *validator.Validate
	logger           *logger.Logger
}

// NewInvestingHandlers creates a new InvestingHandlers instance
func NewInvestingHandlers(investingService *investing.Service, logger *logger.Logger) *InvestingHandlers {
	return &InvestingHandlers{
		investingService: investingService,
		validator:        validator.New(),
		logger:           logger,
	}
}

// GetBaskets handles GET /api/v1/investing/baskets
func (h *InvestingHandlers) GetBaskets(c *gin.Context) {
	baskets, err := h.investingService.ListBaskets(c.Request.Context())
	if err != nil {
		h.logger.Error("Failed to get baskets", "error", err)
		SendInternalError(c, "BASKETS_ERROR", "Failed to retrieve baskets")
		return
	}

	SendSuccess(c, baskets)
}

// GetBasket handles GET /api/v1/investing/baskets/:basketId
func (h *InvestingHandlers) GetBasket(c *gin.Context) {
	basketIDStr := c.Param("basketId")
	basketID, err := uuid.Parse(basketIDStr)
	if err != nil {
		SendBadRequest(c, "INVALID_BASKET_ID", "Invalid basket ID format")
		return
	}

	basket, err := h.investingService.GetBasket(c.Request.Context(), basketID)
	if err != nil {
		if err == investing.ErrBasketNotFound {
			SendNotFound(c, ErrCodeBasketNotFound, "Basket not found")
			return
		}
		h.logger.Error("Failed to get basket",
			"error", err,
			"basket_id", basketID)
		SendInternalError(c, "BASKET_ERROR", "Failed to retrieve basket")
		return
	}

	SendSuccess(c, basket)
}

// CreateOrder handles POST /api/v1/investing/orders
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
		h.handleOrderError(c, err, userUUID)
		return
	}

	SendCreated(c, order)
}

// GetOrders handles GET /api/v1/investing/orders
func (h *InvestingHandlers) GetOrders(c *gin.Context) {
	userUUID, err := getUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		respondUnauthorized(c, "User not authenticated")
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	var statusFilter *entities.OrderStatus
	if statusStr := c.Query("status"); statusStr != "" {
		status := entities.OrderStatus(statusStr)
		statusFilter = &status
	}

	orders, err := h.investingService.ListOrders(c.Request.Context(), userUUID, limit, offset, statusFilter)
	if err != nil {
		h.logger.Error("Failed to get orders",
			"error", err,
			"user_id", userUUID)
		SendInternalError(c, "ORDERS_ERROR", "Failed to retrieve orders")
		return
	}

	SendSuccess(c, orders)
}

// GetOrder handles GET /api/v1/investing/orders/:orderId
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
		SendBadRequest(c, "INVALID_ORDER_ID", "Invalid order ID format")
		return
	}

	order, err := h.investingService.GetOrder(c.Request.Context(), userUUID, orderID)
	if err != nil {
		if err == investing.ErrOrderNotFound {
			SendNotFound(c, ErrCodeOrderNotFound, "Order not found")
			return
		}
		h.logger.Error("Failed to get order",
			"error", err,
			"user_id", userUUID,
			"order_id", orderID)
		SendInternalError(c, "ORDER_ERROR", "Failed to retrieve order")
		return
	}

	SendSuccess(c, order)
}

// GetPortfolio handles GET /api/v1/investing/portfolio
func (h *InvestingHandlers) GetPortfolio(c *gin.Context) {
	userUUID, err := getUserID(c)
	if err != nil {
		h.logger.Error("Failed to get user ID", "error", err)
		respondUnauthorized(c, "User not authenticated")
		return
	}

	portfolio, err := h.investingService.GetPortfolio(c.Request.Context(), userUUID)
	if err != nil {
		h.logger.Error("Failed to get portfolio",
			"error", err,
			"user_id", userUUID)
		SendInternalError(c, "PORTFOLIO_ERROR", "Failed to retrieve portfolio")
		return
	}

	SendSuccess(c, portfolio)
}

// Helper methods

func (h *InvestingHandlers) handleOrderError(c *gin.Context, err error, userUUID uuid.UUID) {
	switch err {
	case investing.ErrBasketNotFound:
		SendBadRequest(c, ErrCodeBasketNotFound, "Specified basket does not exist")
	case investing.ErrInvalidAmount:
		SendBadRequest(c, ErrCodeInvalidAmount, "Invalid order amount")
	case investing.ErrInsufficientFunds:
		SendForbidden(c, ErrCodeInsufficientFunds)
	case investing.ErrInsufficientPosition:
		SendBadRequest(c, ErrCodeInsufficientPosition, "Insufficient position for sell order")
	default:
		h.logger.Error("Failed to create order",
			"error", err,
			"user_id", userUUID)
		SendInternalError(c, "ORDER_ERROR", "Failed to create order")
	}
}
