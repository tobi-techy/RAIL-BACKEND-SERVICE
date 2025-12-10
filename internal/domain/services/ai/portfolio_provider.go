package ai

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// PortfolioValueProvider interface for portfolio value data
type PortfolioValueProvider interface {
	GetPortfolioValue(ctx context.Context, userID uuid.UUID, date time.Time) (decimal.Decimal, error)
}

// PositionRepository interface for position data
type PositionRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.Position, error)
}

// BasketAllocationProvider interface for basket allocation data
type BasketAllocationProvider interface {
	GetUserBasketAllocations(ctx context.Context, userID uuid.UUID) ([]*BasketAllocation, error)
}

// BasketAllocation represents a user's allocation to a basket
type BasketAllocation struct {
	BasketID   uuid.UUID
	BasketName string
	Value      decimal.Decimal
	Weight     decimal.Decimal
}

// ContributionRepository interface for contribution data
type ContributionRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID, contributionType *entities.ContributionType, startDate, endDate *time.Time, limit, offset int) ([]*entities.UserContribution, error)
	GetTotalByType(ctx context.Context, userID uuid.UUID, startDate, endDate time.Time) (map[entities.ContributionType]string, error)
}

// StreakRepository interface for streak data
type StreakRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.InvestmentStreak, error)
}

// PortfolioDataProviderImpl implements PortfolioDataProvider
type PortfolioDataProviderImpl struct {
	portfolioValueProvider PortfolioValueProvider
	positionRepo           PositionRepository
	logger                 *zap.Logger
}

// NewPortfolioDataProvider creates a new portfolio data provider
func NewPortfolioDataProvider(
	portfolioValueProvider PortfolioValueProvider,
	positionRepo PositionRepository,
	logger *zap.Logger,
) *PortfolioDataProviderImpl {
	return &PortfolioDataProviderImpl{
		portfolioValueProvider: portfolioValueProvider,
		positionRepo:           positionRepo,
		logger:                 logger,
	}
}

// GetWeeklyStats returns weekly portfolio statistics
func (p *PortfolioDataProviderImpl) GetWeeklyStats(ctx context.Context, userID uuid.UUID) (*PortfolioStats, error) {
	now := time.Now()
	weekAgo := now.AddDate(0, 0, -7)

	// Get current portfolio value
	currentValue, err := p.portfolioValueProvider.GetPortfolioValue(ctx, userID, now)
	if err != nil {
		p.logger.Warn("Failed to get current portfolio value", zap.Error(err))
		currentValue = decimal.Zero
	}

	// Get week-ago portfolio value
	weekAgoValue, err := p.portfolioValueProvider.GetPortfolioValue(ctx, userID, weekAgo)
	if err != nil {
		p.logger.Warn("Failed to get week-ago portfolio value", zap.Error(err))
		weekAgoValue = decimal.Zero
	}

	// Calculate returns
	weeklyReturn := currentValue.Sub(weekAgoValue)
	weeklyReturnPct := decimal.Zero
	if weekAgoValue.GreaterThan(decimal.Zero) {
		weeklyReturnPct = weeklyReturn.Div(weekAgoValue)
	}

	return &PortfolioStats{
		TotalValue:      currentValue,
		WeeklyReturn:    weeklyReturn,
		WeeklyReturnPct: weeklyReturnPct,
		MonthlyReturn:   weeklyReturn.Mul(decimal.NewFromInt(4)), // Approximate
		TotalGainLoss:   weeklyReturn,
	}, nil
}

