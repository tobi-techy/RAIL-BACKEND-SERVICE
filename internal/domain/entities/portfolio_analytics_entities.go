package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// PortfolioSnapshot represents a point-in-time portfolio value
type PortfolioSnapshot struct {
	ID              uuid.UUID       `json:"id" db:"id"`
	UserID          uuid.UUID       `json:"user_id" db:"user_id"`
	TotalValue      decimal.Decimal `json:"total_value" db:"total_value"`
	CashValue       decimal.Decimal `json:"cash_value" db:"cash_value"`
	InvestedValue   decimal.Decimal `json:"invested_value" db:"invested_value"`
	TotalCostBasis  decimal.Decimal `json:"total_cost_basis" db:"total_cost_basis"`
	TotalGainLoss   decimal.Decimal `json:"total_gain_loss" db:"total_gain_loss"`
	TotalGainLossPct decimal.Decimal `json:"total_gain_loss_pct" db:"total_gain_loss_pct"`
	DayGainLoss     decimal.Decimal `json:"day_gain_loss" db:"day_gain_loss"`
	DayGainLossPct  decimal.Decimal `json:"day_gain_loss_pct" db:"day_gain_loss_pct"`
	SnapshotDate    time.Time       `json:"snapshot_date" db:"snapshot_date"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
}

// PerformanceMetrics contains calculated portfolio performance
type PerformanceMetrics struct {
	TotalReturn       decimal.Decimal `json:"total_return"`
	TotalReturnPct    decimal.Decimal `json:"total_return_pct"`
	DayReturn         decimal.Decimal `json:"day_return"`
	DayReturnPct      decimal.Decimal `json:"day_return_pct"`
	WeekReturn        decimal.Decimal `json:"week_return"`
	WeekReturnPct     decimal.Decimal `json:"week_return_pct"`
	MonthReturn       decimal.Decimal `json:"month_return"`
	MonthReturnPct    decimal.Decimal `json:"month_return_pct"`
	YearReturn        decimal.Decimal `json:"year_return"`
	YearReturnPct     decimal.Decimal `json:"year_return_pct"`
	CAGR              decimal.Decimal `json:"cagr"`
	SharpeRatio       decimal.Decimal `json:"sharpe_ratio"`
	BestDay           decimal.Decimal `json:"best_day"`
	WorstDay          decimal.Decimal `json:"worst_day"`
	WinningDays       int             `json:"winning_days"`
	LosingDays        int             `json:"losing_days"`
}

// RiskMetrics contains portfolio risk assessment
type RiskMetrics struct {
	Volatility       decimal.Decimal `json:"volatility"`        // Annualized std dev
	MaxDrawdown      decimal.Decimal `json:"max_drawdown"`      // Maximum peak-to-trough decline
	MaxDrawdownDate  *time.Time      `json:"max_drawdown_date"`
	Beta             decimal.Decimal `json:"beta"`              // vs S&P 500
	VaR95            decimal.Decimal `json:"var_95"`            // 95% Value at Risk
	RiskLevel        string          `json:"risk_level"`        // low, moderate, high
}

// DiversificationAnalysis contains portfolio diversification metrics
type DiversificationAnalysis struct {
	SectorAllocation   map[string]decimal.Decimal `json:"sector_allocation"`
	AssetTypeAllocation map[string]decimal.Decimal `json:"asset_type_allocation"` // stocks, etfs, etc
	TopHoldings        []HoldingWeight            `json:"top_holdings"`
	ConcentrationRisk  decimal.Decimal            `json:"concentration_risk"` // HHI index
	DiversificationScore int                      `json:"diversification_score"` // 0-100
	Recommendations    []string                   `json:"recommendations"`
}

// HoldingWeight represents a position's weight in portfolio
type HoldingWeight struct {
	Symbol      string          `json:"symbol"`
	Name        string          `json:"name"`
	Weight      decimal.Decimal `json:"weight"`
	Value       decimal.Decimal `json:"value"`
	GainLoss    decimal.Decimal `json:"gain_loss"`
	GainLossPct decimal.Decimal `json:"gain_loss_pct"`
}

// PortfolioHistory represents historical portfolio data for charting
type PortfolioHistory struct {
	Period     string                   `json:"period"` // 1D, 1W, 1M, 3M, 6M, 1Y, ALL
	DataPoints []PortfolioHistoryPoint  `json:"data_points"`
	StartValue decimal.Decimal          `json:"start_value"`
	EndValue   decimal.Decimal          `json:"end_value"`
	Change     decimal.Decimal          `json:"change"`
	ChangePct  decimal.Decimal          `json:"change_pct"`
}

// PortfolioHistoryPoint represents a single point in portfolio history
type PortfolioHistoryPoint struct {
	Date       time.Time       `json:"date"`
	Value      decimal.Decimal `json:"value"`
	DayChange  decimal.Decimal `json:"day_change"`
	DayChangePct decimal.Decimal `json:"day_change_pct"`
}

// PortfolioDashboard aggregates all analytics for a comprehensive view
type PortfolioDashboard struct {
	Summary        DashboardSummary         `json:"summary"`
	Performance    *PerformanceMetrics      `json:"performance"`
	Risk           *RiskMetrics             `json:"risk"`
	Diversification *DiversificationAnalysis `json:"diversification"`
	RecentHistory  []PortfolioHistoryPoint  `json:"recent_history"`
	GeneratedAt    time.Time                `json:"generated_at"`
}

// DashboardSummary provides quick portfolio overview
type DashboardSummary struct {
	TotalValue      decimal.Decimal `json:"total_value"`
	CashBalance     decimal.Decimal `json:"cash_balance"`
	InvestedValue   decimal.Decimal `json:"invested_value"`
	TotalGainLoss   decimal.Decimal `json:"total_gain_loss"`
	TotalGainLossPct decimal.Decimal `json:"total_gain_loss_pct"`
	DayGainLoss     decimal.Decimal `json:"day_gain_loss"`
	DayGainLossPct  decimal.Decimal `json:"day_gain_loss_pct"`
	PositionCount   int             `json:"position_count"`
	LastUpdated     time.Time       `json:"last_updated"`
}

// ScheduledInvestment represents a recurring investment
type ScheduledInvestment struct {
	ID              uuid.UUID       `json:"id" db:"id"`
	UserID          uuid.UUID       `json:"user_id" db:"user_id"`
	Name            *string         `json:"name" db:"name"`
	Symbol          *string         `json:"symbol" db:"symbol"`
	BasketID        *uuid.UUID      `json:"basket_id" db:"basket_id"`
	Amount          decimal.Decimal `json:"amount" db:"amount"`
	Frequency       string          `json:"frequency" db:"frequency"` // daily, weekly, biweekly, monthly
	DayOfWeek       *int            `json:"day_of_week" db:"day_of_week"`
	DayOfMonth      *int            `json:"day_of_month" db:"day_of_month"`
	NextExecutionAt time.Time       `json:"next_execution_at" db:"next_execution_at"`
	LastExecutedAt  *time.Time      `json:"last_executed_at" db:"last_executed_at"`
	Status          string          `json:"status" db:"status"`
	TotalInvested   decimal.Decimal `json:"total_invested" db:"total_invested"`
	ExecutionCount  int             `json:"execution_count" db:"execution_count"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at" db:"updated_at"`
}

