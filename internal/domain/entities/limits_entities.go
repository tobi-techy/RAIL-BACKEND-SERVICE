package entities

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// KYCTier represents user verification level for transaction limits
type KYCTier string

const (
	KYCTierUnverified KYCTier = "unverified" // No KYC completed
	KYCTierBasic      KYCTier = "basic"      // Basic identity verification (Tier 1)
	KYCTierAdvanced   KYCTier = "advanced"   // Advanced verification with proof of address/funds (Tier 2)
)

// Deposit Limits (USD)
var (
	// Minimum deposit - extremely low to enable micro-investing and round-ups
	MinDepositAmount = decimal.NewFromFloat(1.00)

	// Tier 1 (Basic KYC) limits
	Tier1DailyDepositLimit   = decimal.NewFromFloat(5000.00)
	Tier1MonthlyDepositLimit = decimal.NewFromFloat(25000.00)

	// Tier 2 (Advanced KYC) limits
	Tier2DailyDepositLimit   = decimal.NewFromFloat(50000.00)
	Tier2MonthlyDepositLimit = decimal.NewFromFloat(250000.00)

	// Unverified limits (very restrictive)
	UnverifiedDailyDepositLimit   = decimal.NewFromFloat(100.00)
	UnverifiedMonthlyDepositLimit = decimal.NewFromFloat(500.00)
)

// Withdrawal Limits (USD)
var (
	// Minimum withdrawal - covers network fees while allowing small exits
	MinWithdrawalAmount = decimal.NewFromFloat(10.00)

	// Tier 1 (Basic KYC) limits - slightly lower than deposit to encourage retention
	Tier1DailyWithdrawalLimit   = decimal.NewFromFloat(2500.00)
	Tier1MonthlyWithdrawalLimit = decimal.NewFromFloat(25000.00)

	// Tier 2 (Advanced KYC) limits
	Tier2DailyWithdrawalLimit   = decimal.NewFromFloat(10000.00)
	Tier2MonthlyWithdrawalLimit = decimal.NewFromFloat(150000.00)

	// Unverified limits
	UnverifiedDailyWithdrawalLimit   = decimal.NewFromFloat(50.00)
	UnverifiedMonthlyWithdrawalLimit = decimal.NewFromFloat(200.00)
)

// TransactionLimitConfig holds limits for a specific KYC tier
type TransactionLimitConfig struct {
	Tier                   KYCTier
	MinDeposit             decimal.Decimal
	DailyDepositLimit      decimal.Decimal
	MonthlyDepositLimit    decimal.Decimal
	MinWithdrawal          decimal.Decimal
	DailyWithdrawalLimit   decimal.Decimal
	MonthlyWithdrawalLimit decimal.Decimal
}

// GetLimitConfigForTier returns the limit configuration for a KYC tier
func GetLimitConfigForTier(tier KYCTier) TransactionLimitConfig {
	switch tier {
	case KYCTierAdvanced:
		return TransactionLimitConfig{
			Tier:                   KYCTierAdvanced,
			MinDeposit:             MinDepositAmount,
			DailyDepositLimit:      Tier2DailyDepositLimit,
			MonthlyDepositLimit:    Tier2MonthlyDepositLimit,
			MinWithdrawal:          MinWithdrawalAmount,
			DailyWithdrawalLimit:   Tier2DailyWithdrawalLimit,
			MonthlyWithdrawalLimit: Tier2MonthlyWithdrawalLimit,
		}
	case KYCTierBasic:
		return TransactionLimitConfig{
			Tier:                   KYCTierBasic,
			MinDeposit:             MinDepositAmount,
			DailyDepositLimit:      Tier1DailyDepositLimit,
			MonthlyDepositLimit:    Tier1MonthlyDepositLimit,
			MinWithdrawal:          MinWithdrawalAmount,
			DailyWithdrawalLimit:   Tier1DailyWithdrawalLimit,
			MonthlyWithdrawalLimit: Tier1MonthlyWithdrawalLimit,
		}
	default: // Unverified
		return TransactionLimitConfig{
			Tier:                   KYCTierUnverified,
			MinDeposit:             MinDepositAmount,
			DailyDepositLimit:      UnverifiedDailyDepositLimit,
			MonthlyDepositLimit:    UnverifiedMonthlyDepositLimit,
			MinWithdrawal:          MinWithdrawalAmount,
			DailyWithdrawalLimit:   UnverifiedDailyWithdrawalLimit,
			MonthlyWithdrawalLimit: UnverifiedMonthlyWithdrawalLimit,
		}
	}
}

