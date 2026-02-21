package entities

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// InvestmentRulesConfig holds all investment rules for a user
type InvestmentRulesConfig struct {
	ID                    uuid.UUID              `json:"id" db:"id"`
	UserID                uuid.UUID              `json:"user_id" db:"user_id"`
	AgeBasedAllocation    *AgeBasedAllocation    `json:"age_based_allocation,omitempty" db:"age_based_allocation"`
	RebalancingConfig     *AutoRebalancingConfig `json:"rebalancing_config,omitempty" db:"rebalancing_config"`
	DRIPConfig            *DRIPConfig            `json:"drip_config,omitempty" db:"drip_config"`
	WithdrawalCooling     *WithdrawalCooling     `json:"withdrawal_cooling,omitempty" db:"withdrawal_cooling"`
	RoundUpMultiplier     RoundUpMultiplier      `json:"round_up_multiplier" db:"round_up_multiplier"`
	MilestoneNotifications bool                  `json:"milestone_notifications" db:"milestone_notifications"`
	CreatedAt             time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt             time.Time              `json:"updated_at" db:"updated_at"`
}

// AgeBasedAllocation configures age-based stock/bond allocation
type AgeBasedAllocation struct {
	Enabled       bool            `json:"enabled" db:"enabled"`
	Formula       AgeFormula      `json:"formula" db:"formula"`         // "100-age" or "120-age"
	MinStockPct   decimal.Decimal `json:"min_stock_pct" db:"min_stock_pct"` // Floor for stock allocation
	MaxStockPct   decimal.Decimal `json:"max_stock_pct" db:"max_stock_pct"` // Ceiling for stock allocation
	LastAdjusted  *time.Time      `json:"last_adjusted,omitempty" db:"last_adjusted"`
}

// AgeFormula represents the formula used for age-based allocation
type AgeFormula string

const (
	AgeFormula100MinusAge AgeFormula = "100-age" // Traditional conservative
	AgeFormula120MinusAge AgeFormula = "120-age" // Modern longevity-adjusted
)

// CalculateStockAllocation returns stock percentage based on age
func (a *AgeBasedAllocation) CalculateStockAllocation(age int) decimal.Decimal {
	if !a.Enabled {
		return decimal.NewFromInt(60) // Default 60% stocks
	}

	var base int
	switch a.Formula {
	case AgeFormula120MinusAge:
		base = 120
	default:
		base = 100
	}

	stockPct := decimal.NewFromInt(int64(base - age))

	// Apply floor
	if stockPct.LessThan(a.MinStockPct) {
		stockPct = a.MinStockPct
	}
	// Apply ceiling
	if stockPct.GreaterThan(a.MaxStockPct) {
		stockPct = a.MaxStockPct
	}

	return stockPct
}

// AutoRebalancingConfig configures automatic portfolio rebalancing
type AutoRebalancingConfig struct {
	Enabled          bool            `json:"enabled" db:"enabled"`
	ThresholdPct     decimal.Decimal `json:"threshold_pct" db:"threshold_pct"`       // Drift threshold to trigger (e.g., 5%)
	Frequency        RebalanceFreq   `json:"frequency" db:"frequency"`               // How often to check
	LastChecked      *time.Time      `json:"last_checked,omitempty" db:"last_checked"`
	LastRebalanced   *time.Time      `json:"last_rebalanced,omitempty" db:"last_rebalanced"`
	NotifyOnRebalance bool           `json:"notify_on_rebalance" db:"notify_on_rebalance"`
}

// RebalanceFreq represents rebalancing check frequency
type RebalanceFreq string

const (
	RebalanceFreqDaily    RebalanceFreq = "daily"
	RebalanceFreqWeekly   RebalanceFreq = "weekly"
	RebalanceFreqMonthly  RebalanceFreq = "monthly"
	RebalanceFreqQuarterly RebalanceFreq = "quarterly"
)

