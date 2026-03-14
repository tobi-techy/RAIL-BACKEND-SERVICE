package rebalancing_worker

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// InvestmentRulesRepository retrieves user investment rules
type InvestmentRulesRepository interface {
	GetAllWithRebalancingEnabled(ctx context.Context) ([]*entities.InvestmentRulesConfig, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.InvestmentRulesConfig, error)
	UpdateRebalancingTimestamps(ctx context.Context, userID uuid.UUID, checked, rebalanced *time.Time) error
}

// PositionProvider retrieves user positions
type PositionProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentPosition, error)
}

// StrategyProvider retrieves target allocations
type StrategyProvider interface {
	GetTargetAllocations(ctx context.Context, userID uuid.UUID) (map[string]decimal.Decimal, error)
}

// OrderPlacer places rebalancing orders
type OrderPlacer interface {
	PlaceMarketOrder(ctx context.Context, userID uuid.UUID, symbol string, amount decimal.Decimal) (*entities.AlpacaOrderResponse, error)
}

// NotificationService sends rebalancing notifications
type NotificationService interface {
	SendRebalanceNotification(ctx context.Context, userID uuid.UUID, trades int, totalValue decimal.Decimal) error
}

// Worker processes automatic portfolio rebalancing
type Worker struct {
	rulesRepo       InvestmentRulesRepository
	positionRepo    PositionProvider
	strategyProvider StrategyProvider
	orderPlacer     OrderPlacer
	notifier        NotificationService
	logger          *zap.Logger
	stopCh          chan struct{}
}

// NewWorker creates a new rebalancing worker
func NewWorker(
	rulesRepo InvestmentRulesRepository,
	positionRepo PositionProvider,
	strategyProvider StrategyProvider,
	orderPlacer OrderPlacer,
	notifier NotificationService,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		rulesRepo:        rulesRepo,
		positionRepo:     positionRepo,
		strategyProvider: strategyProvider,
		orderPlacer:      orderPlacer,
		notifier:         notifier,
		logger:           logger,
		stopCh:           make(chan struct{}),
	}
}

// Start begins the worker processing loop
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting rebalancing worker")

	// Check for rebalancing needs every hour
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	// Run immediately on start
	w.processRebalancing(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Rebalancing worker stopped (context cancelled)")
			return
		case <-w.stopCh:
			w.logger.Info("Rebalancing worker stopped")
			return
		case <-ticker.C:
			w.processRebalancing(ctx)
		}
	}
}

// Stop signals the worker to stop
func (w *Worker) Stop() {
	close(w.stopCh)
}

func (w *Worker) processRebalancing(ctx context.Context) {
	if !isMarketOpen() {
		return
	}
	configs, err := w.rulesRepo.GetAllWithRebalancingEnabled(ctx)
	if err != nil {
		w.logger.Error("Failed to get rebalancing configs", zap.Error(err))
		return
	}

	for _, config := range configs {
		if !w.shouldCheckRebalancing(config) {
			continue
		}

		if err := w.checkAndRebalanceUser(ctx, config); err != nil {
			w.logger.Error("Failed to rebalance user",
				zap.String("user_id", config.UserID.String()),
				zap.Error(err))
		}
	}
}

func (w *Worker) shouldCheckRebalancing(config *entities.InvestmentRulesConfig) bool {
	if config.RebalancingConfig == nil || !config.RebalancingConfig.Enabled {
		return false
	}

	rc := config.RebalancingConfig
	if rc.LastChecked == nil {
		return true
	}

	// Check based on frequency
	since := time.Since(*rc.LastChecked)
	switch rc.Frequency {
	case entities.RebalanceFreqDaily:
		return since >= 24*time.Hour
	case entities.RebalanceFreqWeekly:
		return since >= 7*24*time.Hour
	case entities.RebalanceFreqMonthly:
		return since >= 30*24*time.Hour
	case entities.RebalanceFreqQuarterly:
		return since >= 90*24*time.Hour
	default:
		return since >= 30*24*time.Hour
	}
}

