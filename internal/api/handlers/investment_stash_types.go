package handlers

// InvestmentStashResponse is the main response for the investment stash screen
type InvestmentStashResponse struct {
	TotalInvestmentBalance string `json:"total_investment_balance"`
	TotalCostBasis         string `json:"total_cost_basis"`
	TotalGain              string `json:"total_gain"`
	TotalGainPercent       string `json:"total_gain_percent"`

	Positions *PositionList `json:"positions"`

	AllocationInfo InvestmentAllocationInfo `json:"allocation_info"`

	Stats InvestmentStats `json:"stats"`
}

// PositionList represents paginated positions
type PositionList struct {
	Items      []PositionSummary `json:"items"`
	TotalCount int               `json:"total_count"`
	Page       int               `json:"page"`
	PageSize   int               `json:"page_size"`
}

// PositionSummary represents a single position in the portfolio
type PositionSummary struct {
	Symbol            string  `json:"symbol"`
	Name              string  `json:"name"`
	LogoURL           string  `json:"logo_url,omitempty"`
	Quantity          string  `json:"quantity"`
	CurrentPrice      string  `json:"current_price"`
	MarketValue       string  `json:"market_value"`
	CostBasis         string  `json:"cost_basis"`
	AvgCost           string  `json:"avg_cost"`
	UnrealizedGain    string  `json:"unrealized_gain"`
	UnrealizedGainPct string  `json:"unrealized_gain_pct"`
	DayChange         string  `json:"day_change"`
	DayChangePct      string  `json:"day_change_pct"`
	PortfolioWeight   float64 `json:"portfolio_weight"`
}

// InvestmentAllocationInfo provides allocation mode details for investment stash
type InvestmentAllocationInfo struct {
	Active           bool   `json:"active"`
	StashRatio       string `json:"stash_ratio"`
	TotalAllocated   string `json:"total_allocated,omitempty"`
	LastAllocatedAt  string `json:"last_allocated_at,omitempty"`
	LastAllocatedAmt string `json:"last_allocated_amount,omitempty"`
}

// InvestmentStats contains quick stats for the investment stash
type InvestmentStats struct {
	TotalDeposits     string `json:"total_deposits"`
	TotalWithdrawals  string `json:"total_withdrawals"`
	PositionCount     int    `json:"position_count"`
	FirstInvestmentAt string `json:"first_investment_at,omitempty"`
}
