package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const (
	ConsciousSpendingPlanStatusCommitted  = "committed"
	ConsciousSpendingPlanStatusSuperseded = "superseded"
	ConsciousSpendingPlanStatusPaused     = "paused"

	ConsciousSpendingPlanScopeHousehold = "household"

	IncomeBasisStableAverage    = "stable_average"
	IncomeBasisConservativeFloor = "conservative_floor"
	IncomeBasisCurrentMonth     = "current_month"
	IncomeBasisUserProvided     = "user_provided"

	CheckInCadenceWeekly   = "weekly"
	CheckInCadenceBiweekly = "biweekly"
	CheckInCadenceMonthly  = "monthly"

	CSPItemBucketFixedCost  = "fixed_cost"
	CSPItemBucketInvestment = "investment"
	CSPItemBucketSavings    = "savings"

	CSPAmountSourceUserProvided = "user_provided"
	CSPAmountSourceProfile      = "financial_profile"
	CSPAmountSourceObligation   = "obligation"
	CSPAmountSourceRail         = "rail"
	CSPAmountSourceMono         = "mono"
	CSPAmountSourcePayroll      = "payroll"
	CSPAmountSourceDerived      = "derived"
)

// ConsciousSpendingPlan is a versioned, user-owned household commitment.
// Personal motivations stay in Miriam memory; operational numbers live here.
type ConsciousSpendingPlan struct {
	ID                      uuid.UUID       `json:"id" db:"id"`
	UserID                  uuid.UUID       `json:"user_id" db:"user_id"`
	Version                 int             `json:"version" db:"version"`
	Scope                   string          `json:"scope" db:"scope"`
	Country                 string          `json:"country" db:"country"`
	BaseCurrency            string          `json:"base_currency" db:"base_currency"`
	GrossMonthlyIncome      decimal.Decimal `json:"gross_monthly_income" db:"gross_monthly_income"`
	PayrollDeductions       decimal.Decimal `json:"payroll_deductions" db:"payroll_deductions"`
	PreTaxInvestments       decimal.Decimal `json:"pre_tax_investments" db:"pre_tax_investments"`
	TakeHomeIncome          decimal.Decimal `json:"take_home_income" db:"take_home_income"`
	IncomeCadence           string          `json:"income_cadence" db:"income_cadence"`
	IncomeBasis             string          `json:"income_basis" db:"income_basis"`
	IncomeSource            string          `json:"income_source" db:"income_source"`
	IncomeConfidence        string          `json:"income_confidence" db:"income_confidence"`
	FixedCostsSubtotal      decimal.Decimal `json:"fixed_costs_subtotal" db:"fixed_costs_subtotal"`
	MiscBufferRate          decimal.Decimal `json:"misc_buffer_rate" db:"misc_buffer_rate"`
	MiscBufferAmount        decimal.Decimal `json:"misc_buffer_amount" db:"misc_buffer_amount"`
	FixedCosts              decimal.Decimal `json:"fixed_costs" db:"fixed_costs"`
	PostTaxInvestments      decimal.Decimal `json:"post_tax_investments" db:"post_tax_investments"`
	Savings                 decimal.Decimal `json:"savings" db:"savings"`
	GuiltFreeSpending       decimal.Decimal `json:"guilt_free_spending" db:"guilt_free_spending"`
	FixedCostsPct           decimal.Decimal `json:"fixed_costs_pct" db:"fixed_costs_pct"`
	InvestmentsPct          decimal.Decimal `json:"investments_pct" db:"investments_pct"`
	SavingsPct              decimal.Decimal `json:"savings_pct" db:"savings_pct"`
	GuiltFreeSpendingPct    decimal.Decimal `json:"guilt_free_spending_pct" db:"guilt_free_spending_pct"`
	Status                  string          `json:"status" db:"status"`
	CheckInCadence          string          `json:"check_in_cadence" db:"check_in_cadence"`
	CommittedAt             *time.Time      `json:"committed_at,omitempty" db:"committed_at"`
	CreatedAt               time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt               time.Time       `json:"updated_at" db:"updated_at"`
	Items                   []ConsciousSpendingPlanItem `json:"items,omitempty" db:"-"`
	NetWorth                *ConsciousSpendingNetWorth  `json:"net_worth,omitempty" db:"-"`
}

// ConsciousSpendingPlanItem is a user-confirmed recurring commitment,
// post-tax investment, or savings goal. Original-currency fields preserve
// cross-border provenance while Amount is normalized to the plan currency.
type ConsciousSpendingPlanItem struct {
	ID               uuid.UUID       `json:"id" db:"id"`
	PlanID           uuid.UUID       `json:"plan_id" db:"plan_id"`
	Bucket           string          `json:"bucket" db:"bucket"`
	Name             string          `json:"name" db:"name"`
	Amount           decimal.Decimal `json:"amount" db:"amount"`
	Cadence          string          `json:"cadence" db:"cadence"`
	Source           string          `json:"source" db:"source"`
	Confidence       string          `json:"confidence" db:"confidence"`
	OriginalAmount   decimal.Decimal `json:"original_amount" db:"original_amount"`
	OriginalCurrency string          `json:"original_currency" db:"original_currency"`
	FXRate           decimal.Decimal `json:"fx_rate" db:"fx_rate"`
	FXRateAt         *time.Time      `json:"fx_rate_at,omitempty" db:"fx_rate_at"`
	EvidenceRef      string          `json:"evidence_ref,omitempty" db:"evidence_ref"`
	DisplayOrder     int             `json:"display_order" db:"display_order"`
	CreatedAt        time.Time       `json:"created_at" db:"created_at"`
}

type ConsciousSpendingNetWorth struct {
	ID          uuid.UUID       `json:"id" db:"id"`
	PlanID      uuid.UUID       `json:"plan_id" db:"plan_id"`
	UserID      uuid.UUID       `json:"user_id" db:"user_id"`
	Currency    string          `json:"currency" db:"currency"`
	Assets      decimal.Decimal `json:"assets" db:"assets"`
	Investments decimal.Decimal `json:"investments" db:"investments"`
	Savings     decimal.Decimal `json:"savings" db:"savings"`
	Debt        decimal.Decimal `json:"debt" db:"debt"`
	Total       decimal.Decimal `json:"total" db:"total"`
	Source      string          `json:"source" db:"source"`
	Confidence  string          `json:"confidence" db:"confidence"`
	CapturedAt  time.Time       `json:"captured_at" db:"captured_at"`
}

type ConsciousSpendingPlanCheckIn struct {
	Plan    ConsciousSpendingPlan
	Country string
}