func (w *Worker) checkAndRebalanceUser(ctx context.Context, config *entities.InvestmentRulesConfig) error {
	userID := config.UserID
	now := time.Now()

	// Update last checked timestamp
	if err := w.rulesRepo.UpdateRebalancingTimestamps(ctx, userID, &now, nil); err != nil {
		w.logger.Warn("Failed to update last checked timestamp", zap.Error(err))
	}

	// Get current positions
	positions, err := w.positionRepo.GetByUserID(ctx, userID)
	if err != nil {
		return err
	}

	if len(positions) == 0 {
		return nil // Nothing to rebalance
	}

	// Get target allocations
	targets, err := w.strategyProvider.GetTargetAllocations(ctx, userID)
	if err != nil {
		return err
	}

	// Calculate drift and generate trades
	trades, needsRebalance := w.calculateDrift(positions, targets, config.RebalancingConfig.ThresholdPct)
	if !needsRebalance {
		w.logger.Debug("No rebalancing needed",
			zap.String("user_id", userID.String()))
		return nil
	}

	// Execute trades
	totalValue := decimal.Zero
	for _, trade := range trades {
		_, err := w.orderPlacer.PlaceMarketOrder(ctx, userID, trade.Symbol, trade.Amount)
		if err != nil {
			w.logger.Error("Failed to place rebalancing order",
				zap.String("symbol", trade.Symbol),
				zap.Error(err))
			continue
		}
		totalValue = totalValue.Add(trade.Amount.Abs())
	}

	// Update last rebalanced timestamp
	if err := w.rulesRepo.UpdateRebalancingTimestamps(ctx, userID, nil, &now); err != nil {
		w.logger.Warn("Failed to update last rebalanced timestamp", zap.Error(err))
	}

	// Send notification if enabled
	if config.RebalancingConfig.NotifyOnRebalance && w.notifier != nil {
		_ = w.notifier.SendRebalanceNotification(ctx, userID, len(trades), totalValue)
	}

	w.logger.Info("Rebalancing completed",
		zap.String("user_id", userID.String()),
		zap.Int("trades", len(trades)),
		zap.String("total_value", totalValue.String()))

	return nil
}

// RebalanceTrade represents a single rebalancing trade
type RebalanceTrade struct {
	Symbol     string
	Amount     decimal.Decimal // Positive = buy, negative = sell
	CurrentPct decimal.Decimal
	TargetPct  decimal.Decimal
	DriftPct   decimal.Decimal
}

func (w *Worker) calculateDrift(positions []*entities.InvestmentPosition, targets map[string]decimal.Decimal, threshold decimal.Decimal) ([]RebalanceTrade, bool) {
	// Calculate total portfolio value
	totalValue := decimal.Zero
	positionMap := make(map[string]*entities.InvestmentPosition)
	for _, pos := range positions {
		totalValue = totalValue.Add(pos.MarketValue)
		positionMap[pos.Symbol] = pos
	}

	if totalValue.IsZero() {
		return nil, false
	}

	hundred := decimal.NewFromInt(100)
	var trades []RebalanceTrade
	needsRebalance := false

	// Check existing positions against targets
	for symbol, pos := range positionMap {
		currentPct := pos.MarketValue.Div(totalValue).Mul(hundred)
		targetPct := targets[symbol]
		drift := currentPct.Sub(targetPct)

		if drift.Abs().GreaterThan(threshold) {
			needsRebalance = true
			// Calculate trade amount
			targetValue := totalValue.Mul(targetPct).Div(hundred)
			tradeAmount := targetValue.Sub(pos.MarketValue)

			trades = append(trades, RebalanceTrade{
				Symbol:     symbol,
				Amount:     tradeAmount,
				CurrentPct: currentPct,
				TargetPct:  targetPct,
				DriftPct:   drift,
			})
		}
	}

	// Check for missing positions (targets we don't have)
	for symbol, targetPct := range targets {
		if _, exists := positionMap[symbol]; !exists && targetPct.GreaterThan(threshold) {
			needsRebalance = true
			targetValue := totalValue.Mul(targetPct).Div(hundred)
			trades = append(trades, RebalanceTrade{
				Symbol:     symbol,
				Amount:     targetValue,
				CurrentPct: decimal.Zero,
				TargetPct:  targetPct,
				DriftPct:   targetPct.Neg(),
			})
		}
	}

	return trades, needsRebalance
}

// isMarketOpen returns true when the US equity market is currently open (Mon–Fri 09:30–16:00 ET).
func isMarketOpen() bool {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return true
	}
	et := time.Now().In(loc)
	wd := et.Weekday()
	if wd == time.Saturday || wd == time.Sunday {
		return false
	}
	open := time.Date(et.Year(), et.Month(), et.Day(), 9, 30, 0, 0, loc)
	close := time.Date(et.Year(), et.Month(), et.Day(), 16, 0, 0, 0, loc)
	return et.After(open) && et.Before(close)
}
