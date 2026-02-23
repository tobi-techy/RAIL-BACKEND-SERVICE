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
	StrategyName    string          // Name of the selected strategy
	Allocations     []Allocation    // Asset allocations with weights
	StockAllocation decimal.Decimal // Total stock allocation percentage
	BondAllocation  decimal.Decimal // Total bond allocation percentage
	AgeUsed         *int            // Age used for calculation (if applicable)
}

// UserSignals contains signals used for strategy personalization
type UserSignals struct {
	UserID      uuid.UUID
	Age         *int       // Calculated from DateOfBirth
	DateOfBirth *time.Time // Raw date of birth
	RiskConfig  *entities.AgeBasedAllocation // User's custom risk config
}

// UserProfileProvider retrieves user profile data for signal extraction
type UserProfileProvider interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
}

// InvestmentRulesProvider retrieves user's investment rules config
type InvestmentRulesProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.InvestmentRulesConfig, error)
}

// Engine determines investment strategy based on user signals
type Engine struct {
	userProvider  UserProfileProvider
	rulesProvider InvestmentRulesProvider
	logger        *logger.Logger
}

// NewEngine creates a new strategy engine
func NewEngine(userProvider UserProfileProvider, logger *logger.Logger) *Engine {
	return &Engine{
		userProvider: userProvider,
		logger:       logger,
	}
}

// SetRulesProvider sets the investment rules provider (for DI)
func (e *Engine) SetRulesProvider(provider InvestmentRulesProvider) {
	e.rulesProvider = provider
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

	if e.userProvider != nil {
		profile, err := e.userProvider.GetByID(ctx, userID)
		if err != nil {
			return nil, err
		}
		if profile != nil && profile.DateOfBirth != nil {
			signals.DateOfBirth = profile.DateOfBirth
			age := calculateAge(*profile.DateOfBirth)
			signals.Age = &age
		}
	}

	// Get user's custom investment rules if available
	if e.rulesProvider != nil {
		rules, err := e.rulesProvider.GetByUserID(ctx, userID)
		if err == nil && rules != nil && rules.AgeBasedAllocation != nil {
			signals.RiskConfig = rules.AgeBasedAllocation
		}
	}

	return signals, nil
}

// selectStrategy chooses a strategy based on user signals using 120-age formula
func (e *Engine) selectStrategy(signals *UserSignals) *StrategyResult {
	if signals.Age == nil {
		return e.getFallbackStrategy()
	}

	age := *signals.Age
	stockPct := e.calculateStockAllocation(age, signals.RiskConfig)
	bondPct := decimal.NewFromInt(100).Sub(stockPct)

	return e.buildAgeBasedStrategy(age, stockPct, bondPct)
}

// calculateStockAllocation uses 120-age formula with configurable bounds
func (e *Engine) calculateStockAllocation(age int, config *entities.AgeBasedAllocation) decimal.Decimal {
	// Use custom config if provided
	if config != nil && config.Enabled {
		return config.CalculateStockAllocation(age)
	}

	// Default: 120-age formula (modern longevity-adjusted)
	stockPct := decimal.NewFromInt(int64(120 - age))

	// Apply default bounds: 20% min, 95% max
	minStock := decimal.NewFromInt(20)
	maxStock := decimal.NewFromInt(95)

	if stockPct.LessThan(minStock) {
		stockPct = minStock
	}
	if stockPct.GreaterThan(maxStock) {
		stockPct = maxStock
	}

	return stockPct
}

// buildAgeBasedStrategy creates allocation based on stock/bond split
func (e *Engine) buildAgeBasedStrategy(age int, stockPct, bondPct decimal.Decimal) *StrategyResult {
	// Granular age brackets with smooth transitions
	switch {
	case age >= 18 && age <= 22:
		return e.buildGenZEarlyStrategy(stockPct, bondPct, age)
	case age >= 23 && age <= 27:
		return e.buildGenZLateStrategy(stockPct, bondPct, age)
	case age >= 28 && age <= 35:
		return e.buildMillennialEarlyStrategy(stockPct, bondPct, age)
	case age >= 36 && age <= 45:
		return e.buildMillennialLateStrategy(stockPct, bondPct, age)
	case age >= 46 && age <= 55:
		return e.buildGenXStrategy(stockPct, bondPct, age)
	case age >= 56 && age <= 65:
		return e.buildPreRetirementStrategy(stockPct, bondPct, age)
	default:
		return e.buildRetirementStrategy(stockPct, bondPct, age)
	}
}

