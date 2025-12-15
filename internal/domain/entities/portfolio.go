package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type RebalanceStrategy string

const (
	RebalanceStrategyThreshold RebalanceStrategy = "threshold"
	RebalanceStrategyPeriodic  RebalanceStrategy = "periodic"
	RebalanceStrategyDrift     RebalanceStrategy = "drift"
)

type PortfolioRebalance struct {
	ID              uuid.UUID         `json:"id" db:"id"`
	PortfolioID     uuid.UUID         `json:"portfolio_id" db:"portfolio_id"`
	Strategy        RebalanceStrategy `json:"strategy" db:"strategy"`
	TargetAllocations map[string]decimal.Decimal `json:"target_allocations" db:"target_allocations"`
	CurrentAllocations map[string]decimal.Decimal `json:"current_allocations" db:"current_allocations"`
	Trades          []RebalanceTrade  `json:"trades" db:"trades"`
	Status          string            `json:"status" db:"status"`
	ExecutedAt      *time.Time        `json:"executed_at,omitempty" db:"executed_at"`
	CreatedAt       time.Time         `json:"created_at" db:"created_at"`
}

type RebalanceTrade struct {
	Symbol   string          `json:"symbol"`
	Action   string          `json:"action"`
	Quantity decimal.Decimal `json:"quantity"`
	Price    decimal.Decimal `json:"price"`
}

type TaxReport struct {
	ID          uuid.UUID       `json:"id" db:"id"`
	UserID      uuid.UUID       `json:"user_id" db:"user_id"`
	TaxYear     int             `json:"tax_year" db:"tax_year"`
	TotalGains  decimal.Decimal `json:"total_gains" db:"total_gains"`
	TotalLosses decimal.Decimal `json:"total_losses" db:"total_losses"`
	ShortTermGains decimal.Decimal `json:"short_term_gains" db:"short_term_gains"`
	LongTermGains  decimal.Decimal `json:"long_term_gains" db:"long_term_gains"`
	ReportURL   string          `json:"report_url" db:"report_url"`
	GeneratedAt time.Time       `json:"generated_at" db:"generated_at"`
}
