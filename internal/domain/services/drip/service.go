package drip

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// DividendEvent represents a dividend payment received
type DividendEvent struct {
	ID            uuid.UUID       `json:"id"`
	UserID        uuid.UUID       `json:"user_id"`
	Symbol        string          `json:"symbol"`
	Amount        decimal.Decimal `json:"amount"`
	SharesHeld    decimal.Decimal `json:"shares_held"`
	ExDate        time.Time       `json:"ex_date"`
	PayDate       time.Time       `json:"pay_date"`
	ReceivedAt    time.Time       `json:"received_at"`
	Reinvested    bool            `json:"reinvested"`
	ReinvestedAt  *time.Time      `json:"reinvested_at,omitempty"`
	ReinvestOrder *uuid.UUID      `json:"reinvest_order_id,omitempty"`
}

// DividendRepository manages dividend records
type DividendRepository interface {
	Create(ctx context.Context, event *DividendEvent) error
	GetByUserID(ctx context.Context, userID uuid.UUID, limit int) ([]*DividendEvent, error)
	GetPendingReinvestment(ctx context.Context) ([]*DividendEvent, error)
	MarkReinvested(ctx context.Context, id uuid.UUID, orderID uuid.UUID) error
	GetTotalReinvested(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
}

// InvestmentRulesRepository retrieves user DRIP config
type InvestmentRulesRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.InvestmentRulesConfig, error)
	UpdateDRIPStats(ctx context.Context, userID uuid.UUID, totalReinvested decimal.Decimal, lastDividend *time.Time) error
}

// OrderPlacer places reinvestment orders
type OrderPlacer interface {
	PlaceMarketOrder(ctx context.Context, userID uuid.UUID, symbol string, amount decimal.Decimal) (*entities.AlpacaOrderResponse, error)
}

// StrategyProvider gets user's target allocations for reinvestment
type StrategyProvider interface {
	GetTargetAllocations(ctx context.Context, userID uuid.UUID) (map[string]decimal.Decimal, error)
}

// Service handles dividend reinvestment
type Service struct {
	dividendRepo  DividendRepository
	rulesRepo     InvestmentRulesRepository
	orderPlacer   OrderPlacer
	strategyProvider StrategyProvider
	logger        *zap.Logger
}

// NewService creates a new DRIP service
func NewService(
	dividendRepo DividendRepository,
	rulesRepo InvestmentRulesRepository,
	orderPlacer OrderPlacer,
	strategyProvider StrategyProvider,
	logger *zap.Logger,
) *Service {
	return &Service{
		dividendRepo:     dividendRepo,
		rulesRepo:        rulesRepo,
		orderPlacer:      orderPlacer,
		strategyProvider: strategyProvider,
		logger:           logger,
	}
}

// ProcessDividend handles an incoming dividend payment
func (s *Service) ProcessDividend(ctx context.Context, userID uuid.UUID, symbol string, amount decimal.Decimal, sharesHeld decimal.Decimal) error {
	// Create dividend event record
	event := &DividendEvent{
		ID:         uuid.New(),
		UserID:     userID,
		Symbol:     symbol,
		Amount:     amount,
		SharesHeld: sharesHeld,
		ReceivedAt: time.Now(),
		Reinvested: false,
	}

	if err := s.dividendRepo.Create(ctx, event); err != nil {
		return fmt.Errorf("failed to record dividend: %w", err)
	}

	s.logger.Info("Dividend received",
		zap.String("user_id", userID.String()),
		zap.String("symbol", symbol),
		zap.String("amount", amount.String()))

	// Check if user has DRIP enabled
	rules, err := s.rulesRepo.GetByUserID(ctx, userID)
	if err != nil {
		s.logger.Warn("Failed to get DRIP config, skipping reinvestment",
			zap.String("user_id", userID.String()),
			zap.Error(err))
		return nil
	}

	if rules == nil || rules.DRIPConfig == nil || !rules.DRIPConfig.Enabled {
		s.logger.Debug("DRIP not enabled for user",
			zap.String("user_id", userID.String()))
		return nil
	}

	// Process reinvestment
	return s.reinvestDividend(ctx, event, rules.DRIPConfig)
}