// buildGenZEarlyStrategy for ages 18-22: Maximum growth, high risk tolerance
func (e *Engine) buildGenZEarlyStrategy(stockPct, bondPct decimal.Decimal, age int) *StrategyResult {
	// Within stocks: heavy growth/tech tilt
	return &StrategyResult{
		StrategyName:    "Gen Z Early Career",
		StockAllocation: stockPct,
		BondAllocation:  bondPct,
		AgeUsed:         &age,
		Allocations: []Allocation{
			{Symbol: "VTI", Weight: stockPct.Mul(decimal.NewFromFloat(0.35))},  // Total US market
			{Symbol: "QQQ", Weight: stockPct.Mul(decimal.NewFromFloat(0.35))},  // Tech/growth
			{Symbol: "VUG", Weight: stockPct.Mul(decimal.NewFromFloat(0.20))},  // Growth ETF
			{Symbol: "VXUS", Weight: stockPct.Mul(decimal.NewFromFloat(0.10))}, // International
			{Symbol: "BND", Weight: bondPct},                                    // Bonds (minimal)
		},
	}
}

// buildGenZLateStrategy for ages 23-27: High growth with slight diversification
func (e *Engine) buildGenZLateStrategy(stockPct, bondPct decimal.Decimal, age int) *StrategyResult {
	return &StrategyResult{
		StrategyName:    "Gen Z Late Career",
		StockAllocation: stockPct,
		BondAllocation:  bondPct,
		AgeUsed:         &age,
		Allocations: []Allocation{
			{Symbol: "VTI", Weight: stockPct.Mul(decimal.NewFromFloat(0.40))},  // Total US market
			{Symbol: "QQQ", Weight: stockPct.Mul(decimal.NewFromFloat(0.25))},  // Tech/growth
			{Symbol: "VUG", Weight: stockPct.Mul(decimal.NewFromFloat(0.15))},  // Growth ETF
			{Symbol: "VXUS", Weight: stockPct.Mul(decimal.NewFromFloat(0.20))}, // International
			{Symbol: "BND", Weight: bondPct},                                    // Bonds
		},
	}
}

// buildMillennialEarlyStrategy for ages 28-35: Balanced growth
func (e *Engine) buildMillennialEarlyStrategy(stockPct, bondPct decimal.Decimal, age int) *StrategyResult {
	return &StrategyResult{
		StrategyName:    "Millennial Growth",
		StockAllocation: stockPct,
		BondAllocation:  bondPct,
		AgeUsed:         &age,
		Allocations: []Allocation{
			{Symbol: "VTI", Weight: stockPct.Mul(decimal.NewFromFloat(0.45))},  // Total US market
			{Symbol: "VXUS", Weight: stockPct.Mul(decimal.NewFromFloat(0.25))}, // International
			{Symbol: "QQQ", Weight: stockPct.Mul(decimal.NewFromFloat(0.15))},  // Tech exposure
			{Symbol: "VNQ", Weight: stockPct.Mul(decimal.NewFromFloat(0.15))},  // Real estate
			{Symbol: "BND", Weight: bondPct},                                    // Bonds
		},
	}
}

// buildMillennialLateStrategy for ages 36-45: Growth with stability
func (e *Engine) buildMillennialLateStrategy(stockPct, bondPct decimal.Decimal, age int) *StrategyResult {
	return &StrategyResult{
		StrategyName:    "Millennial Balanced",
		StockAllocation: stockPct,
		BondAllocation:  bondPct,
		AgeUsed:         &age,
		Allocations: []Allocation{
			{Symbol: "VTI", Weight: stockPct.Mul(decimal.NewFromFloat(0.50))},  // Total US market
			{Symbol: "VXUS", Weight: stockPct.Mul(decimal.NewFromFloat(0.25))}, // International
			{Symbol: "VYM", Weight: stockPct.Mul(decimal.NewFromFloat(0.15))},  // Dividend income
			{Symbol: "VNQ", Weight: stockPct.Mul(decimal.NewFromFloat(0.10))},  // Real estate
			{Symbol: "BND", Weight: bondPct.Mul(decimal.NewFromFloat(0.70))},   // Core bonds
			{Symbol: "VTIP", Weight: bondPct.Mul(decimal.NewFromFloat(0.30))},  // Inflation protected
		},
	}
}

