package investing

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// RebalancingConfigRepository interface
type RebalancingConfigRepository interface {
	Create(ctx context.Context, config *entities.RebalancingConfig) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.RebalancingConfig, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.RebalancingConfig, error)
	Update(ctx context.Context, config *entities.RebalancingConfig) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// PositionProvider interface for getting positions
type PositionProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentPosition, error)
}

// QuoteProvider interface for getting prices
type QuoteProvider interface {
	GetQuotes(ctx context.Context, symbols []string) (map[string]*entities.MarketQuote, error)
}

// RebalancingService handles portfolio rebalancing
type RebalancingService struct {
	configRepo    RebalancingConfigRepository
	positionRepo  PositionProvider
	quoteProvider QuoteProvider
	orderPlacer   OrderPlacer
	logger        *zap.Logger
}

func NewRebalancingService(
	configRepo RebalancingConfigRepository,
	positionRepo PositionProvider,
	quoteProvider QuoteProvider,
	orderPlacer OrderPlacer,
	logger *zap.Logger,
) *RebalancingService {
	return &RebalancingService{
		configRepo:    configRepo,
		positionRepo:  positionRepo,
		quoteProvider: quoteProvider,
		orderPlacer:   orderPlacer,
		logger:        logger,
	}
}

// CreateRebalancingConfig creates a new rebalancing configuration
func (s *RebalancingService) CreateRebalancingConfig(ctx context.Context, userID uuid.UUID, name string, allocations map[string]decimal.Decimal, thresholdPct decimal.Decimal, frequency *string) (*entities.RebalancingConfig, error) {
	// Validate allocations sum to 100
	total := decimal.Zero
	for _, pct := range allocations {
		if pct.LessThan(decimal.Zero) || pct.GreaterThan(decimal.NewFromInt(100)) {
			return nil, fmt.Errorf("allocation percentages must be between 0 and 100")
		}
		total = total.Add(pct)
	}
	if !total.Equal(decimal.NewFromInt(100)) {
		return nil, fmt.Errorf("allocations must sum to 100%%, got %s%%", total.String())
	}

	now := time.Now()
	config := &entities.RebalancingConfig{
		ID:                uuid.New(),
		UserID:            userID,
		Name:              name,
		TargetAllocations: allocations,
		ThresholdPct:      thresholdPct,
		Frequency:         frequency,
		Status:            entities.ScheduleStatusActive,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if err := s.configRepo.Create(ctx, config); err != nil {
		return nil, fmt.Errorf("create config: %w", err)
	}

	s.logger.Info("Rebalancing config created",
		zap.String("id", config.ID.String()),
		zap.String("user_id", userID.String()))

	return config, nil
}

// GetUserConfigs returns all rebalancing configs for a user
func (s *RebalancingService) GetUserConfigs(ctx context.Context, userID uuid.UUID) ([]*entities.RebalancingConfig, error) {
	return s.configRepo.GetByUserID(ctx, userID)
}

// GetConfig returns a specific rebalancing config
func (s *RebalancingService) GetConfig(ctx context.Context, id uuid.UUID) (*entities.RebalancingConfig, error) {
	return s.configRepo.GetByID(ctx, id)
}

// UpdateConfig updates a rebalancing configuration
func (s *RebalancingService) UpdateConfig(ctx context.Context, id uuid.UUID, allocations map[string]decimal.Decimal, thresholdPct *decimal.Decimal) error {
	config, err := s.configRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if config == nil {
		return fmt.Errorf("config not found")
	}

	if allocations != nil {
		total := decimal.Zero
		for _, pct := range allocations {
			total = total.Add(pct)
		}
		if !total.Equal(decimal.NewFromInt(100)) {
			return fmt.Errorf("allocations must sum to 100%%")
		}
		config.TargetAllocations = allocations
	}

	if thresholdPct != nil {
		config.ThresholdPct = *thresholdPct
	}

	return s.configRepo.Update(ctx, config)
}

// DeleteConfig deletes a rebalancing configuration
func (s *RebalancingService) DeleteConfig(ctx context.Context, id uuid.UUID) error {
	return s.configRepo.Delete(ctx, id)
}

// GenerateRebalancingPlan calculates trades needed to rebalance
func (s *RebalancingService) GenerateRebalancingPlan(ctx context.Context, userID uuid.UUID, configID uuid.UUID) (*entities.RebalancingPlan, error) {
	config, err := s.configRepo.GetByID(ctx, configID)
	if err != nil {
		return nil, err
	}
	if config == nil {
		return nil, fmt.Errorf("config not found")
	}

	positions, err := s.positionRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get positions: %w", err)
	}

	// Calculate total portfolio value
	var totalValue decimal.Decimal
	positionMap := make(map[string]*entities.InvestmentPosition)
	for _, pos := range positions {
		totalValue = totalValue.Add(pos.MarketValue)
		positionMap[pos.Symbol] = pos
	}

	if totalValue.IsZero() {
		return nil, fmt.Errorf("portfolio has no value")
	}

	// Calculate current allocations
	currentAllocs := make(map[string]decimal.Decimal)
	for _, pos := range positions {
		currentAllocs[pos.Symbol] = pos.MarketValue.Div(totalValue).Mul(decimal.NewFromInt(100))
	}

	// Generate trades
	plan := &entities.RebalancingPlan{
		ConfigID:           configID,
		CurrentAllocations: currentAllocs,
		TargetAllocations:  config.TargetAllocations,
		Trades:             []entities.RebalanceTradeOrder{},
	}

	// Get quotes for target symbols
	symbols := make([]string, 0, len(config.TargetAllocations))
	for sym := range config.TargetAllocations {
		symbols = append(symbols, sym)
	}
	quotes, err := s.quoteProvider.GetQuotes(ctx, symbols)
	if err != nil {
		s.logger.Warn("Failed to get quotes", zap.Error(err))
	}

	hundred := decimal.NewFromInt(100)

	// Calculate sells first (positions we have but shouldn't or have too much)
	for symbol, currentPct := range currentAllocs {
		targetPct, inTarget := config.TargetAllocations[symbol]
		if !inTarget {
			targetPct = decimal.Zero
		}

		drift := currentPct.Sub(targetPct)
		if drift.GreaterThan(config.ThresholdPct) {
			// Need to sell
			sellAmount := drift.Div(hundred).Mul(totalValue)
			trade := entities.RebalanceTradeOrder{
				Symbol:     symbol,
				Side:       "sell",
				CurrentPct: currentPct,
				TargetPct:  targetPct,
				DriftPct:   drift,
				Amount:     sellAmount,
			}
			if quote, ok := quotes[symbol]; ok && quote.Price.GreaterThan(decimal.Zero) {
				trade.EstimatedQty = sellAmount.Div(quote.Price)
			}
			plan.Trades = append(plan.Trades, trade)
			plan.TotalSellAmount = plan.TotalSellAmount.Add(sellAmount)
		}
	}

	// Calculate buys (positions we should have more of)
	for symbol, targetPct := range config.TargetAllocations {
		currentPct := currentAllocs[symbol]
		drift := targetPct.Sub(currentPct)

		if drift.GreaterThan(config.ThresholdPct) {
			// Need to buy
			buyAmount := drift.Div(hundred).Mul(totalValue)
			trade := entities.RebalanceTradeOrder{
				Symbol:     symbol,
				Side:       "buy",
				CurrentPct: currentPct,
				TargetPct:  targetPct,
				DriftPct:   drift,
				Amount:     buyAmount,
			}
			if quote, ok := quotes[symbol]; ok && quote.Price.GreaterThan(decimal.Zero) {
				trade.EstimatedQty = buyAmount.Div(quote.Price)
			}
			plan.Trades = append(plan.Trades, trade)
			plan.TotalBuyAmount = plan.TotalBuyAmount.Add(buyAmount)
		}
	}

	return plan, nil
}

