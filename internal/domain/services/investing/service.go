package investing

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
)

// WalletBalanceProvider interface for fetching real-time wallet balances
type WalletBalanceProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.ManagedWallet, error)
}

// CircleClient interface for fetching wallet balances from Circle
type CircleClient interface {
	GetWalletBalances(ctx context.Context, walletID string, tokenAddress ...string) (*entities.CircleWalletBalancesResponse, error)
}

// AllocationService interface for spending enforcement
type AllocationService interface {
	CanSpend(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (bool, error)
	GetMode(ctx context.Context, userID uuid.UUID) (*entities.SmartAllocationMode, error)
	LogDeclinedSpending(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reason string) error
}

// AllocationNotificationManager interface for sending allocation notifications
type AllocationNotificationManager interface {
	NotifyTransactionDeclined(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, transactionType string) error
}

// Service handles investing operations - baskets, orders, portfolio management
type Service struct {
	basketRepo         BasketRepository
	orderRepo          OrderRepository
	positionRepo       PositionRepository
	balanceRepo        BalanceRepository
	brokerageAPI       BrokerageAdapter
	walletRepo         WalletBalanceProvider
	circleClient       CircleClient
	allocationService  AllocationService
	allocationNotifier AllocationNotificationManager
	logger             *logger.Logger
}

// BasketRepository interface for basket operations
type BasketRepository interface {
	GetAll(ctx context.Context) ([]*entities.Basket, error)
	GetByID(ctx context.Context, id uuid.UUID) (*entities.Basket, error)
}

// OrderRepository interface for order management
type OrderRepository interface {
	Create(ctx context.Context, order *entities.Order) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.Order, error)
	GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int, status *entities.OrderStatus) ([]*entities.Order, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status entities.OrderStatus, brokerageRef *string) error
}

// PositionRepository interface for position tracking
type PositionRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.Position, error)
	CreateOrUpdate(ctx context.Context, position *entities.Position) error
	GetByUserAndBasket(ctx context.Context, userID, basketID uuid.UUID) (*entities.Position, error)
}

