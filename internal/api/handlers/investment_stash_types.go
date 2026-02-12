package handlers

import "time"

// InvestmentStashResponse is the main response for the investment stash screen
type InvestmentStashResponse struct {
	Balance    InvestmentBalanceInfo    `json:"balance"`
	Allocation InvestmentAllocationInfo `json:"allocation"`
	Performance PerformanceInfo         `json:"performance"`
	Positions  PositionListResponse     `json:"positions"`
	Stats      InvestmentStats          `json:"stats"`
	AutoInvest *AutoInvestInfo          `json:"auto_invest,omitempty"`
	Links      InvestmentLinks          `json:"_links"`
}

// InvestmentBalanceInfo groups balance-related fields
type InvestmentBalanceInfo struct {
	Total             string    `json:"total"`
	Stash             string    `json:"stash"`
	Invested          string    `json:"invested"`
	PendingAllocation string    `json:"pending_allocation"`
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
	TotalGain        string  `json:"total_gain"`
	TotalGainPercent float64 `json:"total_gain_percent"`
	DayChange        string  `json:"day_change"`
	DayChangePercent float64 `json:"day_change_percent"`
	WeekChange       string  `json:"week_change"`
	WeekChangePercent float64 `json:"week_change_percent"`
	MonthChange      string  `json:"month_change"`
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

// InvestmentLinks contains HATEOAS links for investment stash
type InvestmentLinks struct {
	Self           string `json:"self"`
	Positions      string `json:"positions"`
	Baskets        string `json:"baskets"`
	Performance    string `json:"performance"`
	Withdraw       string `json:"withdraw"`
	EditAllocation string `json:"edit_allocation"`
	EditAutoInvest string `json:"edit_auto_invest"`
}
