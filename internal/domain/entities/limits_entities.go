package entities

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// KYCTier represents user verification level for transaction limits
type KYCTier string

const (
	KYCTierUnverified KYCTier = "unverified" // No account setup — all transactions blocked
	KYCTierNonKYC     KYCTier = "non_kyc"    // Tier 1: wallet created, no KYC — limited crypto + NGN ramp
	KYCTierBasic      KYCTier = "basic"      // Tier 2: BVN + NIN verified via Graph — NGN virtual account
	KYCTierAdvanced   KYCTier = "advanced"   // Tier 3: Bridge KYC — USD virtual account, cards, investing
)

// ── Non-KYC Limits (crypto-only, no fiat) ────────────────────────

var (
	NonKYCDailyTransferLimit   = decimal.NewFromFloat(100.00) // $100/day
	NonKYCMonthlyTransferLimit = decimal.NewFromFloat(500.00) // $500/month
	NonKYCMaxTransferAmount    = decimal.NewFromFloat(100.00) // $100 per transaction
	NonKYCMinTransferAmount    = decimal.NewFromFloat(1.00)   // $1 minimum
)

// ── Non-KYC NGN Limits (Naira allowed without KYC) ───────────────

var (
	NonKYCDailyTransferLimitNGN   = decimal.NewFromFloat(50000.00)  // ₦50,000/day
	NonKYCMonthlyTransferLimitNGN = decimal.NewFromFloat(200000.00) // ₦200,000/month
	NonKYCMaxTransferAmountNGN    = decimal.NewFromFloat(50000.00)  // ₦50,000 per transaction
	NonKYCMinTransferAmountNGN    = decimal.NewFromFloat(500.00)    // ₦500 minimum
)

// ── USD Limits ───────────────────────────────────────────────────

var (
	MinDepositAmount    = decimal.NewFromFloat(1.00)
	MinWithdrawalAmount = decimal.NewFromFloat(1.00)
	MaxWithdrawalAmount = decimal.NewFromFloat(10000.00)

	// Tier 2 (basic — BVN+NIN) — USD
	BasicDailyDepositLimit      = decimal.NewFromFloat(5000.00)
	BasicMonthlyDepositLimit    = decimal.NewFromFloat(25000.00)
	BasicDailyWithdrawalLimit   = decimal.NewFromFloat(2500.00)
	BasicMonthlyWithdrawalLimit = decimal.NewFromFloat(25000.00)

	// Tier 3 (advanced — Bridge KYC) — USD
	AdvancedDailyDepositLimit      = decimal.NewFromFloat(50000.00)
	AdvancedMonthlyDepositLimit    = decimal.NewFromFloat(250000.00)
	AdvancedDailyWithdrawalLimit   = decimal.NewFromFloat(10000.00)
	AdvancedMonthlyWithdrawalLimit = decimal.NewFromFloat(150000.00)
)

// ── NGN Limits ───────────────────────────────────────────────────

