package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const (
	ConsciousSpendingPlanStatusCommitted = "committed"
	ConsciousSpendingPlanStatusPaused    = "paused"

	CheckInCadenceWeekly   = "weekly"
	CheckInCadenceBiweekly = "biweekly"
	CheckInCadenceMonthly  = "monthly"
)

// ConsciousSpendingPlan is the user's numeric monthly commitment. The
// personal reason behind the plan stays in Miriam's memory, not this record.
type ConsciousSpendingPlan struct {
	UserID               uuid.UUID       `json:"user_id" db:"user_id"`
	TakeHomeIncome       decimal.Decimal `json:"take_home_income" db:"take_home_income"`
	Currency             string          `json:"currency" db:"currency"`
	FixedCosts           decimal.Decimal `json:"fixed_costs" db:"fixed_costs"`
	Investments          decimal.Decimal `json:"investments" db:"investments"`
	Savings              decimal.Decimal `json:"savings" db:"savings"`
	GuiltFreeSpending    decimal.Decimal `json:"guilt_free_spending" db:"guilt_free_spending"`
	FixedCostsPct        decimal.Decimal `json:"fixed_costs_pct" db:"fixed_costs_pct"`
	InvestmentsPct       decimal.Decimal `json:"investments_pct" db:"investments_pct"`
	SavingsPct           decimal.Decimal `json:"savings_pct" db:"savings_pct"`
	GuiltFreeSpendingPct decimal.Decimal `json:"guilt_free_spending_pct" db:"guilt_free_spending_pct"`
	Status               string          `json:"status" db:"status"`
	CheckInCadence       string          `json:"check_in_cadence" db:"check_in_cadence"`
	CommittedAt          *time.Time      `json:"committed_at,omitempty" db:"committed_at"`
	CreatedAt            time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at" db:"updated_at"`
}

type ConsciousSpendingPlanCheckIn struct {
	Plan    ConsciousSpendingPlan
	Country string
}