// DRIPConfig configures dividend reinvestment
type DRIPConfig struct {
	Enabled           bool            `json:"enabled" db:"enabled"`
	ReinvestPct       decimal.Decimal `json:"reinvest_pct" db:"reinvest_pct"`         // % of dividends to reinvest (default 100)
	MinReinvestAmount decimal.Decimal `json:"min_reinvest_amount" db:"min_reinvest_amount"` // Min amount to trigger reinvestment
	ReinvestInSame    bool            `json:"reinvest_in_same" db:"reinvest_in_same"` // Reinvest in same asset or strategy
	TotalReinvested   decimal.Decimal `json:"total_reinvested" db:"total_reinvested"` // Lifetime reinvested amount
	LastDividend      *time.Time      `json:"last_dividend,omitempty" db:"last_dividend"`
}

// WithdrawalCooling configures withdrawal cooling-off period
type WithdrawalCooling struct {
	Enabled          bool          `json:"enabled" db:"enabled"`
	CoolingPeriod    time.Duration `json:"cooling_period" db:"cooling_period"` // Default 24 hours
	BypassForSmall   bool          `json:"bypass_for_small" db:"bypass_for_small"` // Skip for small amounts
	SmallThreshold   decimal.Decimal `json:"small_threshold" db:"small_threshold"` // What counts as "small"
	PendingWithdrawals []PendingWithdrawal `json:"pending_withdrawals,omitempty" db:"-"`
}

// PendingWithdrawal represents a withdrawal in cooling-off period
type PendingWithdrawal struct {
	ID            uuid.UUID               `json:"id" db:"id"`
	UserID        uuid.UUID               `json:"user_id" db:"user_id"`
	Amount        decimal.Decimal         `json:"amount" db:"amount"`
	RequestedAt   time.Time               `json:"requested_at" db:"requested_at"`
	ExecuteAfter  time.Time               `json:"execute_after" db:"execute_after"`
	Status        PendingWithdrawalStatus `json:"status" db:"status"`
	CancelledAt   *time.Time              `json:"cancelled_at,omitempty" db:"cancelled_at"`
	ExecutedAt    *time.Time              `json:"executed_at,omitempty" db:"executed_at"`
}

// PendingWithdrawalStatus represents the status of a pending withdrawal
type PendingWithdrawalStatus string

const (
	PendingWithdrawalStatusPending   PendingWithdrawalStatus = "pending"
	PendingWithdrawalStatusCancelled PendingWithdrawalStatus = "cancelled"
	PendingWithdrawalStatusExecuted  PendingWithdrawalStatus = "executed"
	PendingWithdrawalStatusExpired   PendingWithdrawalStatus = "expired"
)

// CanExecute checks if withdrawal can be executed
func (p *PendingWithdrawal) CanExecute() bool {
	return p.Status == PendingWithdrawalStatusPending && time.Now().After(p.ExecuteAfter)
}

// TimeRemaining returns time until withdrawal can execute
func (p *PendingWithdrawal) TimeRemaining() time.Duration {
	if p.CanExecute() {
		return 0
	}
	return time.Until(p.ExecuteAfter)
}

// RoundUpMultiplier represents the round-up acceleration factor
type RoundUpMultiplier int

const (
	RoundUpMultiplier1x  RoundUpMultiplier = 1
	RoundUpMultiplier2x  RoundUpMultiplier = 2
	RoundUpMultiplier5x  RoundUpMultiplier = 5
	RoundUpMultiplier10x RoundUpMultiplier = 10
)

// Validate validates the round-up multiplier
func (m RoundUpMultiplier) Validate() error {
	switch m {
	case RoundUpMultiplier1x, RoundUpMultiplier2x, RoundUpMultiplier5x, RoundUpMultiplier10x:
		return nil
	default:
		return fmt.Errorf("invalid round-up multiplier: %d (must be 1, 2, 5, or 10)", m)
	}
}

