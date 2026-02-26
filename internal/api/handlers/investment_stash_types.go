package handlers

import "time"

// InvestmentStashResponse is the main response for the investment stash screen
type InvestmentStashResponse struct {
	Balance                   InvestmentBalanceInfo          `json:"balance"`
	Performance               PerformanceInfo                `json:"performance"`
	Positions                 PositionListResponse           `json:"positions"`
	Stats                     InvestmentStats                `json:"stats"`
	AutoInvest                *AutoInvestInfo                `json:"auto_invest,omitempty"`
	Summary                   *InvestmentSummary             `json:"summary,omitempty"`
	HoldingsPreview           []InvestmentPositionDetail     `json:"holdings_preview"`
	TopPerformersPreview      []InvestmentPositionDetail     `json:"top_performers_preview"`
	DistributionPreview       []InvestmentDistributionItem   `json:"distribution_preview"`
	RecentTransactionsPreview []InvestmentTradeTransaction   `json:"recent_transactions_preview"`
	PerformancePreview        *InvestmentPerformanceResponse `json:"performance_preview,omitempty"`
	DataHealth                InvestmentDataHealth           `json:"data_health"`
	Links                     InvestmentLinks                `json:"_links"`
}

// MoneyValue provides machine-safe and UX-ready amount representations
type MoneyValue struct {
	Raw       string `json:"raw"`
	Formatted string `json:"formatted"`
}

// InvestmentBalanceInfo groups balance-related fields
type InvestmentBalanceInfo struct {
	Total             string    `json:"total"`
	Stash             string    `json:"stash"`
	Invested          string    `json:"invested"`
	PendingAllocation string    `json:"pending_allocation"`
	LeftToInvest      string    `json:"left_to_invest"`
	NetPnL            string    `json:"net_pnl"`
	NetPnLPercent     float64   `json:"net_pnl_percent"`
	Currency          string    `json:"currency"`
	LastUpdated       time.Time `json:"last_updated"`
}

// InvestmentAllocationInfo provides allocation mode details for investment stash
type InvestmentAllocationInfo struct {
	Active               bool    `json:"active"`
	SpendingRatio        float64 `json:"spending_ratio"`
	StashRatio           float64 `json:"stash_ratio"`
	TotalAllocated       string  `json:"total_allocated"`
	LastAllocationAt     *string `json:"last_allocation_at,omitempty"`
	LastAllocationAmount *string `json:"last_allocation_amount,omitempty"`
	NextAllocationAt     *string `json:"next_allocation_at,omitempty"`
}

// PerformanceInfo contains portfolio performance metrics
type PerformanceInfo struct {
	TotalGain          string  `json:"total_gain"`
	TotalGainPercent   float64 `json:"total_gain_percent"`
	DayChange          string  `json:"day_change"`
	DayChangePercent   float64 `json:"day_change_percent"`
	WeekChange         string  `json:"week_change"`
	WeekChangePercent  float64 `json:"week_change_percent"`
	MonthChange        string  `json:"month_change"`
	MonthChangePercent float64 `json:"month_change_percent"`
}

// PositionListResponse represents paginated positions
type PositionListResponse struct {
	Page       int               `json:"page"`
	PageSize   int               `json:"page_size"`
	TotalCount int               `json:"total_count"`
	HasMore    bool              `json:"has_more"`
	Items      []PositionSummary `json:"items"`
}

// PositionSummary represents a single position in the portfolio
type PositionSummary struct {
	ID                string  `json:"id"`
	Symbol            string  `json:"symbol"`
	Name              string  `json:"name"`
	Type              string  `json:"type"`
	Quantity          string  `json:"quantity"`
	CurrentPrice      string  `json:"current_price"`
	MarketValue       string  `json:"market_value"`
	CostBasis         string  `json:"cost_basis"`
	AvgCost           string  `json:"avg_cost"`
	UnrealizedGain    string  `json:"unrealized_gain"`
	UnrealizedGainPct float64 `json:"unrealized_gain_percent"`
	DayChange         string  `json:"day_change"`
	DayChangePct      float64 `json:"day_change_percent"`
	PortfolioWeight   float64 `json:"portfolio_weight"`
	LogoURL           *string `json:"logo_url,omitempty"`
}

// InvestmentStats contains quick stats for the investment stash
type InvestmentStats struct {
	TotalDeposits     string  `json:"total_deposits"`
	TotalWithdrawals  string  `json:"total_withdrawals"`
	PositionCount     int     `json:"position_count"`
	FirstInvestmentAt *string `json:"first_investment_at,omitempty"`
}

// AutoInvestInfo contains auto-invest configuration
type AutoInvestInfo struct {
	IsEnabled        bool    `json:"is_enabled"`
	TriggerThreshold string  `json:"trigger_threshold"`
	LastTriggeredAt  *string `json:"last_triggered_at,omitempty"`
	Strategy         string  `json:"strategy"`
}