// BalanceRepository interface for balance operations
type BalanceRepository interface {
	Get(ctx context.Context, userID uuid.UUID) (*entities.Balance, error)
	DeductBuyingPower(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
	AddBuyingPower(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
}

// BrokerageAdapter interface for brokerage integration
type BrokerageAdapter interface {
	PlaceOrder(ctx context.Context, userID, basketID uuid.UUID, side entities.OrderSide, amount decimal.Decimal) (*BrokerageOrderResponse, error)
	GetOrderStatus(ctx context.Context, brokerageRef string) (*BrokerageOrderStatus, error)
	CancelOrder(ctx context.Context, brokerageRef string) error
}

// BrokerageOrderResponse represents brokerage order response
type BrokerageOrderResponse struct {
	OrderRef string
	Status   entities.OrderStatus
}

// BrokerageOrderStatus represents brokerage order status
type BrokerageOrderStatus struct {
	Status         entities.OrderStatus
	Fills          []entities.BrokerageFill
	FilledQty      decimal.Decimal
	FilledAvgPrice decimal.Decimal
}

// NewService creates a new investing service
func NewService(
	basketRepo BasketRepository,
	orderRepo OrderRepository,
	positionRepo PositionRepository,
	balanceRepo BalanceRepository,
	brokerageAPI BrokerageAdapter,
	walletRepo WalletBalanceProvider,
	circleClient CircleClient,
	allocationService AllocationService,
	allocationNotifier AllocationNotificationManager,
	logger *logger.Logger,
) *Service {
	return &Service{
		basketRepo:         basketRepo,
		orderRepo:          orderRepo,
		positionRepo:       positionRepo,
		balanceRepo:        balanceRepo,
		brokerageAPI:       brokerageAPI,
		walletRepo:         walletRepo,
		circleClient:       circleClient,
		allocationService:  allocationService,
		allocationNotifier: allocationNotifier,
		logger:             logger,
	}
}

// ListBaskets returns all available curated baskets
func (s *Service) ListBaskets(ctx context.Context) ([]*entities.Basket, error) {
	baskets, err := s.basketRepo.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get baskets: %w", err)
	}

	s.logger.Debug("Retrieved baskets", "count", len(baskets))
	return baskets, nil
}

// GetBasket returns a specific basket by ID
func (s *Service) GetBasket(ctx context.Context, basketID uuid.UUID) (*entities.Basket, error) {
	basket, err := s.basketRepo.GetByID(ctx, basketID)
	if err != nil {
		return nil, fmt.Errorf("failed to get basket: %w", err)
	}

	return basket, nil
}

// CreateOrder places a new investment order
func (s *Service) CreateOrder(ctx context.Context, userID uuid.UUID, req *entities.OrderCreateRequest) (*entities.Order, error) {
	s.logger.Info("Creating order", "user_id", userID, "basket_id", req.BasketID, "side", req.Side, "amount", req.Amount)

	// Validate basket exists
	basket, err := s.basketRepo.GetByID(ctx, req.BasketID)
	if err != nil {
		return nil, fmt.Errorf("failed to get basket: %w", err)
	}

	if basket == nil {
		return nil, ErrBasketNotFound
	}

	// Parse and validate amount
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		return nil, fmt.Errorf("invalid amount format: %w", err)
	}

	if amount.LessThanOrEqual(decimal.Zero) {
		return nil, ErrInvalidAmount
	}

	// Check 70/30 allocation mode spending limit for buy orders
	if req.Side == entities.OrderSideBuy {
		if s.allocationService != nil {
			canSpend, err := s.allocationService.CanSpend(ctx, userID, amount)
			if err != nil {
				s.logger.Error("Failed to check spending limit", "error", err, "user_id", userID)
				return nil, fmt.Errorf("failed to check spending limit: %w", err)
			}

			if !canSpend {
				s.logger.Warn("Order declined - spending limit reached",
					"user_id", userID,
					"basket_id", req.BasketID,
					"amount", amount)
				
				// Log declined spending event
				_ = s.allocationService.LogDeclinedSpending(ctx, userID, amount, "investment")
				
				// Send notification to user
				if s.allocationNotifier != nil {
					_ = s.allocationNotifier.NotifyTransactionDeclined(ctx, userID, amount, "investment")
				}
				
				return nil, entities.ErrSpendingLimitReached
			}
		}

		// Check user has sufficient buying power
		balance, err := s.balanceRepo.Get(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("failed to get user balance: %w", err)
		}

		if balance.BuyingPower.LessThan(amount) {
			return nil, ErrInsufficientFunds
		}
	}

	// For sell orders, check user has position in the basket
	if req.Side == entities.OrderSideSell {
		position, err := s.positionRepo.GetByUserAndBasket(ctx, userID, req.BasketID)
		if err != nil && err != ErrPositionNotFound {
			return nil, fmt.Errorf("failed to check position: %w", err)
		}

		if position == nil || position.MarketValue.LessThan(amount) {
			return nil, ErrInsufficientPosition
		}
	}

	// Create order record
	order := &entities.Order{
		ID:           uuid.New(),
		UserID:       userID,
		BasketID:     req.BasketID,
		Side:         req.Side,
		Amount:       amount,
		Status:       entities.OrderStatusAccepted,
		BrokerageRef: nil,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	// Save order to database
	if err := s.orderRepo.Create(ctx, order); err != nil {
		return nil, fmt.Errorf("failed to create order: %w", err)
	}

	// Reserve buying power for buy orders
	if req.Side == entities.OrderSideBuy {
		if err := s.balanceRepo.DeductBuyingPower(ctx, userID, amount); err != nil {
			return nil, fmt.Errorf("failed to reserve buying power: %w", err)
		}
	}

	// Submit order to brokerage asynchronously
	go func() {
		brokerageResp, err := s.brokerageAPI.PlaceOrder(ctx, userID, req.BasketID, req.Side, amount)
		if err != nil {
			s.logger.Error("Failed to submit order to brokerage", "order_id", order.ID, "error", err)
			// Update order status to failed
			s.orderRepo.UpdateStatus(ctx, order.ID, entities.OrderStatusFailed, nil)
			return
		}

		// Update order with brokerage reference
		s.orderRepo.UpdateStatus(ctx, order.ID, brokerageResp.Status, &brokerageResp.OrderRef)
		s.logger.Info("Order submitted to brokerage", "order_id", order.ID, "brokerage_ref", brokerageResp.OrderRef)
	}()

	return order, nil
}

// ListOrders returns orders for a user
func (s *Service) ListOrders(ctx context.Context, userID uuid.UUID, limit, offset int, status *entities.OrderStatus) ([]*entities.Order, error) {
	orders, err := s.orderRepo.GetByUserID(ctx, userID, limit, offset, status)
	if err != nil {
		return nil, fmt.Errorf("failed to get orders: %w", err)
	}

	return orders, nil
}

// GetOrder returns a specific order
func (s *Service) GetOrder(ctx context.Context, userID, orderID uuid.UUID) (*entities.Order, error) {
	order, err := s.orderRepo.GetByID(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get order: %w", err)
	}

	if order.UserID != userID {
		return nil, ErrOrderNotFound
	}

	return order, nil
}

// GetPortfolio returns user's current portfolio
func (s *Service) GetPortfolio(ctx context.Context, userID uuid.UUID) (*entities.Portfolio, error) {
	positions, err := s.positionRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get positions: %w", err)
	}

	portfolioPositions := make([]entities.PositionResponse, len(positions))
	totalValue := decimal.Zero

	for i, position := range positions {
		portfolioPositions[i] = entities.PositionResponse{
			BasketID:    position.BasketID,
			Quantity:    position.Quantity.String(),
			AvgPrice:    position.AvgPrice.String(),
			MarketValue: position.MarketValue.String(),
		}
		totalValue = totalValue.Add(position.MarketValue)
	}

	return &entities.Portfolio{
		Currency:   "USD",
		Positions:  portfolioPositions,
		TotalValue: totalValue.String(),
	}, nil
}

