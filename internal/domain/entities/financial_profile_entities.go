package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// FinancialProfile stores user-specific financial context that Miriam can use
// to personalize guidance without relying on chat history alone.
type FinancialProfile struct {
	UserID               uuid.UUID              `json:"user_id" db:"user_id"`
	PrimaryCurrency      string                 `json:"primary_currency" db:"primary_currency"`
	IncomeFrequency      string                 `json:"income_frequency" db:"income_frequency"`
	MonthlyIncome        decimal.Decimal        `json:"monthly_income" db:"monthly_income"`
	MonthlyFixedCosts    decimal.Decimal        `json:"monthly_fixed_costs" db:"monthly_fixed_costs"`
	MonthlySavingsTarget decimal.Decimal        `json:"monthly_savings_target" db:"monthly_savings_target"`
	EmergencyFundTarget  decimal.Decimal        `json:"emergency_fund_target" db:"emergency_fund_target"`
	RiskTolerance        string                 `json:"risk_tolerance" db:"risk_tolerance"`
	InvestmentHorizon    string                 `json:"investment_horizon" db:"investment_horizon"`
	FinancialGoal        string                 `json:"financial_goal" db:"financial_goal"`
	Metadata             map[string]interface{} `json:"metadata,omitempty" db:"metadata"`
	CreatedAt            time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt            time.Time              `json:"updated_at" db:"updated_at"`
}

// FinancialProfileUpdate is a partial update payload for financial_profiles.
// Nil fields mean "leave unchanged".
type FinancialProfileUpdate struct {
	PrimaryCurrency      *string
	IncomeFrequency      *string
	MonthlyIncome        *decimal.Decimal
	MonthlyFixedCosts    *decimal.Decimal
	MonthlySavingsTarget *decimal.Decimal
	EmergencyFundTarget  *decimal.Decimal
	RiskTolerance        *string
	InvestmentHorizon    *string
	FinancialGoal        *string
	Metadata             map[string]interface{}
}