// UserTransactionUsage tracks a user's transaction usage within limit periods
type UserTransactionUsage struct {
	ID                      uuid.UUID       `json:"id" db:"id"`
	UserID                  uuid.UUID       `json:"user_id" db:"user_id"`
	DailyDepositUsed        decimal.Decimal `json:"daily_deposit_used" db:"daily_deposit_used"`
	DailyDepositResetAt     time.Time       `json:"daily_deposit_reset_at" db:"daily_deposit_reset_at"`
	MonthlyDepositUsed      decimal.Decimal `json:"monthly_deposit_used" db:"monthly_deposit_used"`
	MonthlyDepositResetAt   time.Time       `json:"monthly_deposit_reset_at" db:"monthly_deposit_reset_at"`
	DailyWithdrawalUsed     decimal.Decimal `json:"daily_withdrawal_used" db:"daily_withdrawal_used"`
	DailyWithdrawalResetAt  time.Time       `json:"daily_withdrawal_reset_at" db:"daily_withdrawal_reset_at"`
	MonthlyWithdrawalUsed   decimal.Decimal `json:"monthly_withdrawal_used" db:"monthly_withdrawal_used"`
	MonthlyWithdrawalResetAt time.Time      `json:"monthly_withdrawal_reset_at" db:"monthly_withdrawal_reset_at"`
	CreatedAt               time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt               time.Time       `json:"updated_at" db:"updated_at"`
}

// LimitCheckResult contains the result of a limit check
type LimitCheckResult struct {
	Allowed           bool            `json:"allowed"`
	Reason            string          `json:"reason,omitempty"`
	CurrentUsage      decimal.Decimal `json:"currentUsage"`
	Limit             decimal.Decimal `json:"limit"`
	RemainingCapacity decimal.Decimal `json:"remainingCapacity"`
	ResetsAt          time.Time       `json:"resetsAt"`
	LimitType         string          `json:"limitType"` // "daily" or "monthly"
}

// UserLimitsResponse represents the API response for user limits
type UserLimitsResponse struct {
	KYCTier    KYCTier         `json:"kycTier"`
	Deposit    LimitDetails    `json:"deposit"`
	Withdrawal LimitDetails    `json:"withdrawal"`
}

// LimitDetails contains detailed limit information
type LimitDetails struct {
	Minimum       string        `json:"minimum"`
	Daily         PeriodLimit   `json:"daily"`
	Monthly       PeriodLimit   `json:"monthly"`
}

// PeriodLimit contains limit and usage for a period
type PeriodLimit struct {
	Limit     string    `json:"limit"`
	Used      string    `json:"used"`
	Remaining string    `json:"remaining"`
	ResetsAt  time.Time `json:"resetsAt"`
}

// Limit validation errors
var (
	ErrBelowMinimumDeposit    = errors.New("amount below minimum deposit")
	ErrBelowMinimumWithdrawal = errors.New("amount below minimum withdrawal")
	ErrDailyDepositExceeded   = errors.New("daily deposit limit exceeded")
	ErrMonthlyDepositExceeded = errors.New("monthly deposit limit exceeded")
	ErrDailyWithdrawalExceeded   = errors.New("daily withdrawal limit exceeded")
	ErrMonthlyWithdrawalExceeded = errors.New("monthly withdrawal limit exceeded")
)

// DeriveKYCTier derives the KYC tier from KYC status
func DeriveKYCTier(kycStatus string) KYCTier {
	switch kycStatus {
	case "approved", "verified":
		return KYCTierBasic
	case "advanced_approved", "advanced_verified":
		return KYCTierAdvanced
	default:
		return KYCTierUnverified
	}
}
