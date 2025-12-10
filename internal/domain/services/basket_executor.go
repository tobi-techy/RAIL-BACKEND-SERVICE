package services

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// BasketExecutor handles batch order execution for investment baskets
type BasketExecutor struct {
	alpacaService *alpaca.Service
	logger        *zap.Logger
}

func NewBasketExecutor(alpacaService *alpaca.Service, logger *zap.Logger) *BasketExecutor {
	return &BasketExecutor{
		alpacaService: alpacaService,
		logger:        logger,
	}
}

// BasketAllocation represents a single asset allocation in a basket
type BasketAllocation struct {
	Symbol     string
	Percentage decimal.Decimal // 0-100
}

// ExecuteBasket places fractional orders for all assets in a basket
func (s *BasketExecutor) ExecuteBasket(
	ctx context.Context,
	alpacaAccountID string,
	totalAmount decimal.Decimal,
	allocations []BasketAllocation,
) ([]*entities.AlpacaOrderResponse, error) {
	s.logger.Info("Executing basket orders",
		zap.String("alpaca_account_id", alpacaAccountID),
		zap.String("total_amount", totalAmount.String()),
		zap.Int("allocations", len(allocations)))

	orders := make([]*entities.AlpacaOrderResponse, 0, len(allocations))

	for _, allocation := range allocations {
		// Calculate dollar amount for this allocation
		allocationAmount := totalAmount.Mul(allocation.Percentage).Div(decimal.NewFromInt(100))

		order, err := s.alpacaService.CreateOrder(ctx, alpacaAccountID, &entities.AlpacaCreateOrderRequest{
			Symbol:        allocation.Symbol,
			Notional:      &allocationAmount,
			Side:          entities.AlpacaOrderSideBuy,
			Type:          entities.AlpacaOrderTypeMarket,
			TimeInForce:   entities.AlpacaTimeInForceDay,
			ExtendedHours: false,
			ClientOrderID: uuid.New().String(),
		})

		if err != nil {
			s.logger.Error("Failed to place basket order",
				zap.String("symbol", allocation.Symbol),
				zap.String("amount", allocationAmount.String()),
				zap.Error(err))
			// Continue with other orders even if one fails
			continue
		}

		s.logger.Info("Basket order placed",
			zap.String("symbol", allocation.Symbol),
			zap.String("order_id", order.ID),
			zap.String("amount", allocationAmount.String()))

		orders = append(orders, order)
	}

	if len(orders) == 0 {
		return nil, fmt.Errorf("all basket orders failed")
	}

	s.logger.Info("Basket execution completed",
		zap.Int("successful_orders", len(orders)),
		zap.Int("total_allocations", len(allocations)))

	return orders, nil
}

// GetBasketAllocations returns predefined basket allocations
func GetBasketAllocations(basketType string) []BasketAllocation {
	baskets := map[string][]BasketAllocation{
		"tech-growth": {
			{Symbol: "AAPL", Percentage: decimal.NewFromInt(20)},
			{Symbol: "MSFT", Percentage: decimal.NewFromInt(20)},
			{Symbol: "GOOGL", Percentage: decimal.NewFromInt(15)},
			{Symbol: "NVDA", Percentage: decimal.NewFromInt(15)},
			{Symbol: "TSLA", Percentage: decimal.NewFromInt(15)},
			{Symbol: "META", Percentage: decimal.NewFromInt(15)},
		},
		"sustainability": {
			{Symbol: "ICLN", Percentage: decimal.NewFromInt(30)}, // Clean energy ETF
			{Symbol: "TAN", Percentage: decimal.NewFromInt(25)},  // Solar ETF
			{Symbol: "TSLA", Percentage: decimal.NewFromInt(20)},
			{Symbol: "NEE", Percentage: decimal.NewFromInt(15)},  // NextEra Energy
			{Symbol: "ENPH", Percentage: decimal.NewFromInt(10)}, // Enphase Energy
		},
		"balanced-etf": {
			{Symbol: "SPY", Percentage: decimal.NewFromInt(40)},  // S&P 500
			{Symbol: "QQQ", Percentage: decimal.NewFromInt(30)},  // Nasdaq 100
			{Symbol: "VTI", Percentage: decimal.NewFromInt(20)},  // Total market
			{Symbol: "AGG", Percentage: decimal.NewFromInt(10)},  // Bond ETF
		},
	}

	return baskets[basketType]
}
