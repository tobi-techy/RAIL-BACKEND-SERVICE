package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/internal/domain/services"
	"go.uber.org/zap"
)

type InvestmentHandlers struct {
	basketExecutor *services.BasketExecutor
	balanceService *services.BalanceService
	logger         *zap.Logger
}

func NewInvestmentHandlers(
	basketExecutor *services.BasketExecutor,
	balanceService *services.BalanceService,
	logger *zap.Logger,
) *InvestmentHandlers {
	return &InvestmentHandlers{
		basketExecutor: basketExecutor,
		balanceService: balanceService,
		logger:         logger,
	}
}

type InvestBasketRequest struct {
	Amount decimal.Decimal `json:"amount" binding:"required"`
}

type InvestBasketResponse struct {
	OrderCount int      `json:"order_count"`
	OrderIDs   []string `json:"order_ids"`
	Message    string   `json:"message"`
}

// InvestInBasket handles basket investment with instant funding
func (h *InvestmentHandlers) InvestInBasket(c *gin.Context) {
	basketType := c.Param("basket_type")
	userID := c.GetString("user_id")

	var req InvestBasketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_REQUEST",
			Message: err.Error(),
		})
		return
	}

	uid, _ := uuid.Parse(userID)

	// Get user's buying power
	balance, err := h.balanceService.GetBalance(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "BALANCE_ERROR",
			Message: "Failed to get balance",
		})
		return
	}

	// Check buying power
	if balance.LessThan(req.Amount) {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INSUFFICIENT_FUNDS",
			Message: "Insufficient buying power",
		})
		return
	}

	// TODO: Get Alpaca account ID from user profile or virtual accounts
	alpacaAccountID := "" // Placeholder

	// Get basket allocations
	allocations := services.GetBasketAllocations(basketType)
	if len(allocations) == 0 {
		c.JSON(http.StatusNotFound, entities.ErrorResponse{
			Code:    "BASKET_NOT_FOUND",
			Message: "Invalid basket type",
		})
		return
	}

	// Execute basket orders
	orders, err := h.basketExecutor.ExecuteBasket(
		c.Request.Context(),
		alpacaAccountID,
		req.Amount,
		allocations,
	)
	if err != nil {
		h.logger.Error("Basket execution failed",
			zap.String("user_id", userID),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "EXECUTION_ERROR",
			Message: "Failed to execute basket",
		})
		return
	}

	orderIDs := make([]string, len(orders))
	for i, order := range orders {
		orderIDs[i] = order.ID
	}

	c.JSON(http.StatusOK, InvestBasketResponse{
		OrderCount: len(orders),
		OrderIDs:   orderIDs,
		Message:    "Basket orders placed successfully",
	})
}

type GetBasketsResponse struct {
	Baskets []BasketInfo `json:"baskets"`
}

type BasketInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	MinAmount   string   `json:"min_amount"`
	Assets      []string `json:"assets"`
}

// GetBaskets returns available investment baskets
func (h *InvestmentHandlers) GetBaskets(c *gin.Context) {
	baskets := []BasketInfo{
		{
			ID:          "tech-growth",
			Name:        "Tech Growth",
			Description: "High-growth technology stocks",
			MinAmount:   "10.00",
			Assets:      []string{"AAPL", "MSFT", "GOOGL", "NVDA", "TSLA", "META"},
		},
		{
			ID:          "sustainability",
			Name:        "Sustainability",
			Description: "Clean energy and sustainable companies",
			MinAmount:   "10.00",
			Assets:      []string{"ICLN", "TAN", "TSLA", "NEE", "ENPH"},
		},
		{
			ID:          "balanced-etf",
			Name:        "Balanced ETF",
			Description: "Diversified portfolio of ETFs",
			MinAmount:   "10.00",
			Assets:      []string{"SPY", "QQQ", "VTI", "AGG"},
		},
	}

	c.JSON(http.StatusOK, GetBasketsResponse{Baskets: baskets})
}