// GetTopMovers returns top gaining/losing positions
func (p *PortfolioDataProviderImpl) GetTopMovers(ctx context.Context, userID uuid.UUID, limit int) ([]*Mover, error) {
	positions, err := p.positionRepo.GetByUserID(ctx, userID)
	if err != nil {
		p.logger.Warn("Failed to get positions", zap.Error(err))
		return []*Mover{}, nil
	}

	movers := make([]*Mover, 0, len(positions))
	for _, pos := range positions {
		// Calculate return based on avg_price and market_value
		costBasis := pos.AvgPrice.Mul(pos.Quantity)
		if costBasis.IsZero() {
			continue
		}
		returnAmt := pos.MarketValue.Sub(costBasis)
		returnPct := returnAmt.Div(costBasis)

		movers = append(movers, &Mover{
			Symbol:    pos.BasketID.String()[:8], // Use basket ID prefix as identifier
			Name:      "Basket Position",
			Return:    returnAmt,
			ReturnPct: returnPct,
		})
	}

	// Sort by absolute return percentage (top movers either direction)
	for i := 0; i < len(movers)-1; i++ {
		for j := i + 1; j < len(movers); j++ {
			if movers[j].ReturnPct.Abs().GreaterThan(movers[i].ReturnPct.Abs()) {
				movers[i], movers[j] = movers[j], movers[i]
			}
		}
	}

	if len(movers) > limit {
		movers = movers[:limit]
	}

	return movers, nil
}

// GetAllocations returns portfolio allocations by basket
func (p *PortfolioDataProviderImpl) GetAllocations(ctx context.Context, userID uuid.UUID) ([]*Allocation, error) {
	positions, err := p.positionRepo.GetByUserID(ctx, userID)
	if err != nil {
		p.logger.Warn("Failed to get positions for allocations", zap.Error(err))
		return []*Allocation{}, nil
	}

	// Calculate total value
	totalValue := decimal.Zero
	for _, pos := range positions {
		totalValue = totalValue.Add(pos.MarketValue)
	}

	// Build allocations
	result := make([]*Allocation, 0, len(positions))
	for _, pos := range positions {
		weight := decimal.Zero
		if totalValue.GreaterThan(decimal.Zero) {
			weight = pos.MarketValue.Div(totalValue)
		}

		result = append(result, &Allocation{
			BasketID:   pos.BasketID,
			BasketName: "Basket " + pos.BasketID.String()[:8],
			Value:      pos.MarketValue,
			Weight:     weight,
		})
	}

	return result, nil
}

// ActivityDataProviderImpl implements ActivityDataProvider
type ActivityDataProviderImpl struct {
	contributionRepo ContributionRepository
	streakRepo       StreakRepository
	logger           *zap.Logger
}

// NewActivityDataProvider creates a new activity data provider
func NewActivityDataProvider(
	contributionRepo ContributionRepository,
	streakRepo StreakRepository,
	logger *zap.Logger,
) *ActivityDataProviderImpl {
	return &ActivityDataProviderImpl{
		contributionRepo: contributionRepo,
		streakRepo:       streakRepo,
		logger:           logger,
	}
}

// GetContributions returns contribution summary
func (a *ActivityDataProviderImpl) GetContributions(ctx context.Context, userID uuid.UUID, contributionType string, startDate, endDate time.Time) (*ContributionSummary, error) {
	totals, err := a.contributionRepo.GetTotalByType(ctx, userID, startDate, endDate)
	if err != nil {
		a.logger.Warn("Failed to get contribution totals", zap.Error(err))
		return &ContributionSummary{}, nil
	}

	summary := &ContributionSummary{
		Deposits: decimal.Zero,
		Roundups: decimal.Zero,
		Cashback: decimal.Zero,
		Total:    decimal.Zero,
	}

	for contribType, amountStr := range totals {
		amount, _ := decimal.NewFromString(amountStr)
		switch contribType {
		case entities.ContributionTypeDeposit:
			summary.Deposits = amount
		case entities.ContributionTypeRoundup:
			summary.Roundups = amount
		case entities.ContributionTypeCashback:
			summary.Cashback = amount
		}
		summary.Total = summary.Total.Add(amount)
	}

	return summary, nil
}

// GetStreak returns user's investment streak
func (a *ActivityDataProviderImpl) GetStreak(ctx context.Context, userID uuid.UUID) (*entities.InvestmentStreak, error) {
	streak, err := a.streakRepo.GetByUserID(ctx, userID)
	if err != nil {
		a.logger.Warn("Failed to get streak", zap.Error(err))
		return &entities.InvestmentStreak{
			UserID:        userID,
			CurrentStreak: 0,
			LongestStreak: 0,
		}, nil
	}
	return streak, nil
}