var (
	MinDepositAmountNGN    = decimal.NewFromFloat(500.00) // ₦500
	MinWithdrawalAmountNGN = decimal.NewFromFloat(500.00)

	// Tier 2 (basic — BVN+NIN) — NGN
	BasicDailyDepositLimitNGN      = decimal.NewFromFloat(2000000.00)  // ₦2M
	BasicMonthlyDepositLimitNGN    = decimal.NewFromFloat(10000000.00) // ₦10M
	BasicDailyWithdrawalLimitNGN   = decimal.NewFromFloat(1000000.00)  // ₦1M
	BasicMonthlyWithdrawalLimitNGN = decimal.NewFromFloat(5000000.00)  // ₦5M

	// Tier 3 (advanced — Bridge KYC) — NGN
	AdvancedDailyDepositLimitNGN      = decimal.NewFromFloat(10000000.00) // ₦10M
	AdvancedMonthlyDepositLimitNGN    = decimal.NewFromFloat(50000000.00) // ₦50M
	AdvancedDailyWithdrawalLimitNGN   = decimal.NewFromFloat(5000000.00)  // ₦5M
	AdvancedMonthlyWithdrawalLimitNGN = decimal.NewFromFloat(25000000.00) // ₦25M
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
		if currency == "NGN" {
			return TransactionLimitConfig{
				Tier:                   KYCTierNonKYC,
				Currency:               "NGN",
				MinWithdrawal:          NonKYCMinTransferAmountNGN,
				MaxWithdrawal:          NonKYCMaxTransferAmountNGN,
				DailyWithdrawalLimit:   NonKYCDailyTransferLimitNGN,
				MonthlyWithdrawalLimit: NonKYCMonthlyTransferLimitNGN,
				MinDeposit:             NonKYCMinTransferAmountNGN,
				DailyDepositLimit:      NonKYCDailyTransferLimitNGN,
				MonthlyDepositLimit:    NonKYCMonthlyTransferLimitNGN,
			}
		}
		// Non-KYC USD: crypto transfers only
		return TransactionLimitConfig{
			Tier:                   KYCTierNonKYC,
			Currency:               "USD",
			MinWithdrawal:          NonKYCMinTransferAmount,
			MaxWithdrawal:          NonKYCMaxTransferAmount,
			DailyWithdrawalLimit:   NonKYCDailyTransferLimit,
			MonthlyWithdrawalLimit: NonKYCMonthlyTransferLimit,
			MinDeposit:             NonKYCMinTransferAmount,
			DailyDepositLimit:      NonKYCDailyTransferLimit,
			MonthlyDepositLimit:    NonKYCMonthlyTransferLimit,
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
			MinDeposit: MinDepositAmount, DailyDepositLimit: AdvancedDailyDepositLimit, MonthlyDepositLimit: AdvancedMonthlyDepositLimit,
			MinWithdrawal: MinWithdrawalAmount, MaxWithdrawal: MaxWithdrawalAmount, DailyWithdrawalLimit: AdvancedDailyWithdrawalLimit, MonthlyWithdrawalLimit: AdvancedMonthlyWithdrawalLimit,
		}
	default: // Basic
		return TransactionLimitConfig{
			Tier: KYCTierBasic, Currency: "USD",
			MinDeposit: MinDepositAmount, DailyDepositLimit: BasicDailyDepositLimit, MonthlyDepositLimit: BasicMonthlyDepositLimit,
			MinWithdrawal: MinWithdrawalAmount, MaxWithdrawal: MaxWithdrawalAmount, DailyWithdrawalLimit: BasicDailyWithdrawalLimit, MonthlyWithdrawalLimit: BasicMonthlyWithdrawalLimit,
		}
	}
}

