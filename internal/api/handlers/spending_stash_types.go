package handlers

// SpendingStashResponse is the main response for the spending stash screen
type SpendingStashResponse struct {
	SpendingBalance  string `json:"spending_balance"`
	AvailableToSpend string `json:"available_to_spend"`
	PendingAmount    string `json:"pending_amount"`
	Currency         string `json:"currency"`

	AllocationInfo SpendingAllocationInfo `json:"allocation_info"`

	Card *CardSummary `json:"card,omitempty"`

	RecentTransactions []TransactionSummary `json:"recent_transactions"`

	SpendingSummary *SpendingSummary `json:"spending_summary"`

	TopCategories []CategorySummary `json:"top_categories"`

	RoundUps *RoundUpsSummary `json:"round_ups,omitempty"`

	Limits SpendingLimits `json:"limits"`

	Stats SpendingStats `json:"stats"`
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
	ID        string `json:"id"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	LastFour  string `json:"last_four"`
	IsFrozen  bool   `json:"is_frozen"`
	CreatedAt string `json:"created_at"`
}

// TransactionSummary represents a transaction in the spending stash
type TransactionSummary struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	Description string `json:"description"`
	Category    string `json:"category,omitempty"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

// SpendingSummary contains pre-computed spending analytics
type SpendingSummary struct {
	ThisMonthTotal   string `json:"this_month_total"`
	TransactionCount int    `json:"transaction_count"`
	DailyAverage     string `json:"daily_average"`
	Trend            string `json:"trend"`
}

// CategorySummary represents a spending category
type CategorySummary struct {
	Name    string  `json:"name"`
	Amount  string  `json:"amount"`
	Percent float64 `json:"percent"`
}

// RoundUpsSummary represents round-ups summary
type RoundUpsSummary struct {
	IsEnabled        bool   `json:"is_enabled"`
	Multiplier       int    `json:"multiplier"`
	TotalAccumulated string `json:"total_accumulated"`
	TransactionCount int    `json:"transaction_count"`
}

// SpendingLimits represents spending limits
type SpendingLimits struct {
	Daily          LimitDetail `json:"daily"`
	Monthly        LimitDetail `json:"monthly"`
	PerTransaction string      `json:"per_transaction"`
}

// LimitDetail represents a limit with used/remaining
type LimitDetail struct {
	Limit     string `json:"limit"`
	Used      string `json:"used"`
	Remaining string `json:"remaining"`
}

// SpendingStats contains quick stats for spending
type SpendingStats struct {
	TotalSpentAllTime    string `json:"total_spent_all_time"`
	TotalTransactions    int    `json:"total_transactions"`
	MostFrequentCategory string `json:"most_frequent_category,omitempty"`
}
