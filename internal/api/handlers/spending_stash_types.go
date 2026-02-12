package handlers

import "time"

// SpendingStashResponse is the main response for the spending stash screen
type SpendingStashResponse struct {
	Balance               BalanceInfo              `json:"balance"`
	Allocation            SpendingAllocationInfo   `json:"allocation"`
	Card                  *CardSummary             `json:"card,omitempty"`
	SpendingSummary       *SpendingSummary         `json:"spending_summary,omitempty"`
	TopCategories         []CategorySummary        `json:"top_categories"`
	RoundUps              *RoundUpsSummary         `json:"round_ups,omitempty"`
	Limits                SpendingLimits           `json:"limits"`
	PendingAuthorizations []PendingAuthorization   `json:"pending_authorizations"`
	RecentTransactions    TransactionListResponse  `json:"recent_transactions"`
	Links                 SpendingLinks            `json:"_links"`
}

// BalanceInfo groups balance-related fields
type BalanceInfo struct {
	Available   string    `json:"available"`
	Pending     string    `json:"pending"`
	Currency    string    `json:"currency"`
	LastUpdated time.Time `json:"last_updated"`
}

// SpendingAllocationInfo provides allocation mode details for spending stash
type SpendingAllocationInfo struct {
	Active               bool    `json:"active"`
	SpendingRatio        float64 `json:"spending_ratio"`
	StashRatio           float64 `json:"stash_ratio"`
	TotalReceived        string  `json:"total_received"`
	LastAllocationAt     *string `json:"last_allocation_at,omitempty"`
	LastAllocationAmount *string `json:"last_allocation_amount,omitempty"`
}

// CardSummary represents card info for the spending stash
type CardSummary struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Network   string `json:"network"`
	Status    string `json:"status"`
	LastFour  string `json:"last_four"`
	IsFrozen  bool   `json:"is_frozen"`
	CreatedAt string `json:"created_at"`
}

// SpendingSummary contains pre-computed spending analytics
type SpendingSummary struct {
	ThisMonthTotal      string  `json:"this_month_total"`
	TransactionCount    int     `json:"transaction_count"`
	DailyAverage        string  `json:"daily_average"`
	Trend               string  `json:"trend"`
	TrendChangePercent  float64 `json:"trend_change_percent"`
}

// CategorySummary represents a spending category
type CategorySummary struct {
	Name    string  `json:"name"`
	Amount  string  `json:"amount"`
	Percent float64 `json:"percent"`
}

// RoundUpsSummary represents round-ups summary
type RoundUpsSummary struct {
	IsEnabled        bool    `json:"is_enabled"`
	Multiplier       int     `json:"multiplier"`
	TotalAccumulated string  `json:"total_accumulated"`
	TransactionCount int     `json:"transaction_count"`
	NextInvestmentAt *string `json:"next_investment_at,omitempty"`
}

// SpendingLimits represents spending limits
type SpendingLimits struct {
	Daily                       LimitDetail `json:"daily"`
	Monthly                     LimitDetail `json:"monthly"`
	PerTransaction              string      `json:"per_transaction"`
	DailyTransactionsRemaining  int         `json:"daily_transactions_remaining"`
}

// LimitDetail represents a limit with used/remaining
type LimitDetail struct {
	Limit     string `json:"limit"`
	Used      string `json:"used"`
	Remaining string `json:"remaining"`
}

// PendingAuthorization represents a card pre-authorization
type PendingAuthorization struct {
	ID           string `json:"id"`
	MerchantName string `json:"merchant_name"`
	Amount       string `json:"amount"`
	Currency     string `json:"currency"`
	AuthorizedAt string `json:"authorized_at"`
	ExpiresAt    string `json:"expires_at"`
	Category     string `json:"category,omitempty"`
}

// TransactionListResponse represents paginated transactions
type TransactionListResponse struct {
	Items      []TransactionSummary `json:"items"`
	HasMore    bool                 `json:"has_more"`
	NextCursor *string              `json:"next_cursor,omitempty"`
}

// TransactionSummary represents a transaction in the spending stash
type TransactionSummary struct {
	ID                string           `json:"id"`
	Type              string           `json:"type"`
	Amount            string           `json:"amount"`
	Currency          string           `json:"currency"`
	Description       string           `json:"description"`
	Merchant          *MerchantInfo    `json:"merchant,omitempty"`
	Status            string           `json:"status"`
	CreatedAt         string           `json:"created_at"`
	PendingSettlement bool             `json:"pending_settlement"`
	RefundStatus      *string          `json:"refund_status"`
}

// MerchantInfo contains rich merchant data
type MerchantInfo struct {
	Name         string  `json:"name"`
	LogoURL      *string `json:"logo_url,omitempty"`
	Category     string  `json:"category"`
	CategoryIcon string  `json:"category_icon"`
}

// SpendingLinks contains HATEOAS links for spending stash
type SpendingLinks struct {
	Self           string `json:"self"`
	Transactions   string `json:"transactions"`
	EditLimits     string `json:"edit_limits"`
	EditAllocation string `json:"edit_allocation"`
	FreezeCard     string `json:"freeze_card"`
}
