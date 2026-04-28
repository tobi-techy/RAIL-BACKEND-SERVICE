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
	KYCTierUnverified KYCTier = "unverified" // No account setup — all transactions blocked
	KYCTierNonKYC     KYCTier = "non_kyc"    // Circle wallet created, no KYC — limited crypto only
	KYCTierBasic      KYCTier = "basic"      // Basic identity verification (Tier 1)
	KYCTierAdvanced   KYCTier = "advanced"   // Advanced verification with proof of address/funds (Tier 2)
)

// ── Non-KYC Limits (crypto-only, no fiat) ────────────────────────

var (
	NonKYCDailyTransferLimit   = decimal.NewFromFloat(100.00)   // $100/day
	NonKYCMonthlyTransferLimit = decimal.NewFromFloat(500.00)   // $500/month
	NonKYCMaxTransferAmount    = decimal.NewFromFloat(100.00)   // $100 per transaction
	NonKYCMinTransferAmount    = decimal.NewFromFloat(1.00)     // $1 minimum
)

// ── USD Limits ───────────────────────────────────────────────────

var (
	MinDepositAmount = decimal.NewFromFloat(1.00)
	MinWithdrawalAmount = decimal.NewFromFloat(1.00)
	MaxWithdrawalAmount = decimal.NewFromFloat(10000.00)

	// Tier 1 (Basic KYC) — USD
	Tier1DailyDepositLimit   = decimal.NewFromFloat(5000.00)
	Tier1MonthlyDepositLimit = decimal.NewFromFloat(25000.00)
	Tier1DailyWithdrawalLimit   = decimal.NewFromFloat(2500.00)
	Tier1MonthlyWithdrawalLimit = decimal.NewFromFloat(25000.00)

	// Tier 2 (Advanced KYC) — USD
	Tier2DailyDepositLimit   = decimal.NewFromFloat(50000.00)
	Tier2MonthlyDepositLimit = decimal.NewFromFloat(250000.00)
	Tier2DailyWithdrawalLimit   = decimal.NewFromFloat(10000.00)
	Tier2MonthlyWithdrawalLimit = decimal.NewFromFloat(150000.00)
)

// ── NGN Limits ───────────────────────────────────────────────────

var (
	MinDepositAmountNGN = decimal.NewFromFloat(100.00) // ₦100
	MinWithdrawalAmountNGN = decimal.NewFromFloat(100.00)

	// Tier 1 (BVN verified) — NGN
	Tier1DailyDepositLimitNGN   = decimal.NewFromFloat(2000000.00)  // ₦2M
	Tier1MonthlyDepositLimitNGN = decimal.NewFromFloat(10000000.00) // ₦10M
	Tier1DailyWithdrawalLimitNGN   = decimal.NewFromFloat(1000000.00)  // ₦1M
	Tier1MonthlyWithdrawalLimitNGN = decimal.NewFromFloat(5000000.00)  // ₦5M

	// Tier 2 (Full KYC) — NGN
	Tier2DailyDepositLimitNGN   = decimal.NewFromFloat(10000000.00) // ₦10M
	Tier2MonthlyDepositLimitNGN = decimal.NewFromFloat(50000000.00) // ₦50M
	Tier2DailyWithdrawalLimitNGN   = decimal.NewFromFloat(5000000.00)  // ₦5M
	Tier2MonthlyWithdrawalLimitNGN = decimal.NewFromFloat(25000000.00) // ₦25M
)

// TransactionLimitConfig holds limits for a specific KYC tier and currency
type TransactionLimitConfig struct {
	Tier                   KYCTier
	Currency               string
	MinDeposit             decimal.Decimal
	DailyDepositLimit      decimal.Decimal
	MonthlyDepositLimit    decimal.Decimal
	MinWithdrawal          decimal.Decimal
	MaxWithdrawal          decimal.Decimal
	DailyWithdrawalLimit   decimal.Decimal
	MonthlyWithdrawalLimit decimal.Decimal
}

// GetLimitConfigForTier returns the limit configuration for a KYC tier.
// Unverified users get zero limits — deposits/withdrawals are blocked at the handler level.
func GetLimitConfigForTier(tier KYCTier) TransactionLimitConfig {
	return GetLimitConfigForTierAndCurrency(tier, "USD")
}

// GetLimitConfigForTierAndCurrency returns currency-specific limits.
func GetLimitConfigForTierAndCurrency(tier KYCTier, currency string) TransactionLimitConfig {
	if tier == KYCTierUnverified {
		return TransactionLimitConfig{
			Tier:     KYCTierUnverified,
			Currency: currency,
			// All zeros — unverified users cannot transact
		}
	}

	if tier == KYCTierNonKYC {
		// Non-KYC: crypto transfers only, no fiat. Currency-agnostic USD limits.
		return TransactionLimitConfig{
			Tier:                   KYCTierNonKYC,
			Currency:               "USD",
			MinWithdrawal:          NonKYCMinTransferAmount,
			MaxWithdrawal:          NonKYCMaxTransferAmount,
			DailyWithdrawalLimit:   NonKYCDailyTransferLimit,
			MonthlyWithdrawalLimit: NonKYCMonthlyTransferLimit,
			// Deposits: same limits as withdrawals for non-KYC
			MinDeposit:          NonKYCMinTransferAmount,
			DailyDepositLimit:   NonKYCDailyTransferLimit,
			MonthlyDepositLimit: NonKYCMonthlyTransferLimit,
		}
	}

	if currency == "NGN" {
		return getNGNLimits(tier)
	}
	return getUSDLimits(tier)
}

