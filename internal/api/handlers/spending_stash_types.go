package handlers

// SpendingStashResponse is the main response for the spending stash screen
type SpendingStashResponse struct {
	// Balance Summary
	SpendingBalance  string `json:"spending_balance"`
	AvailableToSpend string `json:"available_to_spend"`
	PendingAmount    string `json:"pending_amount"`

	// Allocation Mode Info
	AllocationInfo SpendingAllocationInfo `json:"allocation_info"`

	// Card Info (if user has card)
	Card *CardSummary `json:"card,omitempty"`

	// Recent Transactions (last 10)
	RecentTransactions []TransactionSummary `json:"recent_transactions"`

	// Spending Analytics
	Analytics SpendingAnalytics `json:"analytics"`

	// Round-Ups Summary (if enabled)
	RoundUps *RoundUpsSummary `json:"round_ups,omitempty"`

	// Spending Limits
	Limits SpendingLimits `json:"limits"`

	// Quick Stats
	Stats SpendingStats `json:"stats"`

	// Warnings for partial failures
	Warnings []string `json:"warnings,omitempty"`
}

// SpendingAllocationInfo provides allocation mode details for spending stash
type SpendingAllocationInfo struct {
	Active          bool   `json:"active"`
	SpendingRatio   string `json:"spending_ratio"`
	TotalReceived   string `json:"total_received,omitempty"`
	LastReceivedAt  string `json:"last_received_at,omitempty"`
	LastReceivedAmt string `json:"last_received_amount,omitempty"`
}

// CardSummary represents card info for the spending stash
type CardSummary struct {
	ID              string `json:"id"`
	Type            string `json:"type"`
	Status          string `json:"status"`
	LastFour        string `json:"last_four"`
	ExpiryMonth     string `json:"expiry_month,omitempty"`
	ExpiryYear      string `json:"expiry_year,omitempty"`
	CardholderName  string `json:"cardholder_name,omitempty"`
	SpendingEnabled bool   `json:"spending_enabled"`
	ATMEnabled      bool   `json:"atm_enabled"`
	CreatedAt       string `json:"created_at"`
}

// TransactionSummary represents a transaction in the spending stash
type TransactionSummary struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Amount       string `json:"amount"`
	Currency     string `json:"currency"`
	Description  string `json:"description"`
	MerchantName string `json:"merchant_name,omitempty"`
	MerchantLogo string `json:"merchant_logo,omitempty"`
	Category     string `json:"category,omitempty"`
	CategoryIcon string `json:"category_icon,omitempty"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
}

// SpendingAnalytics contains spending analytics data
type SpendingAnalytics struct {
	TotalSpentThisMonth     string             `json:"total_spent_this_month"`
	TransactionsThisMonth   int                `json:"transactions_this_month"`
	AvgTransactionThisMonth string             `json:"avg_transaction_this_month"`
	TotalSpentLastMonth     string             `json:"total_spent_last_month"`
	TransactionsLastMonth   int                `json:"transactions_last_month"`
	SpendingChange          string             `json:"spending_change"`
	SpendingChangePct       string             `json:"spending_change_pct"`
	SpendingTrend           string             `json:"spending_trend"`
	TopCategories           []SpendingCategory `json:"top_categories"`
	DailyAverage            string             `json:"daily_average"`
}

// SpendingCategory represents a spending category
type SpendingCategory struct {
	Category         string `json:"category"`
	CategoryIcon     string `json:"category_icon"`
	Amount           string `json:"amount"`
	Percent          string `json:"percent"`
	TransactionCount int    `json:"transaction_count"`
	Trend            string `json:"trend"`
}

// RoundUpsSummary represents round-ups summary
type RoundUpsSummary struct {
	Enabled          bool   `json:"enabled"`
	Multiplier       int    `json:"multiplier"`
	TotalAccumulated string `json:"total_accumulated"`
	PendingAmount    string `json:"pending_amount"`
	InvestedAmount   string `json:"invested_amount"`
	ThisMonthTotal   string `json:"this_month_total"`
	LastRoundUpAt    string `json:"last_round_up_at,omitempty"`
	TransactionCount int    `json:"transaction_count"`
}

// SpendingLimits represents spending limits
type SpendingLimits struct {
	DailySpendLimit       string `json:"daily_spend_limit"`
	DailySpendUsed        string `json:"daily_spend_used"`
	DailySpendRemaining   string `json:"daily_spend_remaining"`
	DailyResetAt          string `json:"daily_reset_at"`
	MonthlySpendLimit     string `json:"monthly_spend_limit"`
	MonthlySpendUsed      string `json:"monthly_spend_used"`
	MonthlySpendRemaining string `json:"monthly_spend_remaining"`
	PerTransactionLimit   string `json:"per_transaction_limit"`
	DailyATMLimit         string `json:"daily_atm_limit,omitempty"`
	DailyATMUsed          string `json:"daily_atm_used,omitempty"`
	DailyATMRemaining     string `json:"daily_atm_remaining,omitempty"`
}

// SpendingStats contains quick stats for spending
type SpendingStats struct {
	TotalSpentAllTime    string `json:"total_spent_all_time"`
	TotalTransactions    int    `json:"total_transactions"`
	TotalRoundUps        string `json:"total_round_ups"`
	CardCreatedAt        string `json:"card_created_at,omitempty"`
	FirstTransactionAt   string `json:"first_transaction_at,omitempty"`
	MostFrequentCategory string `json:"most_frequent_category,omitempty"`
}

// categoryIcons maps categories to icons (unexported for thread safety)
var categoryIcons = map[string]string{
	"Food & Dining":    "utensils",
	"Shopping":         "shopping-bag",
	"Transportation":   "car",
	"Entertainment":    "film",
	"Bills & Utilities": "file-text",
	"Health & Fitness": "heart",
	"Travel":           "plane",
	"Education":        "book",
	"Personal Care":    "user",
	"Other":            "more-horizontal",
}

// GetCategoryIcon returns the icon for a category, defaulting to "Other" icon if not found
func GetCategoryIcon(category string) string {
	if icon, ok := categoryIcons[category]; ok {
		return icon
	}
	return categoryIcons["Other"]
}
