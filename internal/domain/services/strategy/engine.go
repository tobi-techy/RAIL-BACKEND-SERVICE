package strategy

import (
	"context"
	"strings"
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
	UserID           uuid.UUID
	Age              *int                         // Calculated from DateOfBirth
	DateOfBirth      *time.Time                   // Raw date of birth
	RiskConfig       *entities.AgeBasedAllocation // User's custom risk config
	DepositCount30d  int                          // Number of confirmed deposits in last 30 days
	DepositAmount    decimal.Decimal              // Current deposit amount being invested
	Region           RegionClass                  // Derived from UserProfile.Country
}

// RegionClass classifies a user's region for portfolio construction
type RegionClass int

const (
	RegionUS RegionClass = iota // Default: USD-denominated, US-centric
	RegionUK                    // GBP base: needs currency-hedged intl exposure
	RegionEU                    // EUR base: similar to UK
	RegionOther                 // Rest of world: broader EM tilt
)

// DepositFrequency classifies how often a user deposits
type DepositFrequency int

const (
	FreqRare     DepositFrequency = iota // 0-1 deposits/month: conservative, lump-sum investor
	FreqOccasional                       // 2-3 deposits/month: moderate
	FreqRegular                          // 4+ deposits/month: DCA investor, can tolerate more volatility
)

// UserProfileProvider retrieves user profile data for signal extraction
type UserProfileProvider interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
}

// InvestmentRulesProvider retrieves user's investment rules config
type InvestmentRulesProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.InvestmentRulesConfig, error)
}

// DepositFrequencyProvider counts recent confirmed deposits for a user
type DepositFrequencyProvider interface {
	CountConfirmedByUserIDSince(ctx context.Context, userID uuid.UUID, since time.Time) (int, error)
}