func getUSDLimits(tier KYCTier) TransactionLimitConfig {
	switch tier {
	case KYCTierAdvanced:
		return TransactionLimitConfig{
			Tier: KYCTierAdvanced, Currency: "USD",
			MinDeposit: MinDepositAmount, DailyDepositLimit: Tier2DailyDepositLimit, MonthlyDepositLimit: Tier2MonthlyDepositLimit,
			MinWithdrawal: MinWithdrawalAmount, MaxWithdrawal: MaxWithdrawalAmount, DailyWithdrawalLimit: Tier2DailyWithdrawalLimit, MonthlyWithdrawalLimit: Tier2MonthlyWithdrawalLimit,
		}
	default: // Basic
		return TransactionLimitConfig{
			Tier: KYCTierBasic, Currency: "USD",
			MinDeposit: MinDepositAmount, DailyDepositLimit: Tier1DailyDepositLimit, MonthlyDepositLimit: Tier1MonthlyDepositLimit,
			MinWithdrawal: MinWithdrawalAmount, MaxWithdrawal: MaxWithdrawalAmount, DailyWithdrawalLimit: Tier1DailyWithdrawalLimit, MonthlyWithdrawalLimit: Tier1MonthlyWithdrawalLimit,
		}
	}
}

func getNGNLimits(tier KYCTier) TransactionLimitConfig {
	switch tier {
	case KYCTierAdvanced:
		return TransactionLimitConfig{
			Tier: KYCTierAdvanced, Currency: "NGN",
			MinDeposit: MinDepositAmountNGN, DailyDepositLimit: Tier2DailyDepositLimitNGN, MonthlyDepositLimit: Tier2MonthlyDepositLimitNGN,
			MinWithdrawal: MinWithdrawalAmountNGN, MaxWithdrawal: Tier2DailyWithdrawalLimitNGN, DailyWithdrawalLimit: Tier2DailyWithdrawalLimitNGN, MonthlyWithdrawalLimit: Tier2MonthlyWithdrawalLimitNGN,
		}
	default: // Basic
		return TransactionLimitConfig{
			Tier: KYCTierBasic, Currency: "NGN",
			MinDeposit: MinDepositAmountNGN, DailyDepositLimit: Tier1DailyDepositLimitNGN, MonthlyDepositLimit: Tier1MonthlyDepositLimitNGN,
			MinWithdrawal: MinWithdrawalAmountNGN, MaxWithdrawal: Tier1DailyWithdrawalLimitNGN, DailyWithdrawalLimit: Tier1DailyWithdrawalLimitNGN, MonthlyWithdrawalLimit: Tier1MonthlyWithdrawalLimitNGN,
		}
	}
}

// UserTransactionUsage tracks a user's transaction usage within limit periods
type UserTransactionUsage struct {
	ID                       uuid.UUID       `json:"id" db:"id"`
	UserID                   uuid.UUID       `json:"user_id" db:"user_id"`
	DailyDepositUsed         decimal.Decimal `json:"daily_deposit_used" db:"daily_deposit_used"`
	DailyDepositResetAt      time.Time       `json:"daily_deposit_reset_at" db:"daily_deposit_reset_at"`
	MonthlyDepositUsed       decimal.Decimal `json:"monthly_deposit_used" db:"monthly_deposit_used"`
	MonthlyDepositResetAt    time.Time       `json:"monthly_deposit_reset_at" db:"monthly_deposit_reset_at"`
	DailyWithdrawalUsed      decimal.Decimal `json:"daily_withdrawal_used" db:"daily_withdrawal_used"`
	DailyWithdrawalResetAt   time.Time       `json:"daily_withdrawal_reset_at" db:"daily_withdrawal_reset_at"`
	MonthlyWithdrawalUsed    decimal.Decimal `json:"monthly_withdrawal_used" db:"monthly_withdrawal_used"`
	MonthlyWithdrawalResetAt time.Time       `json:"monthly_withdrawal_reset_at" db:"monthly_withdrawal_reset_at"`
	CreatedAt                time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt                time.Time       `json:"updated_at" db:"updated_at"`
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
	KYCTier    KYCTier      `json:"kycTier"`
	Currency   string       `json:"currency"`
	Deposit    LimitDetails `json:"deposit"`
	Withdrawal LimitDetails `json:"withdrawal"`
}

// LimitDetails contains detailed limit information
type LimitDetails struct {
	Minimum string      `json:"minimum"`
	Daily   PeriodLimit `json:"daily"`
	Monthly PeriodLimit `json:"monthly"`
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
	ErrUnverifiedUser            = errors.New("identity verification required before transacting")
	ErrBelowMinimumDeposit       = errors.New("amount below minimum deposit")
	ErrBelowMinimumWithdrawal    = errors.New("amount below minimum withdrawal")
	ErrDailyDepositExceeded      = errors.New("daily deposit limit exceeded")
	ErrMonthlyDepositExceeded    = errors.New("monthly deposit limit exceeded")
	ErrDailyWithdrawalExceeded   = errors.New("daily withdrawal limit exceeded")
	ErrMonthlyWithdrawalExceeded = errors.New("monthly withdrawal limit exceeded")
)

// DeriveKYCTier derives the KYC tier from KYC status and onboarding status.
func DeriveKYCTier(kycStatus string) KYCTier {
	switch kycStatus {
	case "approved", "verified":
		return KYCTierBasic
	case "advanced_approved", "advanced_verified":
		return KYCTierAdvanced
	case "non_kyc", "basic_complete":
		// User completed basic onboarding (has Circle wallet) but no KYC
		return KYCTierNonKYC
	default:
		return KYCTierUnverified
	}
}