// GetPortfolioOverview returns comprehensive portfolio overview with balance and performance
// This method fetches real-time balance data, so it reflects immediate updates from funding operations
func (s *Service) GetPortfolioOverview(ctx context.Context, userID uuid.UUID) (*entities.PortfolioOverview, error) {
	if s.positionRepo == nil || s.balanceRepo == nil {
		return nil, fmt.Errorf("service dependencies not initialized")
	}

	// Get positions
	positions, err := s.positionRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get positions: %w", err)
	}

	// Calculate total positions value
	positionsValue := decimal.Zero
	for _, position := range positions {
		positionsValue = positionsValue.Add(position.MarketValue)
	}

	// Fetch real-time Circle wallet balances
	var totalUSDCBalance decimal.Decimal
	if s.walletRepo != nil && s.circleClient != nil {
		wallets, err := s.walletRepo.GetByUserID(ctx, userID)
		if err == nil && len(wallets) > 0 {
			for _, wallet := range wallets {
				// Get USDC token address for this wallet's chain
				usdcTokenAddress := wallet.Chain.GetUSDCTokenAddress()
				if usdcTokenAddress == "" {
					s.logger.Warn("No USDC token address configured for chain",
						"wallet_id", wallet.CircleWalletID,
						"chain", wallet.Chain)
					continue
				}
				
				// Fetch balance from Circle API for each wallet, filtering by USDC token address
				balancesResp, err := s.circleClient.GetWalletBalances(ctx, wallet.CircleWalletID, usdcTokenAddress)
				if err != nil {
					s.logger.Warn("Failed to fetch Circle balance with token filter",
						"wallet_id", wallet.CircleWalletID,
						"chain", wallet.Chain,

						"token_address", usdcTokenAddress,
						"error", err)
				}

					// Console.log equivalent in Go (using Info level)
	s.logger.Info("GetPortfolioOverview result balance",
		"balance", balancesResp,
	)
				
				// Log all token balances for debugging
				if len(balancesResp.TokenBalances) == 0 {
					s.logger.Debug("No token balances found for wallet",
						"wallet_id", wallet.CircleWalletID,
						"chain", wallet.Chain)
				} else {
					for _, tb := range balancesResp.TokenBalances {
						s.logger.Debug("Found token balance",
							"wallet_id", wallet.CircleWalletID,
							"token_symbol", tb.Token.Symbol,
							"token_address", tb.Token.TokenAddress,
							"amount", tb.Amount)
					}
				}
				
				// Extract USDC balance
				usdcBalanceStr := balancesResp.GetUSDCBalance()
				if usdcBalance, err := decimal.NewFromString(usdcBalanceStr); err == nil {
					if usdcBalance.GreaterThan(decimal.Zero) {
						totalUSDCBalance = totalUSDCBalance.Add(usdcBalance)
						s.logger.Debug("Fetched Circle wallet balance",
							"wallet_id", wallet.CircleWalletID,
							"chain", wallet.Chain,
							"token_address", usdcTokenAddress,
							"usdc_balance", usdcBalanceStr)
					}
				}
			}
		}
	}
	
	// Get database balance (buying power) as fallback
	balance, err := s.balanceRepo.Get(ctx, userID)
	if err != nil {
		// If balance doesn't exist yet (new user), use zero
		s.logger.Debug("Balance not found for user, using zero", "user_id", userID)
		balance = &entities.Balance{
			BuyingPower: decimal.Zero,
			Currency:    "USD",
			UpdatedAt:   time.Now(),
		}
	}
	
	// Add Circle USDC balance to database buying power
	// (USDC is 1:1 with USD buying power)
	buyingPower := balance.BuyingPower.Add(totalUSDCBalance)

	// Calculate total portfolio (positions + buying power)
	totalPortfolio := positionsValue.Add(buyingPower)

	// Calculate performance (30-day)
	// For now, we'll use a simple calculation: (current value - invested amount) / invested amount
	// In production, you'd query portfolio_performance table for historical data
	performance := 0.0
	totalInvested := decimal.Zero
	for _, position := range positions {
		// Invested amount = quantity * avg price
		invested := position.Quantity.Mul(position.AvgPrice)
		totalInvested = totalInvested.Add(invested)
	}

	if totalInvested.GreaterThan(decimal.Zero) {
		gain := positionsValue.Sub(totalInvested)
		performanceDecimal := gain.Div(totalInvested).Mul(decimal.NewFromInt(100))
		performance, _ = performanceDecimal.Float64()
	}



	return &entities.PortfolioOverview{
		TotalPortfolio:     totalPortfolio.StringFixed(2),
		BuyingPower:        buyingPower.StringFixed(2),
		PositionsValue:     positionsValue.StringFixed(2),
		PerformanceLast30d: performance,
		Currency:           "USD",
		LastUpdated:        time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// ProcessBrokerageFill processes brokerage fill webhook
func (s *Service) ProcessBrokerageFill(ctx context.Context, webhook *entities.BrokerageFillWebhook) error {
	s.logger.Info("Processing brokerage fill", "order_id", webhook.OrderID, "status", webhook.Status)

	// Get the order
	order, err := s.orderRepo.GetByID(ctx, webhook.OrderID)
	if err != nil {
		return fmt.Errorf("failed to get order: %w", err)
	}

	// Update order status
	if err := s.orderRepo.UpdateStatus(ctx, order.ID, webhook.Status, order.BrokerageRef); err != nil {
		return fmt.Errorf("failed to update order status: %w", err)
	}

	// If order is filled, update positions
	if webhook.Status == entities.OrderStatusFilled {
		if err := s.updatePositions(ctx, order, webhook.Fills); err != nil {
			return fmt.Errorf("failed to update positions: %w", err)
		}
	}

	// If order failed, refund buying power for buy orders
	if webhook.Status == entities.OrderStatusFailed && order.Side == entities.OrderSideBuy {
		if err := s.balanceRepo.AddBuyingPower(ctx, order.UserID, order.Amount); err != nil {
			s.logger.Error("Failed to refund buying power", "order_id", order.ID, "error", err)
		}
	}

	return nil
}

// updatePositions updates user positions based on fills
func (s *Service) updatePositions(ctx context.Context, order *entities.Order, fills []entities.BrokerageFill) error {
	// Get or create position for this basket
	position, err := s.positionRepo.GetByUserAndBasket(ctx, order.UserID, order.BasketID)
	if err != nil && err != ErrPositionNotFound {
		return fmt.Errorf("failed to get position: %w", err)
	}

	// Calculate total fill value
	totalValue := decimal.Zero
	totalQuantity := decimal.Zero
	for _, fill := range fills {
		quantity, _ := decimal.NewFromString(fill.Quantity)
		price, _ := decimal.NewFromString(fill.Price)
		totalValue = totalValue.Add(quantity.Mul(price))
		totalQuantity = totalQuantity.Add(quantity)
	}

	if position == nil {
		// Create new position
		position = &entities.Position{
			ID:          uuid.New(),
			UserID:      order.UserID,
			BasketID:    order.BasketID,
			Quantity:    totalQuantity,
			AvgPrice:    totalValue.Div(totalQuantity),
			MarketValue: totalValue,
			UpdatedAt:   time.Now(),
		}
	} else {
		// Update existing position
		if order.Side == entities.OrderSideBuy {
			// Add to position
			newTotalValue := position.MarketValue.Add(totalValue)
			newTotalQuantity := position.Quantity.Add(totalQuantity)
			position.AvgPrice = newTotalValue.Div(newTotalQuantity)
			position.Quantity = newTotalQuantity
			position.MarketValue = newTotalValue
		} else {
			// Reduce position
			position.Quantity = position.Quantity.Sub(totalQuantity)
			position.MarketValue = position.MarketValue.Sub(totalValue)
			if position.Quantity.LessThanOrEqual(decimal.Zero) {
				position.Quantity = decimal.Zero
				position.MarketValue = decimal.Zero
			}
		}
		position.UpdatedAt = time.Now()
	}

	return s.positionRepo.CreateOrUpdate(ctx, position)
}

// Common errors
var (
	ErrBasketNotFound       = fmt.Errorf("basket not found")
	ErrOrderNotFound        = fmt.Errorf("order not found")
	ErrPositionNotFound     = fmt.Errorf("position not found")
	ErrInvalidAmount        = fmt.Errorf("invalid amount")
	ErrInsufficientFunds    = fmt.Errorf("insufficient buying power")
	ErrInsufficientPosition = fmt.Errorf("insufficient position")
)