// Engine determines investment strategy based on user signals
type Engine struct {
	userProvider      UserProfileProvider
	rulesProvider     InvestmentRulesProvider
	frequencyProvider DepositFrequencyProvider
	logger            *logger.Logger
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

// SetFrequencyProvider sets the deposit frequency provider (for DI)
func (e *Engine) SetFrequencyProvider(provider DepositFrequencyProvider) {
	e.frequencyProvider = provider
}

// GetStrategy determines the optimal strategy for a user based on their signals
func (e *Engine) GetStrategy(ctx context.Context, userID uuid.UUID) (*StrategyResult, error) {
	return e.GetStrategyForAmount(ctx, userID, decimal.Zero)
}

// GetStrategyForAmount determines strategy with deposit amount for small-deposit collapse
func (e *Engine) GetStrategyForAmount(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (*StrategyResult, error) {
	signals, _ := e.collectSignals(ctx, userID) // best-effort; never returns a hard error
	if signals == nil {
		signals = &UserSignals{UserID: userID}
	}
	signals.DepositAmount = amount
	return e.selectStrategy(signals), nil
}

// collectSignals gathers user signals for strategy personalization.
// Each signal is collected independently (best-effort) — a failure in one does not block others.
func (e *Engine) collectSignals(ctx context.Context, userID uuid.UUID) (*UserSignals, error) {
	signals := &UserSignals{UserID: userID}

	if e.userProvider != nil {
		profile, err := e.userProvider.GetByID(ctx, userID)
		if err != nil {
			e.logger.Warn("Failed to fetch user profile for strategy signals, continuing without age/region",
				"user_id", userID, "error", err)
		} else if profile != nil {
			if profile.DateOfBirth != nil {
				signals.DateOfBirth = profile.DateOfBirth
				age := calculateAge(*profile.DateOfBirth)
				signals.Age = &age
			}
			signals.Region = classifyRegion(profile.Country)
		}
	}

	if e.rulesProvider != nil {
		rules, err := e.rulesProvider.GetByUserID(ctx, userID)
		if err == nil && rules != nil && rules.AgeBasedAllocation != nil {
			signals.RiskConfig = rules.AgeBasedAllocation
		}
	}

	if e.frequencyProvider != nil {
		count, err := e.frequencyProvider.CountConfirmedByUserIDSince(ctx, userID, time.Now().AddDate(0, 0, -30))
		if err == nil {
			signals.DepositCount30d = count
		}
	}

	return signals, nil
}

// classifyRegion maps a country code/name to a RegionClass
func classifyRegion(country *string) RegionClass {
	if country == nil {
		return RegionOther
	}
	switch strings.ToUpper(strings.TrimSpace(*country)) {
	case "US", "USA", "UNITED STATES":
		return RegionUS
	case "GB", "UK", "GBR", "UNITED KINGDOM":
		return RegionUK
	case "DE", "FR", "IT", "ES", "NL", "BE", "AT", "PT", "IE", "FI", "SE", "NO", "DK",
		"DEU", "FRA", "ITA", "ESP", "NLD", "BEL", "AUT", "PRT", "IRL", "FIN", "SWE", "NOR", "DNK":
		return RegionEU
	default:
		return RegionOther
	}
}

// depositFrequency classifies a raw 30-day deposit count
func depositFrequency(count30d int) DepositFrequency {
	switch {
	case count30d >= 4:
		return FreqRegular
	case count30d >= 2:
		return FreqOccasional
	default:
		return FreqRare
	}
}

// selectStrategy chooses a strategy based on user signals
func (e *Engine) selectStrategy(signals *UserSignals) *StrategyResult {
	if signals.Age == nil {
		return e.applyRegionAndSizeAdjustments(e.getFallbackStrategy(), signals)
	}

	age := *signals.Age
	stockPct := e.calculateStockAllocation(age, signals.RiskConfig)

	// Frequency boost: regular DCA depositors get up to +5% stock allocation
	// (they benefit from cost-averaging so can tolerate slightly more volatility)
	freq := depositFrequency(signals.DepositCount30d)
	switch freq {
	case FreqRegular:
		stockPct = stockPct.Add(decimal.NewFromInt(5))
	case FreqOccasional:
		stockPct = stockPct.Add(decimal.NewFromInt(2))
	}
	// Re-clamp after boost
	if stockPct.GreaterThan(decimal.NewFromInt(95)) {
		stockPct = decimal.NewFromInt(95)
	}

	bondPct := decimal.NewFromInt(100).Sub(stockPct)
	result := e.buildAgeBasedStrategy(age, stockPct, bondPct)
	return e.applyRegionAndSizeAdjustments(result, signals)
}

// applyRegionAndSizeAdjustments modifies allocations based on region and deposit size.
// Region swaps happen FIRST so collapsed ETFs are already the hedged versions.
func (e *Engine) applyRegionAndSizeAdjustments(result *StrategyResult, signals *UserSignals) *StrategyResult {
	// Region adjustment first: swap broad international ETFs for currency-hedged equivalents
	switch signals.Region {
	case RegionUK:
		result.Allocations = replaceSymbol(result.Allocations, "VEA", "HEFA")  // iShares Currency Hedged MSCI EAFE
		result.Allocations = replaceSymbol(result.Allocations, "VWO", "HEWJ")  // Hedged EM proxy
		result.Allocations = replaceSymbol(result.Allocations, "EFAV", "HEFA") // Hedged min-vol intl
	case RegionEU:
		result.Allocations = replaceSymbol(result.Allocations, "VEA", "HEFA")
		result.Allocations = replaceSymbol(result.Allocations, "EFAV", "HEFA")
	case RegionOther:
		result.Allocations = replaceSymbol(result.Allocations, "VEA", "VWO") // Tilt toward EM for non-US/EU/UK
	}

	// Small deposit collapse AFTER region swap: reduce ETF count to avoid sub-$1 orders
	if !signals.DepositAmount.IsZero() && signals.DepositAmount.LessThan(decimal.NewFromInt(20)) {
		result.Allocations = collapseToTopN(result.Allocations, 2)
	} else if !signals.DepositAmount.IsZero() && signals.DepositAmount.LessThan(decimal.NewFromInt(50)) {
		result.Allocations = collapseToTopN(result.Allocations, 3)
	}

	return result
}

// collapseToTopN keeps the top N allocations by weight and renormalises to exactly 100%.
// Rounding remainder is added to the largest weight to ensure full deployment.
func collapseToTopN(allocs []Allocation, n int) []Allocation {
	if len(allocs) <= n {
		return allocs
	}
	sorted := make([]Allocation, len(allocs))
	copy(sorted, allocs)
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].Weight.GreaterThan(sorted[i].Weight) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	top := sorted[:n]
	total := decimal.Zero
	for _, a := range top {
		total = total.Add(a.Weight)
	}
	if total.IsZero() {
		return top
	}
	hundred := decimal.NewFromInt(100)
	assignedTotal := decimal.Zero
	for i := range top {
		top[i].Weight = top[i].Weight.Mul(hundred).Div(total).Truncate(2)
		assignedTotal = assignedTotal.Add(top[i].Weight)
	}
	// Add any truncation remainder to the largest (first) weight
	remainder := hundred.Sub(assignedTotal)
	if !remainder.IsZero() {
		top[0].Weight = top[0].Weight.Add(remainder)
	}
	return top
}

// replaceSymbol swaps one ETF symbol for another, preserving weight
func replaceSymbol(allocs []Allocation, from, to string) []Allocation {
	for i := range allocs {
		if allocs[i].Symbol == from {
			allocs[i].Symbol = to
			return allocs
		}
	}
	return allocs
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

// buildGenZEarlyStrategy for ages 18-22: Pure US growth, maximum risk tolerance.
// Equity: large-cap growth + Nasdaq tech + small-cap. No international, near-zero bonds.
func (e *Engine) buildGenZEarlyStrategy(stockPct, bondPct decimal.Decimal, age int) *StrategyResult {
	return &StrategyResult{
		StrategyName:    "Gen Z Early Career",
		StockAllocation: stockPct,
		BondAllocation:  bondPct,
		AgeUsed:         &age,
		Allocations: []Allocation{
			{Symbol: "VUG", Weight: stockPct.Mul(decimal.NewFromFloat(0.40))},  // Large-cap growth (Vanguard)
			{Symbol: "QQQ", Weight: stockPct.Mul(decimal.NewFromFloat(0.35))},  // Nasdaq-100 tech/innovation
			{Symbol: "VB", Weight: stockPct.Mul(decimal.NewFromFloat(0.25))},   // Small-cap blend (long-run premium)
			{Symbol: "SHV", Weight: bondPct},                                    // Ultra-short T-bills (near-cash)
		},
	}
}

// buildGenZLateStrategy for ages 23-27: US growth + emerging markets + small-cap.
// First international exposure via EM; small-cap still present for long horizon.
func (e *Engine) buildGenZLateStrategy(stockPct, bondPct decimal.Decimal, age int) *StrategyResult {
	return &StrategyResult{
		StrategyName:    "Gen Z Late Career",
		StockAllocation: stockPct,
		BondAllocation:  bondPct,
		AgeUsed:         &age,
		Allocations: []Allocation{
			{Symbol: "VUG", Weight: stockPct.Mul(decimal.NewFromFloat(0.35))},  // Large-cap growth
			{Symbol: "QQQ", Weight: stockPct.Mul(decimal.NewFromFloat(0.25))},  // Nasdaq-100
			{Symbol: "VB", Weight: stockPct.Mul(decimal.NewFromFloat(0.20))},   // Small-cap blend
			{Symbol: "VWO", Weight: stockPct.Mul(decimal.NewFromFloat(0.20))},  // Emerging markets (growth upside)
			{Symbol: "SHV", Weight: bondPct},                                    // Ultra-short T-bills
		},
	}
}

// buildMillennialEarlyStrategy for ages 28-35: Broad US + developed intl + small-cap value.
// Growth tilt fades; value factor and developed-market diversification enter.
func (e *Engine) buildMillennialEarlyStrategy(stockPct, bondPct decimal.Decimal, age int) *StrategyResult {
	return &StrategyResult{
		StrategyName:    "Millennial Growth",
		StockAllocation: stockPct,
		BondAllocation:  bondPct,
		AgeUsed:         &age,
		Allocations: []Allocation{
			{Symbol: "VTI", Weight: stockPct.Mul(decimal.NewFromFloat(0.40))},  // Total US market (broad)
			{Symbol: "VEA", Weight: stockPct.Mul(decimal.NewFromFloat(0.25))},  // Developed markets ex-US (Europe/Japan)
			{Symbol: "VBR", Weight: stockPct.Mul(decimal.NewFromFloat(0.20))},  // Small-cap value (Fama-French premium)
			{Symbol: "VWO", Weight: stockPct.Mul(decimal.NewFromFloat(0.15))},  // Emerging markets
			{Symbol: "BND", Weight: bondPct},                                    // Total bond market
		},
	}
}

// buildMillennialLateStrategy for ages 36-45: Core US + developed intl + dividend income + REITs.
// Small-cap exits; income and real assets enter for the first time.
func (e *Engine) buildMillennialLateStrategy(stockPct, bondPct decimal.Decimal, age int) *StrategyResult {
	return &StrategyResult{
		StrategyName:    "Millennial Balanced",
		StockAllocation: stockPct,
		BondAllocation:  bondPct,
		AgeUsed:         &age,
		Allocations: []Allocation{
			{Symbol: "VTI", Weight: stockPct.Mul(decimal.NewFromFloat(0.40))},  // Total US market
			{Symbol: "VEA", Weight: stockPct.Mul(decimal.NewFromFloat(0.25))},  // Developed markets ex-US
			{Symbol: "VYM", Weight: stockPct.Mul(decimal.NewFromFloat(0.20))},  // High-dividend yield US
			{Symbol: "VNQ", Weight: stockPct.Mul(decimal.NewFromFloat(0.15))},  // US REITs (real asset / inflation hedge)
			{Symbol: "BND", Weight: bondPct.Mul(decimal.NewFromFloat(0.70))},   // Core bonds
			{Symbol: "VTIP", Weight: bondPct.Mul(decimal.NewFromFloat(0.30))},  // TIPS (inflation protection)
		},
	}
}

// buildGenXStrategy for ages 46-55: Dividend-heavy US + low-volatility intl + REITs.
// Growth fully removed; income and downside protection dominate equity side.
func (e *Engine) buildGenXStrategy(stockPct, bondPct decimal.Decimal, age int) *StrategyResult {
	return &StrategyResult{
		StrategyName:    "Gen X Stability",
		StockAllocation: stockPct,
		BondAllocation:  bondPct,
		AgeUsed:         &age,
		Allocations: []Allocation{
			{Symbol: "VYM", Weight: stockPct.Mul(decimal.NewFromFloat(0.35))},  // High-dividend yield US (income focus)
			{Symbol: "VTI", Weight: stockPct.Mul(decimal.NewFromFloat(0.30))},  // Total US market (core)
			{Symbol: "EFAV", Weight: stockPct.Mul(decimal.NewFromFloat(0.20))}, // MSCI EAFE min-volatility (low-vol intl)
			{Symbol: "VNQ", Weight: stockPct.Mul(decimal.NewFromFloat(0.15))},  // REITs
			{Symbol: "BND", Weight: bondPct.Mul(decimal.NewFromFloat(0.55))},   // Core bonds
			{Symbol: "VTIP", Weight: bondPct.Mul(decimal.NewFromFloat(0.45))},  // TIPS (heavier inflation hedge)
		},
	}
}

// buildPreRetirementStrategy for ages 56-65: Capital preservation, short-duration bonds, min-vol equity.
// Sequence-of-returns risk managed via low-vol ETFs and short-duration bond ladder.
func (e *Engine) buildPreRetirementStrategy(stockPct, bondPct decimal.Decimal, age int) *StrategyResult {
	return &StrategyResult{
		StrategyName:    "Pre-Retirement",
		StockAllocation: stockPct,
		BondAllocation:  bondPct,
		AgeUsed:         &age,
		Allocations: []Allocation{
			{Symbol: "VYM", Weight: stockPct.Mul(decimal.NewFromFloat(0.40))},  // High-dividend yield (income)
			{Symbol: "USMV", Weight: stockPct.Mul(decimal.NewFromFloat(0.35))}, // US min-volatility (downside protection)
			{Symbol: "EFAV", Weight: stockPct.Mul(decimal.NewFromFloat(0.25))}, // Intl min-volatility
			{Symbol: "VCSH", Weight: bondPct.Mul(decimal.NewFromFloat(0.40))},  // Short-term corporate (low duration risk)
			{Symbol: "VTIP", Weight: bondPct.Mul(decimal.NewFromFloat(0.35))},  // TIPS
			{Symbol: "BND", Weight: bondPct.Mul(decimal.NewFromFloat(0.25))},   // Core bonds
		},
	}
}

// buildRetirementStrategy for ages 66+: Pure income + capital preservation, laddered short bonds.
// No growth exposure; VYM + USMV for equity income, three-rung bond ladder for stability.
func (e *Engine) buildRetirementStrategy(stockPct, bondPct decimal.Decimal, age int) *StrategyResult {
	return &StrategyResult{
		StrategyName:    "Retirement Income",
		StockAllocation: stockPct,
		BondAllocation:  bondPct,
		AgeUsed:         &age,
		Allocations: []Allocation{
			{Symbol: "VYM", Weight: stockPct.Mul(decimal.NewFromFloat(0.55))},  // High-dividend yield (primary income)
			{Symbol: "USMV", Weight: stockPct.Mul(decimal.NewFromFloat(0.45))}, // US min-volatility (preservation)
			{Symbol: "VCSH", Weight: bondPct.Mul(decimal.NewFromFloat(0.40))},  // Short-term corporate
			{Symbol: "VTIP", Weight: bondPct.Mul(decimal.NewFromFloat(0.35))},  // TIPS
			{Symbol: "VGSH", Weight: bondPct.Mul(decimal.NewFromFloat(0.25))},  // Short-term Treasuries (safest)
		},
	}
}

// getFallbackStrategy returns the global default when no age signal is available.
// Balanced 70/30: broad US + developed intl + bonds. No growth tilt, no factor bets.
func (e *Engine) getFallbackStrategy() *StrategyResult {
	return &StrategyResult{
		StrategyName:    "Global Fallback",
		StockAllocation: decimal.NewFromInt(70),
		BondAllocation:  decimal.NewFromInt(30),
		Allocations: []Allocation{
			{Symbol: "VTI", Weight: decimal.NewFromInt(42)},  // Total US market
			{Symbol: "VEA", Weight: decimal.NewFromInt(18)},  // Developed markets ex-US
			{Symbol: "VWO", Weight: decimal.NewFromInt(10)},  // Emerging markets
			{Symbol: "BND", Weight: decimal.NewFromInt(20)},  // Total bond market
			{Symbol: "VTIP", Weight: decimal.NewFromInt(10)}, // TIPS (inflation hedge)
		},
	}
}

// calculateAge computes age from date of birth
func calculateAge(dob time.Time) int {
	now := time.Now()
	age := now.Year() - dob.Year()
	// Subtract 1 if birthday hasn't occurred yet this year (compare month+day, not YearDay)
	if now.Month() < dob.Month() || (now.Month() == dob.Month() && now.Day() < dob.Day()) {
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
