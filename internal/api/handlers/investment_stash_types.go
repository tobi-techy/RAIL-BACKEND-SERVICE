package handlers

// InvestmentStashResponse is the main response for the investment stash screen
type InvestmentStashResponse struct {
	// Balance Summary
	TotalInvestmentBalance string `json:"total_investment_balance"`
	TotalCostBasis         string `json:"total_cost_basis"`
	TotalGain              string `json:"total_gain"`
	TotalGainPercent       string `json:"total_gain_percent"`

	// Performance Metrics
	Performance PerformanceMetrics `json:"performance"`

	// Portfolio Breakdown
	Positions []PositionSummary `json:"positions"`

	// Basket Investments
	Baskets []BasketInvestmentSummary `json:"baskets"`

	// Conductor Application Status
	ConductorStatus *ConductorApplicationStatus `json:"conductor_status,omitempty"`

	// Copy Trading - Followed Conductors
	Following []FollowedConductorSummary `json:"following"`

	// Allocation Mode Info
	AllocationInfo InvestmentAllocationInfo `json:"allocation_info"`

	// Quick Stats
	Stats InvestmentStats `json:"stats"`

	// Warnings for partial failures
	Warnings []string `json:"warnings,omitempty"`
}

// PerformanceMetrics contains percentage changes over various time periods
type PerformanceMetrics struct {
	Day        string `json:"day"`
	DayPct     string `json:"day_pct"`
	Week       string `json:"week"`
	WeekPct    string `json:"week_pct"`
	Month      string `json:"month"`
	MonthPct   string `json:"month_pct"`
	YTD        string `json:"ytd"`
	YTDPct     string `json:"ytd_pct"`
	AllTime    string `json:"all_time"`
	AllTimePct string `json:"all_time_pct"`
}

// PositionSummary represents a single position in the portfolio
type PositionSummary struct {
	Symbol            string `json:"symbol"`
	Name              string `json:"name"`
	LogoURL           string `json:"logo_url,omitempty"`
	Quantity          string `json:"quantity"`
	CurrentPrice      string `json:"current_price"`
	MarketValue       string `json:"market_value"`
	CostBasis         string `json:"cost_basis"`
	AvgCost           string `json:"avg_cost"`
	UnrealizedGain    string `json:"unrealized_gain"`
	UnrealizedGainPct string `json:"unrealized_gain_pct"`
	DayChange         string `json:"day_change"`
	DayChangePct      string `json:"day_change_pct"`
	PortfolioWeight   string `json:"portfolio_weight"`
}

// BasketInvestmentSummary represents a basket investment
type BasketInvestmentSummary struct {
	BasketID     string `json:"basket_id"`
	BasketName   string `json:"basket_name"`
	Description  string `json:"description"`
	RiskLevel    string `json:"risk_level"`
	Invested     string `json:"invested"`
	CurrentValue string `json:"current_value"`
	Gain         string `json:"gain"`
	GainPercent  string `json:"gain_percent"`
	AssetCount   int    `json:"asset_count"`
}

// ConductorApplicationStatus represents the user's conductor application status
type ConductorApplicationStatus struct {
	Status          string `json:"status"` // none, pending, approved, rejected
	CanApply        bool   `json:"can_apply"`
	AppliedAt       string `json:"applied_at,omitempty"`
	ApprovedAt      string `json:"approved_at,omitempty"`
	RejectedAt      string `json:"rejected_at,omitempty"`
	RejectionReason string `json:"rejection_reason,omitempty"`
	ConductorID     string `json:"conductor_id,omitempty"`
	TrackCount      int    `json:"track_count,omitempty"`
	FollowerCount   int    `json:"follower_count,omitempty"`
}

// FollowedConductorSummary represents a conductor the user is following
type FollowedConductorSummary struct {
	FollowID        string `json:"follow_id"`
	ConductorID     string `json:"conductor_id"`
	ConductorName   string `json:"conductor_name"`
	ConductorAvatar string `json:"conductor_avatar,omitempty"`
	TrackID         string `json:"track_id"`
	TrackName       string `json:"track_name"`
	Allocated       string `json:"allocated"`
	CurrentValue    string `json:"current_value"`
	Gain            string `json:"gain"`
	GainPercent     string `json:"gain_percent"`
	FollowedAt      string `json:"followed_at"`
}

// InvestmentStats contains quick stats for the investment stash
type InvestmentStats struct {
	TotalDeposits     string `json:"total_deposits"`
	TotalWithdrawals  string `json:"total_withdrawals"`
	PositionCount     int    `json:"position_count"`
	BasketCount       int    `json:"basket_count"`
	FollowingCount    int    `json:"following_count"`
	FirstInvestmentAt string `json:"first_investment_at,omitempty"`
}

// InvestmentAllocationInfo provides allocation mode details for investment stash
type InvestmentAllocationInfo struct {
	Active            bool   `json:"active"`
	StashRatio        string `json:"stash_ratio"`
	TotalAllocated    string `json:"total_allocated,omitempty"`
	LastAllocatedAt   string `json:"last_allocated_at,omitempty"`
	LastAllocatedAmt  string `json:"last_allocated_amount,omitempty"`
}
