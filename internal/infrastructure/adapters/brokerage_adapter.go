package adapters

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"go.uber.org/zap"
)

// BrokerageAdapter provides integration with brokerage partner (Alpaca)
type BrokerageAdapter struct {
	alpacaClient     *alpaca.Client
	basketRepo       *repositories.BasketRepository
	alpacaAccountRepo *repositories.AlpacaAccountRepository
	logger           *zap.Logger
	orderTracker     *OrderTracker
}

// OrderTracker tracks basket orders and their component orders
type OrderTracker struct {
	mu     sync.RWMutex
	orders map[string]*BasketOrderInfo
}

// BasketOrderInfo holds information about a basket order
type BasketOrderInfo struct {
	BasketRef    string
	BasketID     uuid.UUID
	AccountID    string
	ComponentIDs []string
	Status       entities.OrderStatus
	CreatedAt    time.Time
}

// NewOrderTracker creates a new order tracker
func NewOrderTracker() *OrderTracker {
	return &OrderTracker{
		orders: make(map[string]*BasketOrderInfo),
	}
}

// TrackBasketOrder stores basket order information
func (t *OrderTracker) TrackBasketOrder(ctx context.Context, basketRef string, basketID uuid.UUID, accountID string, orderIDs []string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.orders[basketRef] = &BasketOrderInfo{
		BasketRef:    basketRef,
		BasketID:     basketID,
		AccountID:    accountID,
		ComponentIDs: orderIDs,
		Status:       entities.OrderStatusAccepted,
		CreatedAt:    time.Now(),
	}
	return nil
}

// GetBasketOrderInfo retrieves basket order info
func (t *OrderTracker) GetBasketOrderInfo(ctx context.Context, basketRef string) (*BasketOrderInfo, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	info, ok := t.orders[basketRef]
	if !ok {
		return nil, fmt.Errorf("basket order not found: %s", basketRef)
	}
	return info, nil
}

// MarkBasketCanceled marks a basket order as canceled
func (t *OrderTracker) MarkBasketCanceled(ctx context.Context, basketRef string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if info, ok := t.orders[basketRef]; ok {
		info.Status = entities.OrderStatusCanceled
	}
	return nil
}

// NewBrokerageAdapter creates a new brokerage adapter instance
func NewBrokerageAdapter(
	alpacaClient *alpaca.Client,
	basketRepo *repositories.BasketRepository,
	alpacaAccountRepo *repositories.AlpacaAccountRepository,
	logger *zap.Logger,
) *BrokerageAdapter {
	return &BrokerageAdapter{
		alpacaClient:      alpacaClient,
		basketRepo:        basketRepo,
		alpacaAccountRepo: alpacaAccountRepo,
		logger:            logger,
		orderTracker:      NewOrderTracker(),
	}
}

// PlaceOrder submits a basket order to the brokerage via Alpaca API
func (a *BrokerageAdapter) PlaceOrder(ctx context.Context, userID, basketID uuid.UUID, side entities.OrderSide, amount decimal.Decimal) (*investing.BrokerageOrderResponse, error) {
	a.logger.Info("PlaceOrder called",
		zap.String("user_id", userID.String()),
		zap.String("basket_id", basketID.String()),
		zap.String("side", string(side)),
		zap.String("amount", amount.String()),
	)

	// Get user's Alpaca account
	alpacaAccount, err := a.alpacaAccountRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get alpaca account: %w", err)
	}
	if alpacaAccount == nil {
		return nil, fmt.Errorf("user does not have an alpaca account")
	}

	// Fetch basket composition from database
	basket, err := a.basketRepo.GetByID(ctx, basketID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch basket: %w", err)
	}
	if basket == nil {
		return nil, fmt.Errorf("basket not found: %s", basketID)
	}

	// Generate basket order reference
	orderRef := fmt.Sprintf("BASKET-%s", uuid.New().String()[:8])
	var orderIDs []string
	var orderErrors []error

	// Calculate proportional allocation and submit individual orders
	for _, component := range basket.Composition {
		componentAmount := amount.Mul(component.Weight)
		
		// Skip if amount is too small
		if componentAmount.LessThan(decimal.NewFromFloat(1.0)) {
			a.logger.Debug("Skipping component with small amount",
				zap.String("symbol", component.Symbol),
				zap.String("amount", componentAmount.String()))
			continue
		}

		// Submit individual order via Alpaca API
		clientOrderID := fmt.Sprintf("%s-%s", orderRef, component.Symbol)
		orderSide := entities.AlpacaOrderSideBuy
		if side == entities.OrderSideSell {
			orderSide = entities.AlpacaOrderSideSell
		}
		
		orderReq := &entities.AlpacaCreateOrderRequest{
			Symbol:        component.Symbol,
			Notional:      &componentAmount,
			Side:          orderSide,
			Type:          entities.AlpacaOrderTypeMarket,
			TimeInForce:   entities.AlpacaTimeInForceDay,
			ClientOrderID: clientOrderID,
		}
		
		order, err := a.alpacaClient.CreateOrder(ctx, alpacaAccount.AlpacaAccountID, orderReq)
		if err != nil {
			a.logger.Error("Failed to place component order",
				zap.String("symbol", component.Symbol),
				zap.Error(err))
			orderErrors = append(orderErrors, fmt.Errorf("%s: %w", component.Symbol, err))
			continue
		}
		
		orderIDs = append(orderIDs, order.ID)
		a.logger.Info("Component order placed",
			zap.String("symbol", component.Symbol),
			zap.String("order_id", order.ID),
			zap.String("amount", componentAmount.String()))
	}

	// Check if any orders were placed
	if len(orderIDs) == 0 && len(orderErrors) > 0 {
		return nil, fmt.Errorf("failed to place any component orders: %v", orderErrors)
	}

	// Track order execution
	if err := a.orderTracker.TrackBasketOrder(ctx, orderRef, basketID, alpacaAccount.AlpacaAccountID, orderIDs); err != nil {
		a.logger.Error("Failed to track basket order", zap.Error(err))
	}

	status := entities.OrderStatusAccepted
	if len(orderErrors) > 0 {
		status = entities.OrderStatusPartiallyFilled
	}

	return &investing.BrokerageOrderResponse{
		OrderRef: orderRef,
		Status:   status,
	}, nil
}