// InvestmentMilestone represents a milestone achievement
type InvestmentMilestone struct {
	ID          uuid.UUID       `json:"id" db:"id"`
	UserID      uuid.UUID       `json:"user_id" db:"user_id"`
	Type        MilestoneType   `json:"type" db:"type"`
	Amount      decimal.Decimal `json:"amount" db:"amount"`
	AchievedAt  time.Time       `json:"achieved_at" db:"achieved_at"`
	Celebrated  bool            `json:"celebrated" db:"celebrated"`
	CelebratedAt *time.Time     `json:"celebrated_at,omitempty" db:"celebrated_at"`
}

// MilestoneType represents the type of milestone
type MilestoneType string

const (
	MilestoneTypeBalance     MilestoneType = "balance"      // Total invested balance
	MilestoneTypeContribution MilestoneType = "contribution" // Total contributions
	MilestoneTypeGain        MilestoneType = "gain"         // Total gains
	MilestoneTypeStreak      MilestoneType = "streak"       // Consecutive investment days
)

// MilestoneThresholds defines the milestone amounts to celebrate
var MilestoneThresholds = []decimal.Decimal{
	decimal.NewFromInt(100),
	decimal.NewFromInt(500),
	decimal.NewFromInt(1000),
	decimal.NewFromInt(2500),
	decimal.NewFromInt(5000),
	decimal.NewFromInt(10000),
	decimal.NewFromInt(25000),
	decimal.NewFromInt(50000),
	decimal.NewFromInt(100000),
}

// GetNextMilestone returns the next milestone amount to achieve
func GetNextMilestone(currentAmount decimal.Decimal) *decimal.Decimal {
	for _, threshold := range MilestoneThresholds {
		if currentAmount.LessThan(threshold) {
			return &threshold
		}
	}
	return nil
}

// GetAchievedMilestones returns all milestones achieved at current amount
func GetAchievedMilestones(currentAmount decimal.Decimal) []decimal.Decimal {
	var achieved []decimal.Decimal
	for _, threshold := range MilestoneThresholds {
		if currentAmount.GreaterThanOrEqual(threshold) {
			achieved = append(achieved, threshold)
		}
	}
	return achieved
}

// NewDefaultInvestmentRulesConfig creates default investment rules for a new user
func NewDefaultInvestmentRulesConfig(userID uuid.UUID) *InvestmentRulesConfig {
	now := time.Now()
	return &InvestmentRulesConfig{
		ID:     uuid.New(),
		UserID: userID,
		AgeBasedAllocation: &AgeBasedAllocation{
			Enabled:     true,
			Formula:     AgeFormula120MinusAge, // Modern formula
			MinStockPct: decimal.NewFromInt(20),
			MaxStockPct: decimal.NewFromInt(95),
		},
		RebalancingConfig: &AutoRebalancingConfig{
			Enabled:           true,
			ThresholdPct:      decimal.NewFromInt(5), // 5% drift threshold
			Frequency:         RebalanceFreqQuarterly,
			NotifyOnRebalance: false, // Silent by default (Rail philosophy)
		},
		DRIPConfig: &DRIPConfig{
			Enabled:           true,
			ReinvestPct:       decimal.NewFromInt(100), // Reinvest all dividends
			MinReinvestAmount: decimal.NewFromFloat(1.0),
			ReinvestInSame:    true,
			TotalReinvested:   decimal.Zero,
		},
		WithdrawalCooling: &WithdrawalCooling{
			Enabled:        true,
			CoolingPeriod:  24 * time.Hour, // 24-hour rule
			BypassForSmall: true,
			SmallThreshold: decimal.NewFromInt(50), // $50 or less bypasses cooling
		},
		RoundUpMultiplier:      RoundUpMultiplier1x,
		MilestoneNotifications: true,
		CreatedAt:              now,
		UpdatedAt:              now,
	}
}

// Validate validates the investment rules config
func (c *InvestmentRulesConfig) Validate() error {
	if c.ID == uuid.Nil {
		return fmt.Errorf("config ID is required")
	}
	if c.UserID == uuid.Nil {
		return fmt.Errorf("user ID is required")
	}
	if err := c.RoundUpMultiplier.Validate(); err != nil {
		return err
	}
	return nil
}