// buildGenXStrategy for ages 46-55: Stability with growth
func (e *Engine) buildGenXStrategy(stockPct, bondPct decimal.Decimal, age int) *StrategyResult {
	return &StrategyResult{
		StrategyName:    "Gen X Stability",
		StockAllocation: stockPct,
		BondAllocation:  bondPct,
		AgeUsed:         &age,
		Allocations: []Allocation{
			{Symbol: "VTI", Weight: stockPct.Mul(decimal.NewFromFloat(0.45))},  // Total US market
			{Symbol: "VXUS", Weight: stockPct.Mul(decimal.NewFromFloat(0.20))}, // International
			{Symbol: "VYM", Weight: stockPct.Mul(decimal.NewFromFloat(0.25))},  // Dividend income
			{Symbol: "VNQ", Weight: stockPct.Mul(decimal.NewFromFloat(0.10))},  // Real estate
			{Symbol: "BND", Weight: bondPct.Mul(decimal.NewFromFloat(0.60))},   // Core bonds
			{Symbol: "VTIP", Weight: bondPct.Mul(decimal.NewFromFloat(0.40))},  // Inflation protected
		},
	}
}

// buildPreRetirementStrategy for ages 56-65: Capital preservation focus
func (e *Engine) buildPreRetirementStrategy(stockPct, bondPct decimal.Decimal, age int) *StrategyResult {
	return &StrategyResult{
		StrategyName:    "Pre-Retirement",
		StockAllocation: stockPct,
		BondAllocation:  bondPct,
		AgeUsed:         &age,
		Allocations: []Allocation{
			{Symbol: "VTI", Weight: stockPct.Mul(decimal.NewFromFloat(0.40))},  // Total US market
			{Symbol: "VYM", Weight: stockPct.Mul(decimal.NewFromFloat(0.35))},  // Dividend income
			{Symbol: "VXUS", Weight: stockPct.Mul(decimal.NewFromFloat(0.15))}, // International
			{Symbol: "VNQ", Weight: stockPct.Mul(decimal.NewFromFloat(0.10))},  // Real estate
			{Symbol: "BND", Weight: bondPct.Mul(decimal.NewFromFloat(0.50))},   // Core bonds
			{Symbol: "VTIP", Weight: bondPct.Mul(decimal.NewFromFloat(0.30))},  // Inflation protected
			{Symbol: "VCSH", Weight: bondPct.Mul(decimal.NewFromFloat(0.20))},  // Short-term corporate
		},
	}
}

// buildRetirementStrategy for ages 66+: Income and preservation
func (e *Engine) buildRetirementStrategy(stockPct, bondPct decimal.Decimal, age int) *StrategyResult {
	return &StrategyResult{
		StrategyName:    "Retirement Income",
		StockAllocation: stockPct,
		BondAllocation:  bondPct,
		AgeUsed:         &age,
		Allocations: []Allocation{
			{Symbol: "VYM", Weight: stockPct.Mul(decimal.NewFromFloat(0.50))},  // Dividend income
			{Symbol: "VTI", Weight: stockPct.Mul(decimal.NewFromFloat(0.30))},  // Total US market
			{Symbol: "VXUS", Weight: stockPct.Mul(decimal.NewFromFloat(0.20))}, // International
			{Symbol: "BND", Weight: bondPct.Mul(decimal.NewFromFloat(0.40))},   // Core bonds
			{Symbol: "VTIP", Weight: bondPct.Mul(decimal.NewFromFloat(0.30))},  // Inflation protected
			{Symbol: "VCSH", Weight: bondPct.Mul(decimal.NewFromFloat(0.30))},  // Short-term corporate
		},
	}
}

// getFallbackStrategy returns the global default strategy
func (e *Engine) getFallbackStrategy() *StrategyResult {
	return &StrategyResult{
		StrategyName:    "Global Fallback",
		StockAllocation: decimal.NewFromInt(70),
		BondAllocation:  decimal.NewFromInt(30),
		Allocations: []Allocation{
			{Symbol: "VTI", Weight: decimal.NewFromInt(45)},  // Total US market
			{Symbol: "VXUS", Weight: decimal.NewFromInt(15)}, // International
			{Symbol: "QQQ", Weight: decimal.NewFromInt(10)},  // Tech exposure
			{Symbol: "BND", Weight: decimal.NewFromInt(30)},  // Bonds
		},
	}
}

// calculateAge computes age from date of birth
func calculateAge(dob time.Time) int {
	now := time.Now()
	age := now.Year() - dob.Year()
	if now.YearDay() < dob.YearDay() {
		age--
	}
	return age
}

// GetStockAllocationForAge returns the stock allocation for a given age (utility function)
func GetStockAllocationForAge(age int) decimal.Decimal {
	stockPct := decimal.NewFromInt(int64(120 - age))
	minStock := decimal.NewFromInt(20)
	maxStock := decimal.NewFromInt(95)
	if stockPct.LessThan(minStock) {
		return minStock
	}
	if stockPct.GreaterThan(maxStock) {
		return maxStock
	}
	return stockPct
}