// GetOrderStatus retrieves the current status of an order from the brokerage
func (a *BrokerageAdapter) GetOrderStatus(ctx context.Context, brokerageRef string) (*investing.BrokerageOrderStatus, error) {
	a.logger.Info("GetOrderStatus called", zap.String("brokerage_ref", brokerageRef))

	// Check if this is a basket order
	if strings.HasPrefix(brokerageRef, "BASKET-") {
		return a.getBasketOrderStatus(ctx, brokerageRef)
	}

	// For single orders, we need the account ID - this shouldn't happen in normal flow
	return nil, fmt.Errorf("single order status lookup requires account context")
}

// getBasketOrderStatus retrieves status for a basket order
func (a *BrokerageAdapter) getBasketOrderStatus(ctx context.Context, basketRef string) (*investing.BrokerageOrderStatus, error) {
	info, err := a.orderTracker.GetBasketOrderInfo(ctx, basketRef)
	if err != nil {
		return nil, fmt.Errorf("failed to get basket order info: %w", err)
	}

	var allFills []entities.BrokerageFill
	allFilled := true
	anyFailed := false
	totalFilledQty := decimal.Zero
	totalFilledValue := decimal.Zero

	for _, orderID := range info.ComponentIDs {
		order, err := a.alpacaClient.GetOrder(ctx, info.AccountID, orderID)
		if err != nil {
			a.logger.Error("Failed to get component order", zap.String("order_id", orderID), zap.Error(err))
			continue
		}

		if order.Status != entities.AlpacaOrderStatusFilled {
			allFilled = false
		}
		if order.Status == entities.AlpacaOrderStatusCanceled || order.Status == entities.AlpacaOrderStatusRejected {
			anyFailed = true
		}

		if order.FilledQty.GreaterThan(decimal.Zero) {
			filledAvgPrice := decimal.Zero
			if order.FilledAvgPrice != nil {
				filledAvgPrice = *order.FilledAvgPrice
			}
			allFills = append(allFills, entities.BrokerageFill{
				Symbol:   order.Symbol,
				Quantity: order.FilledQty.String(),
				Price:    filledAvgPrice.String(),
			})
			totalFilledQty = totalFilledQty.Add(order.FilledQty)
			totalFilledValue = totalFilledValue.Add(order.FilledQty.Mul(filledAvgPrice))
		}
	}

	status := entities.OrderStatusPending
	if allFilled && len(info.ComponentIDs) > 0 {
		status = entities.OrderStatusFilled
	} else if anyFailed {
		status = entities.OrderStatusPartiallyFilled
	} else if len(allFills) > 0 {
		status = entities.OrderStatusPartiallyFilled
	}

	avgPrice := decimal.Zero
	if totalFilledQty.GreaterThan(decimal.Zero) {
		avgPrice = totalFilledValue.Div(totalFilledQty)
	}

	return &investing.BrokerageOrderStatus{
		Status:         status,
		Fills:          allFills,
		FilledQty:      totalFilledQty,
		FilledAvgPrice: avgPrice,
	}, nil
}

// CancelOrder cancels an order at the brokerage
func (a *BrokerageAdapter) CancelOrder(ctx context.Context, brokerageRef string) error {
	a.logger.Info("CancelOrder called", zap.String("brokerage_ref", brokerageRef))

	// Check if this is a basket order
	if strings.HasPrefix(brokerageRef, "BASKET-") {
		return a.cancelBasketOrder(ctx, brokerageRef)
	}

	return fmt.Errorf("single order cancellation requires account context")
}

// cancelBasketOrder cancels all component orders of a basket order
func (a *BrokerageAdapter) cancelBasketOrder(ctx context.Context, basketRef string) error {
	info, err := a.orderTracker.GetBasketOrderInfo(ctx, basketRef)
	if err != nil {
		return fmt.Errorf("failed to get basket order info: %w", err)
	}

	var cancelErrors []error
	for _, orderID := range info.ComponentIDs {
		if err := a.alpacaClient.CancelOrder(ctx, info.AccountID, orderID); err != nil {
			a.logger.Error("Failed to cancel component order",
				zap.String("order_id", orderID),
				zap.Error(err))
			cancelErrors = append(cancelErrors, err)
		}
	}

	if len(cancelErrors) > 0 {
		return fmt.Errorf("failed to cancel %d/%d component orders", len(cancelErrors), len(info.ComponentIDs))
	}

	// Mark basket order as canceled in tracker
	if err := a.orderTracker.MarkBasketCanceled(ctx, basketRef); err != nil {
		a.logger.Error("Failed to mark basket as canceled", zap.Error(err))
	}

	return nil
}
