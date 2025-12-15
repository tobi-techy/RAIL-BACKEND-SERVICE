package entities

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// RoundupSourceType represents the source of a transaction
type RoundupSourceType string

const (
	RoundupSourceCard   RoundupSourceType = "card"
	RoundupSourceBank   RoundupSourceType = "bank"
	RoundupSourceManual RoundupSourceType = "manual"
)

// RoundupStatus represents the status of a round-up transaction
type RoundupStatus string

const (
	RoundupStatusPending   RoundupStatus = "pending"
	RoundupStatusCollected RoundupStatus = "collected"
	RoundupStatusInvested  RoundupStatus = "invested"
	RoundupStatusFailed    RoundupStatus = "failed"
)

// RoundupSettings represents user's round-up configuration
type RoundupSettings struct {
	UserID              uuid.UUID       `json:"user_id" db:"user_id"`
	Enabled             bool            `json:"enabled" db:"enabled"`
	Multiplier          decimal.Decimal `json:"multiplier" db:"multiplier"`           // 1x-10x
	Threshold           decimal.Decimal `json:"threshold" db:"threshold"`             // Min amount before auto-invest
	AutoInvestEnabled   bool            `json:"auto_invest_enabled" db:"auto_invest_enabled"`
	AutoInvestBasketID  *uuid.UUID      `json:"auto_invest_basket_id,omitempty" db:"auto_invest_basket_id"`
	AutoInvestSymbol    *string         `json:"auto_invest_symbol,omitempty" db:"auto_invest_symbol"`
	CreatedAt           time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at" db:"updated_at"`
}

// Validate validates round-up settings
func (s *RoundupSettings) Validate() error {
	if s.Multiplier.LessThan(decimal.NewFromInt(1)) || s.Multiplier.GreaterThan(decimal.NewFromInt(10)) {
		return fmt.Errorf("multiplier must be between 1 and 10")
	}
	if s.Threshold.LessThan(decimal.NewFromInt(1)) {
		return fmt.Errorf("threshold must be at least $1")
	}
	if s.AutoInvestEnabled && s.AutoInvestBasketID == nil && s.AutoInvestSymbol == nil {
		return fmt.Errorf("auto-invest requires a basket or symbol target")
	}
	return nil
}

// DefaultRoundupSettings returns default settings for a new user
func DefaultRoundupSettings(userID uuid.UUID) *RoundupSettings {
	now := time.Now()
	return &RoundupSettings{
		UserID:            userID,
		Enabled:           false,
		Multiplier:        decimal.NewFromInt(1),
		Threshold:         decimal.NewFromFloat(5.00),
		AutoInvestEnabled: false,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

// RoundupTransaction represents a single round-up from a transaction
type RoundupTransaction struct {
	ID                uuid.UUID         `json:"id" db:"id"`
	UserID            uuid.UUID         `json:"user_id" db:"user_id"`
	OriginalAmount    decimal.Decimal   `json:"original_amount" db:"original_amount"`
	RoundedAmount     decimal.Decimal   `json:"rounded_amount" db:"rounded_amount"`
	SpareChange       decimal.Decimal   `json:"spare_change" db:"spare_change"`
	MultipliedAmount  decimal.Decimal   `json:"multiplied_amount" db:"multiplied_amount"`
	SourceType        RoundupSourceType `json:"source_type" db:"source_type"`
	SourceRef         *string           `json:"source_ref,omitempty" db:"source_ref"`
	MerchantName      *string           `json:"merchant_name,omitempty" db:"merchant_name"`
	Status            RoundupStatus     `json:"status" db:"status"`
	CollectedAt       *time.Time        `json:"collected_at,omitempty" db:"collected_at"`
	InvestedAt        *time.Time        `json:"invested_at,omitempty" db:"invested_at"`
	InvestmentOrderID *uuid.UUID        `json:"investment_order_id,omitempty" db:"investment_order_id"`
	CreatedAt         time.Time         `json:"created_at" db:"created_at"`
}

// RoundupAccumulator tracks pending round-ups before threshold
type RoundupAccumulator struct {
	UserID           uuid.UUID       `json:"user_id" db:"user_id"`
	PendingAmount    decimal.Decimal `json:"pending_amount" db:"pending_amount"`
	TotalCollected   decimal.Decimal `json:"total_collected" db:"total_collected"`
	TotalInvested    decimal.Decimal `json:"total_invested" db:"total_invested"`
	LastCollectionAt *time.Time      `json:"last_collection_at,omitempty" db:"last_collection_at"`
	LastInvestmentAt *time.Time      `json:"last_investment_at,omitempty" db:"last_investment_at"`
	UpdatedAt        time.Time       `json:"updated_at" db:"updated_at"`
}

// RoundupSummary provides a summary view for the user
type RoundupSummary struct {
	Settings         *RoundupSettings `json:"settings"`
	PendingAmount    decimal.Decimal  `json:"pending_amount"`
	TotalCollected   decimal.Decimal  `json:"total_collected"`
	TotalInvested    decimal.Decimal  `json:"total_invested"`
	TransactionCount int              `json:"transaction_count"`
}

// CalculateRoundup calculates spare change from a transaction amount
func CalculateRoundup(amount, multiplier decimal.Decimal) (rounded, spareChange, multiplied decimal.Decimal) {
	// Round up to nearest dollar
	rounded = amount.Ceil()
	spareChange = rounded.Sub(amount)
	
	// If amount is exactly a dollar, round up to next dollar
	if spareChange.IsZero() {
		spareChange = decimal.NewFromInt(1)
		rounded = rounded.Add(decimal.NewFromInt(1))
	}
	
	multiplied = spareChange.Mul(multiplier)
	return rounded, spareChange, multiplied
}
