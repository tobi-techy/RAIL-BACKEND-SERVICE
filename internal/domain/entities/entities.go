package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// === Legacy entities (kept for backward compatibility) ===

// Token represents a cryptocurrency token (legacy structure)
type Token struct {
	ID           uuid.UUID       `json:"id" db:"id"`
	Symbol       string          `json:"symbol" db:"symbol"`
	Name         string          `json:"name" db:"name"`
	Address      string          `json:"address" db:"address"`
	ChainID      int             `json:"chain_id" db:"chain_id"`
	Decimals     int             `json:"decimals" db:"decimals"`
	LogoURL      string          `json:"logo_url" db:"logo_url"`
	IsStablecoin bool            `json:"is_stablecoin" db:"is_stablecoin"`
	IsActive     bool            `json:"is_active" db:"is_active"`
	CurrentPrice decimal.Decimal `json:"current_price" db:"current_price"`
	MarketCap    decimal.Decimal `json:"market_cap" db:"market_cap"`
	Volume24h    decimal.Decimal `json:"volume_24h" db:"volume_24h"`
	CreatedAt    time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at" db:"updated_at"`
}

// LegacyBalance represents token balance in a wallet (legacy structure)
type LegacyBalance struct {
	ID           uuid.UUID       `json:"id" db:"id"`
	WalletID     uuid.UUID       `json:"wallet_id" db:"wallet_id"`
	TokenID      uuid.UUID       `json:"token_id" db:"token_id"`
	Amount       decimal.Decimal `json:"amount" db:"amount"`
	LockedAmount decimal.Decimal `json:"locked_amount" db:"locked_amount"`
	UpdatedAt    time.Time       `json:"updated_at" db:"updated_at"`
}

// Transaction represents a blockchain transaction
type Transaction struct {
	ID              uuid.UUID       `json:"id" db:"id"`
	UserID          uuid.UUID       `json:"user_id" db:"user_id"`
	WalletID        uuid.UUID       `json:"wallet_id" db:"wallet_id"`
	FromAddress     string          `json:"from_address" db:"from_address"`
	ToAddress       string          `json:"to_address" db:"to_address"`
	TokenID         uuid.UUID       `json:"token_id" db:"token_id"`
	Amount          decimal.Decimal `json:"amount" db:"amount"`
	TransactionHash string          `json:"transaction_hash" db:"transaction_hash"`
	BlockNumber     int64           `json:"block_number" db:"block_number"`
	ChainID         int             `json:"chain_id" db:"chain_id"`
	GasUsed         int64           `json:"gas_used" db:"gas_used"`
	GasPrice        decimal.Decimal `json:"gas_price" db:"gas_price"`
	Status          string          `json:"status" db:"status"` // pending, confirmed, failed
	Type            string          `json:"type" db:"type"`     // deposit, withdrawal, swap, transfer
	Description     string          `json:"description" db:"description"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
	ConfirmedAt     *time.Time      `json:"confirmed_at" db:"confirmed_at"`
}

// LegacyBasket represents an investment basket/portfolio (legacy structure)
type LegacyBasket struct {
	ID               uuid.UUID       `json:"id" db:"id"`
	UserID           uuid.UUID       `json:"user_id" db:"user_id"`
	Name             string          `json:"name" db:"name"`
	Description      string          `json:"description" db:"description"`
	IsPublic         bool            `json:"is_public" db:"is_public"`
	IsCurated        bool            `json:"is_curated" db:"is_curated"`
	Category         string          `json:"category" db:"category"` // defi, nft, gaming, etc.
	MinInvestment    decimal.Decimal `json:"min_investment" db:"min_investment"`
	TotalValue       decimal.Decimal `json:"total_value" db:"total_value"`
	PerformanceScore decimal.Decimal `json:"performance_score" db:"performance_score"`
	RiskLevel        int             `json:"risk_level" db:"risk_level"`         // 1-10
	RebalanceFreq    string          `json:"rebalance_freq" db:"rebalance_freq"` // daily, weekly, monthly
	IsActive         bool            `json:"is_active" db:"is_active"`
	CreatedAt        time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at" db:"updated_at"`
}

// BasketAllocation represents the allocation of tokens within a basket
type BasketAllocation struct {
	ID         uuid.UUID       `json:"id" db:"id"`
	BasketID   uuid.UUID       `json:"basket_id" db:"basket_id"`
	TokenID    uuid.UUID       `json:"token_id" db:"token_id"`
	Percentage decimal.Decimal `json:"percentage" db:"percentage"` // 0-100
	IsActive   bool            `json:"is_active" db:"is_active"`
	CreatedAt  time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at" db:"updated_at"`
}

// Investment represents a user's investment in a basket
type Investment struct {
	ID           uuid.UUID       `json:"id" db:"id"`
	UserID       uuid.UUID       `json:"user_id" db:"user_id"`
	BasketID     uuid.UUID       `json:"basket_id" db:"basket_id"`
	Amount       decimal.Decimal `json:"amount" db:"amount"`
	CurrentValue decimal.Decimal `json:"current_value" db:"current_value"`
	ProfitLoss   decimal.Decimal `json:"profit_loss" db:"profit_loss"`
	IsActive     bool            `json:"is_active" db:"is_active"`
	CreatedAt    time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at" db:"updated_at"`
}