func (s *Service) reinvestDividend(ctx context.Context, event *DividendEvent, config *entities.DRIPConfig) error {
	// Calculate reinvestment amount
	reinvestAmount := event.Amount.Mul(config.ReinvestPct).Div(decimal.NewFromInt(100))

	// Check minimum threshold
	if reinvestAmount.LessThan(config.MinReinvestAmount) {
		s.logger.Debug("Dividend below minimum reinvestment threshold",
			zap.String("user_id", event.UserID.String()),
			zap.String("amount", reinvestAmount.String()),
			zap.String("min", config.MinReinvestAmount.String()))
		return nil
	}

	var orderID uuid.UUID
	var err error

	if config.ReinvestInSame {
		// Reinvest in the same asset that paid the dividend
		orderID, err = s.reinvestInSymbol(ctx, event.UserID, event.Symbol, reinvestAmount)
	} else {
		// Reinvest according to user's strategy allocation
		orderID, err = s.reinvestByStrategy(ctx, event.UserID, reinvestAmount)
	}

	if err != nil {
		return fmt.Errorf("failed to reinvest dividend: %w", err)
	}

	// Mark dividend as reinvested
	if err := s.dividendRepo.MarkReinvested(ctx, event.ID, orderID); err != nil {
		s.logger.Warn("Failed to mark dividend as reinvested",
			zap.String("dividend_id", event.ID.String()),
			zap.Error(err))
	}

	// Update user's DRIP stats
	totalReinvested, _ := s.dividendRepo.GetTotalReinvested(ctx, event.UserID)
	now := time.Now()
	_ = s.rulesRepo.UpdateDRIPStats(ctx, event.UserID, totalReinvested.Add(reinvestAmount), &now)

	s.logger.Info("Dividend reinvested",
		zap.String("user_id", event.UserID.String()),
		zap.String("symbol", event.Symbol),
		zap.String("amount", reinvestAmount.String()),
		zap.String("order_id", orderID.String()))

	return nil
}

func (s *Service) reinvestInSymbol(ctx context.Context, userID uuid.UUID, symbol string, amount decimal.Decimal) (uuid.UUID, error) {
	order, err := s.orderPlacer.PlaceMarketOrder(ctx, userID, symbol, amount)
	if err != nil {
		return uuid.Nil, err
	}
	// Parse the string ID to UUID
	orderID, err := uuid.Parse(order.ID)
	if err != nil {
		s.logger.Warn("Failed to parse order ID as UUID, using new UUID",
			zap.String("order_id", order.ID),
			zap.Error(err))
		return uuid.New(), nil
	}
	return orderID, nil
}

func (s *Service) reinvestByStrategy(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (uuid.UUID, error) {
	// Get user's target allocations
	targets, err := s.strategyProvider.GetTargetAllocations(ctx, userID)
	if err != nil {
		// Fallback to VTI if strategy unavailable
		return s.reinvestInSymbol(ctx, userID, "VTI", amount)
	}

	// Find the highest weighted symbol and reinvest there
	var maxSymbol string
	maxWeight := decimal.Zero
	for symbol, weight := range targets {
		if weight.GreaterThan(maxWeight) {
			maxWeight = weight
			maxSymbol = symbol
		}
	}

	if maxSymbol == "" {
		maxSymbol = "VTI" // Fallback
	}

	return s.reinvestInSymbol(ctx, userID, maxSymbol, amount)
}

// ProcessPendingReinvestments processes any dividends that failed to reinvest
func (s *Service) ProcessPendingReinvestments(ctx context.Context) error {
	pending, err := s.dividendRepo.GetPendingReinvestment(ctx)
	if err != nil {
		return fmt.Errorf("failed to get pending reinvestments: %w", err)
	}

	for _, event := range pending {
		rules, err := s.rulesRepo.GetByUserID(ctx, event.UserID)
		if err != nil || rules == nil || rules.DRIPConfig == nil || !rules.DRIPConfig.Enabled {
			continue
		}

		if err := s.reinvestDividend(ctx, event, rules.DRIPConfig); err != nil {
			s.logger.Error("Failed to process pending reinvestment",
				zap.String("dividend_id", event.ID.String()),
				zap.Error(err))
		}
	}

	return nil
}

// GetDividendHistory returns dividend history for a user
func (s *Service) GetDividendHistory(ctx context.Context, userID uuid.UUID, limit int) ([]*DividendEvent, error) {
	return s.dividendRepo.GetByUserID(ctx, userID, limit)
}

// GetDRIPStats returns DRIP statistics for a user
func (s *Service) GetDRIPStats(ctx context.Context, userID uuid.UUID) (*DRIPStats, error) {
	rules, err := s.rulesRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	totalReinvested, _ := s.dividendRepo.GetTotalReinvested(ctx, userID)

	stats := &DRIPStats{
		Enabled:         rules != nil && rules.DRIPConfig != nil && rules.DRIPConfig.Enabled,
		TotalReinvested: totalReinvested,
	}

	if rules != nil && rules.DRIPConfig != nil {
		stats.ReinvestPct = rules.DRIPConfig.ReinvestPct
		stats.ReinvestInSame = rules.DRIPConfig.ReinvestInSame
		stats.LastDividend = rules.DRIPConfig.LastDividend
	}

	return stats, nil
}

// DRIPStats contains DRIP statistics for a user
type DRIPStats struct {
	Enabled         bool            `json:"enabled"`
	ReinvestPct     decimal.Decimal `json:"reinvest_pct"`
	ReinvestInSame  bool            `json:"reinvest_in_same"`
	TotalReinvested decimal.Decimal `json:"total_reinvested"`
	LastDividend    *time.Time      `json:"last_dividend,omitempty"`
}