// ScheduledInvestmentExecution logs each execution attempt
type ScheduledInvestmentExecution struct {
	ID                    uuid.UUID       `json:"id" db:"id"`
	ScheduledInvestmentID uuid.UUID       `json:"scheduled_investment_id" db:"scheduled_investment_id"`
	OrderID               *uuid.UUID      `json:"order_id" db:"order_id"`
	Amount                decimal.Decimal `json:"amount" db:"amount"`
	Status                string          `json:"status" db:"status"` // success, failed, skipped
	ErrorMessage          *string         `json:"error_message" db:"error_message"`
	ExecutedAt            time.Time       `json:"executed_at" db:"executed_at"`
}

// RebalancingConfig defines target portfolio allocation
type RebalancingConfig struct {
	ID                uuid.UUID                  `json:"id" db:"id"`
	UserID            uuid.UUID                  `json:"user_id" db:"user_id"`
	Name              string                     `json:"name" db:"name"`
	TargetAllocations map[string]decimal.Decimal `json:"target_allocations" db:"target_allocations"`
	ThresholdPct      decimal.Decimal            `json:"threshold_pct" db:"threshold_pct"`
	Frequency         *string                    `json:"frequency" db:"frequency"`
	LastRebalancedAt  *time.Time                 `json:"last_rebalanced_at" db:"last_rebalanced_at"`
	Status            string                     `json:"status" db:"status"`
	CreatedAt         time.Time                  `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time                  `json:"updated_at" db:"updated_at"`
}

// RebalancingPlan contains orders needed to rebalance
type RebalancingPlan struct {
	ConfigID         uuid.UUID                  `json:"config_id"`
	CurrentAllocations map[string]decimal.Decimal `json:"current_allocations"`
	TargetAllocations  map[string]decimal.Decimal `json:"target_allocations"`
	Trades           []RebalanceTradeOrder      `json:"trades"`
	TotalBuyAmount   decimal.Decimal            `json:"total_buy_amount"`
	TotalSellAmount  decimal.Decimal            `json:"total_sell_amount"`
	EstimatedCost    decimal.Decimal            `json:"estimated_cost"`
}

// RebalanceTradeOrder represents a single trade in rebalancing plan
type RebalanceTradeOrder struct {
	Symbol       string          `json:"symbol"`
	Side         string          `json:"side"` // buy or sell
	CurrentPct   decimal.Decimal `json:"current_pct"`
	TargetPct    decimal.Decimal `json:"target_pct"`
	DriftPct     decimal.Decimal `json:"drift_pct"`
	Amount       decimal.Decimal `json:"amount"`
	EstimatedQty decimal.Decimal `json:"estimated_qty"`
}

// MarketAlert represents a price/volume alert
type MarketAlert struct {
	ID               uuid.UUID        `json:"id" db:"id"`
	UserID           uuid.UUID        `json:"user_id" db:"user_id"`
	Symbol           string           `json:"symbol" db:"symbol"`
	AlertType        string           `json:"alert_type" db:"alert_type"` // price_above, price_below, pct_change
	ConditionValue   decimal.Decimal  `json:"condition_value" db:"condition_value"`
	CurrentPrice     *decimal.Decimal `json:"current_price" db:"current_price"`
	Triggered        bool             `json:"triggered" db:"triggered"`
	TriggeredAt      *time.Time       `json:"triggered_at" db:"triggered_at"`
	NotificationSent bool             `json:"notification_sent" db:"notification_sent"`
	Status           string           `json:"status" db:"status"`
	CreatedAt        time.Time        `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at" db:"updated_at"`
}