// InvestmentSummary provides top-level investment metrics for dashboard header cards
type InvestmentSummary struct {
	TotalBalance      MoneyValue `json:"total_balance"`
	InvestedValue     MoneyValue `json:"invested_value"`
	BuyingPower       MoneyValue `json:"buying_power"`
	DayChange         MoneyValue `json:"day_change"`
	DayChangePercent  float64    `json:"day_change_percent"`
	WeekChange        MoneyValue `json:"week_change"`
	WeekChangePercent float64    `json:"week_change_percent"`
	Currency          string     `json:"currency"`
	LastUpdated       string     `json:"last_updated"`
}

// InvestmentDataHealth indicates section-level data freshness/availability
type InvestmentDataHealth struct {
	Positions    string `json:"positions"`
	Distribution string `json:"distribution"`
	Transactions string `json:"transactions"`
	Performance  string `json:"performance"`
}

// InvestmentPositionDetail represents a detailed holding row
type InvestmentPositionDetail struct {
	ID                   string     `json:"id"`
	Symbol               string     `json:"symbol"`
	Name                 string     `json:"name"`
	Quantity             string     `json:"quantity"`
	AvgEntryPrice        MoneyValue `json:"avg_entry_price"`
	CurrentPrice         MoneyValue `json:"current_price"`
	MarketValue          MoneyValue `json:"market_value"`
	CostBasis            MoneyValue `json:"cost_basis"`
	UnrealizedPnL        MoneyValue `json:"unrealized_pnl"`
	UnrealizedPnLPercent float64    `json:"unrealized_pnl_percent"`
	PortfolioWeight      float64    `json:"portfolio_weight"`
	LogoURL              *string    `json:"logo_url,omitempty"`
}

// InvestmentPositionsResponse represents paginated positions detail response
type InvestmentPositionsResponse struct {
	Page       int                        `json:"page"`
	PageSize   int                        `json:"page_size"`
	TotalCount int                        `json:"total_count"`
	HasMore    bool                       `json:"has_more"`
	Items      []InvestmentPositionDetail `json:"items"`
}

// InvestmentDistributionItem represents portfolio allocation by symbol
type InvestmentDistributionItem struct {
	Symbol        string     `json:"symbol"`
	Name          string     `json:"name"`
	WeightPercent float64    `json:"weight_percent"`
	Value         MoneyValue `json:"value"`
}

// InvestmentDistributionResponse represents allocation/distribution details
type InvestmentDistributionResponse struct {
	Items             []InvestmentDistributionItem `json:"items"`
	Top1WeightPercent float64                      `json:"top_1_weight_percent"`
	Top3WeightPercent float64                      `json:"top_3_weight_percent"`
	HHI               float64                      `json:"hhi"`
	GeneratedAt       string                       `json:"generated_at"`
}

// InvestmentTradeTransaction represents a trade-focused investment transaction row
type InvestmentTradeTransaction struct {
	ID         string      `json:"id"`
	Type       string      `json:"type"`
	Side       string      `json:"side"`
	Status     string      `json:"status"`
	Symbol     string      `json:"symbol"`
	Quantity   string      `json:"quantity"`
	Price      *MoneyValue `json:"price,omitempty"`
	Amount     MoneyValue  `json:"amount"`
	OccurredAt string      `json:"occurred_at"`
}

// InvestmentTransactionsResponse represents paginated trade transactions response
type InvestmentTransactionsResponse struct {
	Items      []InvestmentTradeTransaction `json:"items"`
	Limit      int                          `json:"limit"`
	Offset     int                          `json:"offset"`
	HasMore    bool                         `json:"has_more"`
	NextOffset *int                         `json:"next_offset,omitempty"`
}

// InvestmentPerformancePoint represents a chart data point
type InvestmentPerformancePoint struct {
	Date  string     `json:"date"`
	Value MoneyValue `json:"value"`
}

// InvestmentPerformanceResponse represents portfolio performance timeseries
type InvestmentPerformanceResponse struct {
	Period        string                       `json:"period"`
	Return        MoneyValue                   `json:"return"`
	ReturnPercent float64                      `json:"return_percent"`
	Points        []InvestmentPerformancePoint `json:"points"`
	GeneratedAt   string                       `json:"generated_at"`
}

// InvestmentLinks contains HATEOAS links for investment stash
type InvestmentLinks struct {
	Self           string `json:"self"`
	Positions      string `json:"positions"`
	Distribution   string `json:"distribution"`
	Transactions   string `json:"transactions"`
	Baskets        string `json:"baskets"`
	Performance    string `json:"performance"`
	Withdraw       string `json:"withdraw"`
	EditAllocation string `json:"edit_allocation"`
	EditAutoInvest string `json:"edit_auto_invest"`
}