func getNGNLimits(tier KYCTier) TransactionLimitConfig {
	switch tier {
	case KYCTierAdvanced:
		return TransactionLimitConfig{
			Tier: KYCTierAdvanced, Currency: "NGN",
			MinDeposit: MinDepositAmountNGN, DailyDepositLimit: AdvancedDailyDepositLimitNGN, MonthlyDepositLimit: AdvancedMonthlyDepositLimitNGN,
			MinWithdrawal: MinWithdrawalAmountNGN, MaxWithdrawal: AdvancedDailyWithdrawalLimitNGN, DailyWithdrawalLimit: AdvancedDailyWithdrawalLimitNGN, MonthlyWithdrawalLimit: AdvancedMonthlyWithdrawalLimitNGN,
		}
	default: // Basic
		return TransactionLimitConfig{
			Tier: KYCTierBasic, Currency: "NGN",
			MinDeposit: MinDepositAmountNGN, DailyDepositLimit: BasicDailyDepositLimitNGN, MonthlyDepositLimit: BasicMonthlyDepositLimitNGN,
			MinWithdrawal: MinWithdrawalAmountNGN, MaxWithdrawal: BasicDailyWithdrawalLimitNGN, DailyWithdrawalLimit: BasicDailyWithdrawalLimitNGN, MonthlyWithdrawalLimit: BasicMonthlyWithdrawalLimitNGN,
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
		return KYCTierNonKYC
	case "pending", "processing":
		// Users who signed up but haven't done KYC — allow limited crypto
		return KYCTierNonKYC
	default:
		return KYCTierUnverified
	}
}

// ── Tiered KYC ladder ─────────────────────────────────────────────
//
// Rail runs a 3-tier ladder split by provider domain:
//
//	Tier 1 (non_kyc):  signup only — crypto + NGN ramp (RampHub/Paj) at capped limits.
//	Tier 2 (basic):    BVN + NIN verified via Graph — unlocks the Graph NGN named
//	                   virtual account and higher NGN limits.
//	Tier 3 (advanced): Bridge KYC active (Didit doc+selfie → Bridge) — unlocks the
//	                   Bridge USD virtual account, cards, and investing (incl. tokenized).
const (
	KYCTierLevelUnverified = 0
	KYCTierLevelNonKYC     = 1
	KYCTierLevelBasic      = 2
	KYCTierLevelAdvanced   = 3
)

// TierLevelToKYCTier maps the persisted numeric tier to a KYCTier.
func TierLevelToKYCTier(level int) KYCTier {
	switch level {
	case KYCTierLevelAdvanced:
		return KYCTierAdvanced
	case KYCTierLevelBasic:
		return KYCTierBasic
	case KYCTierLevelNonKYC:
		return KYCTierNonKYC
	default:
		return KYCTierUnverified
	}
}

// KYCTierToLevel maps a KYCTier to the persisted numeric tier.
func KYCTierToLevel(tier KYCTier) int {
	switch tier {
	case KYCTierAdvanced:
		return KYCTierLevelAdvanced
	case KYCTierBasic:
		return KYCTierLevelBasic
	case KYCTierNonKYC:
		return KYCTierLevelNonKYC
	case KYCTierUnverified:
		return KYCTierLevelUnverified
	default:
		return KYCTierLevelNonKYC
	}
}

// EffectiveKYCTier resolves a user's tier, preferring the explicit persisted
// numeric tier. Account-level blocks stop all access regardless of tier, and the
// legacy-status fallback can never grant Tier 3 (advanced requires a persisted
// tier set by Bridge KYC), keeping the ladder authoritative and downgrade-safe.
func EffectiveKYCTier(tierLevel int, kycStatus string) KYCTier {
	switch strings.ToLower(strings.TrimSpace(kycStatus)) {
	case "blocked", "suspended", "banned", "frozen", "deactivated":
		return KYCTierUnverified
	}
	if tierLevel >= KYCTierLevelBasic {
		return TierLevelToKYCTier(tierLevel)
	}
	derived := DeriveKYCTier(kycStatus)
	if KYCTierToLevel(derived) > KYCTierLevelBasic {
		return KYCTierBasic
	}
	return derived
}

// TierCapabilities describes what a given tier unlocks.
type TierCapabilities struct {
	Tier               int  `json:"tier"`
	CanDepositCrypto   bool `json:"can_deposit_crypto"`
	CanReceiveNGN      bool `json:"can_receive_ngn"`      // Graph NGN virtual account (Tier 2)
	CanDepositFiatUSD  bool `json:"can_deposit_fiat_usd"` // Bridge USD virtual account (Tier 3)
	CanUseCard         bool `json:"can_use_card"`         // Bridge cards (Tier 3)
	CanInvest          bool `json:"can_invest"`           // Brokerage investing (Tier 3)
	CanInvestTokenized bool `json:"can_invest_tokenized"` // Tokenized-asset investing / LI.FI (Tier 3)
}

// CapabilitiesForTier returns the capability set unlocked at a numeric tier.
//
//	Tier 1 (non_kyc):  crypto + NGN ramp (limited caps).
//	Tier 2 (basic):    + Graph NGN named virtual account, higher NGN limits.
//	Tier 3 (advanced): + Bridge USD virtual account, cards, investing (incl. tokenized).
func CapabilitiesForTier(tierLevel int) TierCapabilities {
	caps := TierCapabilities{Tier: tierLevel}
	if tierLevel >= KYCTierLevelNonKYC {
		caps.CanDepositCrypto = true
	}
	if tierLevel >= KYCTierLevelBasic {
		caps.CanReceiveNGN = true
	}
	if tierLevel >= KYCTierLevelAdvanced {
		caps.CanDepositFiatUSD = true
		caps.CanUseCard = true
		caps.CanInvest = true
		caps.CanInvestTokenized = true
	}
	return caps
}