// MarketQuote represents real-time market data
type MarketQuote struct {
	Symbol        string          `json:"symbol"`
	Price         decimal.Decimal `json:"price"`
	Bid           decimal.Decimal `json:"bid"`
	Ask           decimal.Decimal `json:"ask"`
	Volume        int64           `json:"volume"`
	Change        decimal.Decimal `json:"change"`
	ChangePct     decimal.Decimal `json:"change_pct"`
	High          decimal.Decimal `json:"high"`
	Low           decimal.Decimal `json:"low"`
	Open          decimal.Decimal `json:"open"`
	PreviousClose decimal.Decimal `json:"previous_close"`
	Timestamp     time.Time       `json:"timestamp"`
}

// MarketBar represents OHLCV data
type MarketBar struct {
	Symbol    string          `json:"symbol"`
	Open      decimal.Decimal `json:"open"`
	High      decimal.Decimal `json:"high"`
	Low       decimal.Decimal `json:"low"`
	Close     decimal.Decimal `json:"close"`
	Volume    int64           `json:"volume"`
	Timestamp time.Time       `json:"timestamp"`
}

const (
	FrequencyDaily    = "daily"
	FrequencyWeekly   = "weekly"
	FrequencyBiweekly = "biweekly"
	FrequencyMonthly  = "monthly"

	AlertTypePriceAbove = "price_above"
	AlertTypePriceBelow = "price_below"
	AlertTypePctChange  = "pct_change"

	ScheduleStatusActive    = "active"
	ScheduleStatusPaused    = "paused"
	ScheduleStatusCancelled = "cancelled"
)
