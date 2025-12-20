package strategy

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
)

// Allocation represents a single asset allocation within a strategy
type Allocation struct {
	Symbol string          // Trading symbol (e.g., "SPY", "QQQ", "BND")
	Weight decimal.Decimal // Percentage weight (0-100)
}

// StrategyResult contains the computed allocation for a user
type StrategyResult struct {
	StrategyName string       // Name of the selected strategy
	Allocations  []Allocation // Asset allocations with weights
}

// UserSignals contains signals used for strategy personalization
type UserSignals struct {
	UserID      uuid.UUID
	Age         *int       // Calculated from DateOfBirth
	DateOfBirth *time.Time // Raw date of birth
}

// UserProfileProvider retrieves user profile data for signal extraction
type UserProfileProvider interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
}

// Engine determines investment strategy based on user signals
type Engine struct {
	userProvider UserProfileProvider
	logger       *logger.Logger
}

// NewEngine creates a new strategy engine
func NewEngine(userProvider UserProfileProvider, logger *logger.Logger) *Engine {
	return &Engine{
		userProvider: userProvider,
		logger:       logger,
	}
}

// GetStrategy determines the optimal strategy for a user based on their signals
func (e *Engine) GetStrategy(ctx context.Context, userID uuid.UUID) (*StrategyResult, error) {
	signals, err := e.collectSignals(ctx, userID)
	if err != nil {
		e.logger.Warn("Failed to collect user signals, using fallback strategy",
			"user_id", userID,
			"error", err)
		return e.getFallbackStrategy(), nil
	}

	return e.selectStrategy(signals), nil
}

// collectSignals gathers user signals for strategy personalization
func (e *Engine) collectSignals(ctx context.Context, userID uuid.UUID) (*UserSignals, error) {
	signals := &UserSignals{UserID: userID}

	if e.userProvider == nil {
		return signals, nil
	}

	profile, err := e.userProvider.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	if profile != nil && profile.DateOfBirth != nil {
		signals.DateOfBirth = profile.DateOfBirth
		age := calculateAge(*profile.DateOfBirth)
		signals.Age = &age
	}

	return signals, nil
}

// selectStrategy chooses a strategy based on user signals
func (e *Engine) selectStrategy(signals *UserSignals) *StrategyResult {
	// Age-based strategy selection
	if signals.Age != nil {
		age := *signals.Age

		// Young investors (18-25): More aggressive growth
		if age >= 18 && age <= 25 {
			return e.getAggressiveGrowthStrategy()
		}

		// Mid-age investors (26-40): Balanced growth
		if age >= 26 && age <= 40 {
			return e.getBalancedGrowthStrategy()
		}

		// Mature investors (41+): Conservative
		if age > 40 {
			return e.getConservativeStrategy()
		}
	}

	// Default to fallback if no age signal
	return e.getFallbackStrategy()
}

// getFallbackStrategy returns the global default strategy
// 60% ETF (broad market), 25% Tech, 15% Bonds
func (e *Engine) getFallbackStrategy() *StrategyResult {
	return &StrategyResult{
		StrategyName: "Global Fallback",
		Allocations: []Allocation{
			{Symbol: "SPY", Weight: decimal.NewFromInt(60)},  // S&P 500 ETF
			{Symbol: "QQQ", Weight: decimal.NewFromInt(25)},  // Tech-heavy NASDAQ ETF
			{Symbol: "BND", Weight: decimal.NewFromInt(15)},  // Bond ETF for stability
		},
	}
}

// getAggressiveGrowthStrategy for young investors (18-25)
// Higher tech and growth exposure
func (e *Engine) getAggressiveGrowthStrategy() *StrategyResult {
	return &StrategyResult{
		StrategyName: "Aggressive Growth",
		Allocations: []Allocation{
			{Symbol: "QQQ", Weight: decimal.NewFromInt(40)},  // Tech-heavy NASDAQ
			{Symbol: "SPY", Weight: decimal.NewFromInt(35)},  // S&P 500
			{Symbol: "VUG", Weight: decimal.NewFromInt(25)},  // Growth ETF
		},
	}
}

// getBalancedGrowthStrategy for mid-age investors (26-40)
// Balanced between growth and stability
func (e *Engine) getBalancedGrowthStrategy() *StrategyResult {
	return &StrategyResult{
		StrategyName: "Balanced Growth",
		Allocations: []Allocation{
			{Symbol: "SPY", Weight: decimal.NewFromInt(50)},  // S&P 500
			{Symbol: "QQQ", Weight: decimal.NewFromInt(30)},  // Tech exposure
			{Symbol: "BND", Weight: decimal.NewFromInt(20)},  // Bonds
		},
	}
}

// getConservativeStrategy for mature investors (41+)
// Focus on stability and income
func (e *Engine) getConservativeStrategy() *StrategyResult {
	return &StrategyResult{
		StrategyName: "Conservative",
		Allocations: []Allocation{
			{Symbol: "SPY", Weight: decimal.NewFromInt(40)},  // S&P 500
			{Symbol: "BND", Weight: decimal.NewFromInt(35)},  // Bonds
			{Symbol: "VYM", Weight: decimal.NewFromInt(25)},  // Dividend ETF
		},
	}
}

// calculateAge computes age from date of birth
func calculateAge(dob time.Time) int {
	now := time.Now()
	age := now.Year() - dob.Year()

	// Adjust if birthday hasn't occurred this year
	if now.YearDay() < dob.YearDay() {
		age--
	}

	return age
}