// ExecuteRebalancingPlan executes the trades in a rebalancing plan
func (s *RebalancingService) ExecuteRebalancingPlan(ctx context.Context, userID uuid.UUID, plan *entities.RebalancingPlan) error {
	if len(plan.Trades) == 0 {
		return nil
	}

	// Execute sells first to free up cash
	for _, trade := range plan.Trades {
		if trade.Side == "sell" {
			_, err := s.orderPlacer.PlaceMarketOrder(ctx, userID, trade.Symbol, trade.Amount.Neg())
			if err != nil {
				s.logger.Error("Failed to place sell order",
					zap.String("symbol", trade.Symbol),
					zap.Error(err))
				return fmt.Errorf("sell %s: %w", trade.Symbol, err)
			}
		}
	}

	// Then execute buys
	for _, trade := range plan.Trades {
		if trade.Side == "buy" {
			_, err := s.orderPlacer.PlaceMarketOrder(ctx, userID, trade.Symbol, trade.Amount)
			if err != nil {
				s.logger.Error("Failed to place buy order",
					zap.String("symbol", trade.Symbol),
					zap.Error(err))
				return fmt.Errorf("buy %s: %w", trade.Symbol, err)
			}
		}
	}

	// Update last rebalanced timestamp
	config, _ := s.configRepo.GetByID(ctx, plan.ConfigID)
	if config != nil {
		now := time.Now()
		config.LastRebalancedAt = &now
		_ = s.configRepo.Update(ctx, config)
	}

	s.logger.Info("Rebalancing executed",
		zap.String("config_id", plan.ConfigID.String()),
		zap.Int("trades", len(plan.Trades)))

	return nil
}

// CheckDrift checks if portfolio has drifted beyond threshold
func (s *RebalancingService) CheckDrift(ctx context.Context, userID uuid.UUID, configID uuid.UUID) (bool, decimal.Decimal, error) {
	plan, err := s.GenerateRebalancingPlan(ctx, userID, configID)
	if err != nil {
		return false, decimal.Zero, err
	}

	// Find max drift
	maxDrift := decimal.Zero
	for _, trade := range plan.Trades {
		if trade.DriftPct.Abs().GreaterThan(maxDrift) {
			maxDrift = trade.DriftPct.Abs()
		}
	}

	config, _ := s.configRepo.GetByID(ctx, configID)
	needsRebalance := config != nil && maxDrift.GreaterThan(config.ThresholdPct)

	return needsRebalance, maxDrift, nil
}