// CopyTrade represents copy trading functionality
type CopyTrade struct {
	ID         uuid.UUID       `json:"id" db:"id"`
	FollowerID uuid.UUID       `json:"follower_id" db:"follower_id"`
	TraderID   uuid.UUID       `json:"trader_id" db:"trader_id"`
	Amount     decimal.Decimal `json:"amount" db:"amount"` // Amount allocated to copy trading
	IsActive   bool            `json:"is_active" db:"is_active"`
	CopyRatio  decimal.Decimal `json:"copy_ratio" db:"copy_ratio"` // Percentage to copy (0-1)
	CreatedAt  time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at" db:"updated_at"`
}

// TraderStats represents statistics for a trader in copy trading
type TraderStats struct {
	ID             uuid.UUID       `json:"id" db:"id"`
	UserID         uuid.UUID       `json:"user_id" db:"user_id"`
	TotalReturn    decimal.Decimal `json:"total_return" db:"total_return"`
	WinRate        decimal.Decimal `json:"win_rate" db:"win_rate"`
	FollowersCount int             `json:"followers_count" db:"followers_count"`
	TotalPortfolio decimal.Decimal `json:"total_portfolio" db:"total_portfolio"`
	MaxDrawdown    decimal.Decimal `json:"max_drawdown" db:"max_drawdown"`
	SharpeRatio    decimal.Decimal `json:"sharpe_ratio" db:"sharpe_ratio"`
	IsPublic       bool            `json:"is_public" db:"is_public"`
	LastTradeAt    *time.Time      `json:"last_trade_at" db:"last_trade_at"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at" db:"updated_at"`
}

// Card represents a debit card
type Card struct {
	ID             uuid.UUID       `json:"id" db:"id"`
	UserID         uuid.UUID       `json:"user_id" db:"user_id"`
	CardNumber     string          `json:"card_number" db:"card_number_encrypted"` // Last 4 digits shown
	CardType       string          `json:"card_type" db:"card_type"`               // visa, mastercard
	ExpiryMonth    int             `json:"expiry_month" db:"expiry_month"`
	ExpiryYear     int             `json:"expiry_year" db:"expiry_year"`
	CVV            string          `json:"-" db:"cvv_encrypted"`
	CardholderName string          `json:"cardholder_name" db:"cardholder_name"`
	Status         string          `json:"status" db:"status"` // active, frozen, cancelled
	SpendingLimit  decimal.Decimal `json:"spending_limit" db:"spending_limit"`
	DailyLimit     decimal.Decimal `json:"daily_limit" db:"daily_limit"`
	MonthlyLimit   decimal.Decimal `json:"monthly_limit" db:"monthly_limit"`
	IsVirtual      bool            `json:"is_virtual" db:"is_virtual"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at" db:"updated_at"`
}

// CardTransaction represents a card transaction
type CardTransaction struct {
	ID                uuid.UUID       `json:"id" db:"id"`
	CardID            uuid.UUID       `json:"card_id" db:"card_id"`
	UserID            uuid.UUID       `json:"user_id" db:"user_id"`
	Amount            decimal.Decimal `json:"amount" db:"amount"`
	Currency          string          `json:"currency" db:"currency"`
	Description       string          `json:"description" db:"description"`
	MerchantName      string          `json:"merchant_name" db:"merchant_name"`
	MerchantCode      string          `json:"merchant_code" db:"merchant_code"`
	Status            string          `json:"status" db:"status"`                     // pending, completed, declined
	TransactionType   string          `json:"transaction_type" db:"transaction_type"` // purchase, refund, reversal
	AuthorizationCode string          `json:"authorization_code" db:"authorization_code"`
	CreatedAt         time.Time       `json:"created_at" db:"created_at"`
}

// Note: Notification type moved to notification.go with proper types and constants

// LegacyAuditLog represents audit trails for security and compliance
type LegacyAuditLog struct {
	ID           uuid.UUID `json:"id" db:"id"`
	UserID       uuid.UUID `json:"user_id" db:"user_id"`
	Action       string    `json:"action" db:"action"`
	ResourceType string    `json:"resource_type" db:"resource_type"`
	ResourceID   string    `json:"resource_id" db:"resource_id"`
	IPAddress    string    `json:"ip_address" db:"ip_address"`
	UserAgent    string    `json:"user_agent" db:"user_agent"`
	Changes      string    `json:"changes" db:"changes"` // JSON string of changes
	Status       string    `json:"status" db:"status"`   // success, failed
	ErrorMessage string    `json:"error_message" db:"error_message"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}

// Session represents user sessions
type Session struct {
	ID           uuid.UUID  `json:"id" db:"id"`
	UserID       uuid.UUID  `json:"user_id" db:"user_id"`
	Token        string     `json:"token" db:"token_hash"`
	RefreshToken string     `json:"refresh_token" db:"refresh_token_hash"`
	IPAddress    string     `json:"ip_address" db:"ip_address"`
	UserAgent    string     `json:"user_agent" db:"user_agent"`
	IsActive     bool       `json:"is_active" db:"is_active"`
	ExpiresAt    time.Time  `json:"expires_at" db:"expires_at"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	LastUsedAt   *time.Time `json:"last_used_at" db:"last_used_at"`
}
