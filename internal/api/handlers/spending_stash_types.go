package handlers

import "time"

// SpendingStashResponse is the spending analytics overview response
type SpendingStashResponse struct {
	Balance         SpendingBalanceInfo `json:"balance"`
	SpendingSummary SpendingSummary     `json:"spending_summary"`
	TopCategories   []CategorySummary   `json:"top_categories"`
	MonthlyChart    []MonthlyChartBar   `json:"monthly_chart"`
	RoundUps        *RoundUpsSummary    `json:"round_ups,omitempty"`
}

// SpendingBalanceInfo contains current balance info
type SpendingBalanceInfo struct {
	Available   string    `json:"available"`
	Currency    string    `json:"currency"`
	LastUpdated time.Time `json:"last_updated"`
}

// SpendingSummary contains pre-computed spending analytics
type SpendingSummary struct {
	ThisMonthTotal     string  `json:"this_month_total"`
	LastMonthTotal     string  `json:"last_month_total"`
	DailyAverage       string  `json:"daily_average"`
	Trend              string  `json:"trend"`
	TrendChangePercent float64 `json:"trend_change_percent"`
	TransactionCount   int     `json:"transaction_count"`
}

// CategorySummary represents a spending category
type CategorySummary struct {
	Name    string  `json:"name"`
	Amount  string  `json:"amount"`
	Percent float64 `json:"percent"`
}

// MonthlyChartBar is one bar in the 6-month stacked bar chart
type MonthlyChartBar struct {
	Month       string  `json:"month"`        // e.g. "Jan"
	Card        float64 `json:"card"`         // card spend
	P2P         float64 `json:"p2p"`          // sent via P2P
	Withdrawals float64 `json:"withdrawals"`  // crypto + fiat withdrawals
	Total       float64 `json:"total"`
}

// RoundUpsSummary represents round-ups summary
type RoundUpsSummary struct {
	IsEnabled        bool   `json:"is_enabled"`
	TotalAccumulated string `json:"total_accumulated"`
	TransactionCount int    `json:"transaction_count"`
}
