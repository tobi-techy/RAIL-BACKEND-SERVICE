package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Premium Global Dollar Retirement Vault
//
// This is the long-horizon sleeve of Rail's automated money system. The user
// never sees a wallet, chain, token or provider: they see Principal, Earnings,
// an unlock date and a fee for raiding growth early.
//
// Money model: contributions are funded into a Glider portfolio, so the USD
// leaves the double-entry ledger. Principal-vs-earnings is therefore a cost
// basis overlay (ContributionLots) over the portfolio read model, not a second
// ledger balance. Everything the vault reads to answer "what is this worth and
// when can I touch it" lives in Rail-owned tables, so Glider being down does
// not blind us.
// ---------------------------------------------------------------------------

// VaultTier is the retirement allocation profile a user chooses. Tiers map to
// Rail-owned provider strategies; users never build their own portfolio.
type VaultTier string

const (
	// VaultTierConservative is income + capital preservation.
	VaultTierConservative VaultTier = "conservative_pension"
	// VaultTierBalanced is growth with a large stabiliser.
	VaultTierBalanced VaultTier = "balanced_wealth"
	// VaultTierBold is long-horizon growth.
	VaultTierBold VaultTier = "bold_growth"
)

// Valid reports whether the tier is one Rail offers.
func (t VaultTier) Valid() bool {
	switch t {
	case VaultTierConservative, VaultTierBalanced, VaultTierBold:
		return true
	default:
		return false
	}
}

// Label is the only name the user ever sees. It must never contain crypto,
// chain, ticker or provider vocabulary.
func (t VaultTier) Label() string {
	switch t {
	case VaultTierConservative:
		return "Steady Dollar Income"
	case VaultTierBalanced:
		return "Balanced Global Wealth"
	case VaultTierBold:
		return "Long-Term Global Growth"
	default:
		return "Global Dollar Wealth"
	}
}

// Risk and Horizon map onto the investment strategy catalog.
func (t VaultTier) Risk() string {
	switch t {
	case VaultTierConservative:
		return "low"
	case VaultTierBalanced:
		return "medium"
	default:
		return "high"
	}
}

// Horizon is always long for a retirement vault.
func (t VaultTier) Horizon() string { return "long" }

// AllVaultTiers returns the offerable tiers in ascending risk order.
func AllVaultTiers() []VaultTier {
	return []VaultTier{VaultTierConservative, VaultTierBalanced, VaultTierBold}
}

// VaultStatus is the vault lifecycle state.
type VaultStatus string

const (
	VaultStatusActive VaultStatus = "active"
	VaultStatusPaused VaultStatus = "paused"
	VaultStatusClosed VaultStatus = "closed"
)

// Valid reports whether the status is known.
func (s VaultStatus) Valid() bool {
	switch s {
	case VaultStatusActive, VaultStatusPaused, VaultStatusClosed:
		return true
	default:
		return false
	}
}

// VaultLotStatus tracks whether a contribution lot still carries basis.
type VaultLotStatus string

const (
	VaultLotOpen     VaultLotStatus = "open"
	VaultLotConsumed VaultLotStatus = "consumed"
)

// VaultPenaltyStatus is the lifecycle of an early-withdrawal penalty.
type VaultPenaltyStatus string

const (
	VaultPenaltyPending   VaultPenaltyStatus = "pending"
	VaultPenaltyCommitted VaultPenaltyStatus = "committed"
	VaultPenaltyVoid      VaultPenaltyStatus = "void"
)

// VaultAuthorizationStatus is the lifecycle of a single-use withdrawal
// authorization. An authorization is the only thing that lets a vault-linked
// portfolio be withdrawn from.
type VaultAuthorizationStatus string

const (
	VaultAuthorizationIssued   VaultAuthorizationStatus = "issued"
	VaultAuthorizationConsumed VaultAuthorizationStatus = "consumed"
	VaultAuthorizationExpired  VaultAuthorizationStatus = "expired"
)

// OnrampProvider identifies the NGN→USD conversion rail used for a transfer.
type OnrampProvider string

const (
	OnrampProviderGraph      OnrampProvider = "graph"
	OnrampProviderRampHub    OnrampProvider = "ramphub"
	OnrampProviderPaj        OnrampProvider = "paj"
	OnrampProviderYellowCard OnrampProvider = "yellowcard"
	OnrampProviderKotani     OnrampProvider = "kotani"
)

// ---------------------------------------------------------------------------
// Persisted records
// ---------------------------------------------------------------------------

// RetirementVault is a user's locked USD retirement account.
type RetirementVault struct {
	ID                  uuid.UUID       `json:"vault_id" db:"id"`
	UserID              uuid.UUID       `json:"user_id" db:"user_id"`
	Name                string          `json:"name" db:"name"`
	Tier                VaultTier       `json:"tier" db:"tier"`
	Status              VaultStatus     `json:"status" db:"status"`
	RetirementAge       int             `json:"retirement_age" db:"retirement_age"`
	MinLockYears        int             `json:"min_lock_years" db:"min_lock_years"`
	FundedAt            *time.Time      `json:"funded_at,omitempty" db:"funded_at"`
	UnlockDate          *time.Time      `json:"unlock_date,omitempty" db:"unlock_date"`
	AutoContributionPct decimal.Decimal `json:"auto_contribution_pct" db:"auto_contribution_pct"`
	GliderEnrollmentID  *uuid.UUID      `json:"glider_enrollment_id,omitempty" db:"glider_enrollment_id"`
	CreatedAt           time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at" db:"updated_at"`
}

// Locked reports whether earnings are still locked. A vault with no unlock date
// is treated as locked (fail closed).
func (v *RetirementVault) Locked(now time.Time) bool {
	if v == nil || v.UnlockDate == nil {
		return true
	}
	return now.Before(*v.UnlockDate)
}

// VaultContributionLot is one cost-basis lot: money that entered the vault.
type VaultContributionLot struct {
	ID                  uuid.UUID        `json:"lot_id" db:"id"`
	VaultID             uuid.UUID        `json:"vault_id" db:"vault_id"`
	UserID              uuid.UUID        `json:"user_id" db:"user_id"`
	AmountUSD           decimal.Decimal  `json:"amount_usd" db:"amount_usd"`
	RemainingUSD        decimal.Decimal  `json:"remaining_usd" db:"remaining_usd"`
	SourceAccount       string           `json:"source_account,omitempty" db:"source_account"`
	LedgerTransactionID *uuid.UUID       `json:"ledger_transaction_id,omitempty" db:"ledger_transaction_id"`
	FundingTransferID   *uuid.UUID       `json:"funding_transfer_id,omitempty" db:"funding_transfer_id"`
	OnrampTransferID    *uuid.UUID       `json:"onramp_transfer_id,omitempty" db:"onramp_transfer_id"`
	NGNAmount           *decimal.Decimal `json:"ngn_amount,omitempty" db:"ngn_amount"`
	FXRate              *decimal.Decimal `json:"fx_rate,omitempty" db:"fx_rate"`
	AcquiredAt          time.Time        `json:"acquired_at" db:"acquired_at"`
	Status              VaultLotStatus   `json:"status" db:"status"`
	IdempotencyKey      string           `json:"idempotency_key,omitempty" db:"idempotency_key"`
	CreatedAt           time.Time        `json:"created_at" db:"created_at"`
}

// VaultEarningsSnapshot is a point-in-time principal/earnings valuation. It is
// written after each successful provider sync, so the vault can always answer
// from local state.
type VaultEarningsSnapshot struct {
	ID             uuid.UUID       `json:"snapshot_id" db:"id"`
	VaultID        uuid.UUID       `json:"vault_id" db:"vault_id"`
	UserID         uuid.UUID       `json:"user_id" db:"user_id"`
	PrincipalUSD   decimal.Decimal `json:"principal_usd" db:"principal_usd"`
	EarningsUSD    decimal.Decimal `json:"earnings_usd" db:"earnings_usd"`
	MarketValueUSD decimal.Decimal `json:"market_value_usd" db:"market_value_usd"`
	Source         string          `json:"source" db:"source"`
	AsOf           time.Time       `json:"as_of" db:"as_of"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
}

// VaultPenaltyEvent records the 10% haircut applied when earnings are raided
// before unlock. It is written pending when a withdrawal is authorised and
// committed when the money settles.
type VaultPenaltyEvent struct {
	ID                   uuid.UUID          `json:"penalty_event_id" db:"id"`
	VaultID              uuid.UUID          `json:"vault_id" db:"vault_id"`
	UserID               uuid.UUID          `json:"user_id" db:"user_id"`
	WithdrawalID         *uuid.UUID         `json:"withdrawal_id,omitempty" db:"withdrawal_id"`
	ExecutionID          *uuid.UUID         `json:"execution_id,omitempty" db:"execution_id"`
	EarningsWithdrawnUSD decimal.Decimal    `json:"earnings_withdrawn_usd" db:"earnings_withdrawn_usd"`
	PenaltyUSD           decimal.Decimal    `json:"penalty_usd" db:"penalty_usd"`
	Rate                 decimal.Decimal    `json:"rate" db:"rate"`
	Status               VaultPenaltyStatus `json:"status" db:"status"`
	Reason               string             `json:"reason,omitempty" db:"reason"`
	LedgerTransactionID  *uuid.UUID         `json:"ledger_transaction_id,omitempty" db:"ledger_transaction_id"`
	CreatedAt            time.Time          `json:"created_at" db:"created_at"`
	UpdatedAt            time.Time          `json:"updated_at" db:"updated_at"`
}

// VaultWithdrawalAuthorization is a single-use capability issued by the vault
// and consumed by the investment withdrawal gate. It is the mechanism that
// makes the lock fail closed: no valid authorization, no vault withdrawal.
type VaultWithdrawalAuthorization struct {
	ID           uuid.UUID                `json:"authorization_id" db:"id"`
	VaultID      uuid.UUID                `json:"vault_id" db:"vault_id"`
	UserID       uuid.UUID                `json:"user_id" db:"user_id"`
	EnrollmentID uuid.UUID                `json:"enrollment_id" db:"enrollment_id"`
	Key          string                   `json:"-" db:"key"`
	GrossUSD     decimal.Decimal          `json:"gross_usd" db:"gross_usd"`
	PenaltyUSD   decimal.Decimal          `json:"penalty_usd" db:"penalty_usd"`
	NetUSD       decimal.Decimal          `json:"net_usd" db:"net_usd"`
	Plan         []byte                   `json:"-" db:"plan"`
	Status       VaultAuthorizationStatus `json:"status" db:"status"`
	ExecutionID  *uuid.UUID               `json:"execution_id,omitempty" db:"execution_id"`
	ExpiresAt    time.Time                `json:"expires_at" db:"expires_at"`
	ConsumedAt   *time.Time               `json:"consumed_at,omitempty" db:"consumed_at"`
	CreatedAt    time.Time                `json:"created_at" db:"created_at"`
}

// VaultOnrampTransfer is the audit row for one NGN→USD conversion feeding the
// vault. It exists so every Naira unit can be reconciled NGN → USDC → position.
type VaultOnrampTransfer struct {
	ID             uuid.UUID       `json:"transfer_id" db:"id"`
	UserID         uuid.UUID       `json:"user_id" db:"user_id"`
	VaultID        *uuid.UUID      `json:"vault_id,omitempty" db:"vault_id"`
	Provider       OnrampProvider  `json:"provider" db:"provider"`
	Direction      string          `json:"direction" db:"direction"`
	NGNAmount      decimal.Decimal `json:"ngn_amount" db:"ngn_amount"`
	FXRate         decimal.Decimal `json:"fx_rate" db:"fx_rate"`
	USDCAmount     decimal.Decimal `json:"usdc_amount" db:"usdc_amount"`
	ProviderRef    string          `json:"provider_ref,omitempty" db:"provider_ref"`
	CircleTxRef    string          `json:"circle_tx_ref,omitempty" db:"circle_tx_ref"`
	Status         string          `json:"status" db:"status"`
	IdempotencyKey string          `json:"idempotency_key,omitempty" db:"idempotency_key"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at" db:"updated_at"`
}

// ---------------------------------------------------------------------------
// Computed models
// ---------------------------------------------------------------------------

// VaultPosition is the input to the withdrawal engine.
type VaultPosition struct {
	PrincipalUSD   decimal.Decimal
	MarketValueUSD decimal.Decimal
	UnlockDate     time.Time
}

// Earnings is the growth above what the user put in, floored at zero.
func (p VaultPosition) Earnings() decimal.Decimal {
	earnings := p.MarketValueUSD.Sub(p.PrincipalUSD)
	if earnings.IsNegative() {
		return decimal.Zero
	}
	return earnings
}

// VaultWithdrawalPlan is the deterministic decision of how a withdrawal is
// split between principal and earnings, and what penalty (if any) applies.
//
// The rule, encoded and tested: withdrawals consume principal first, so a user
// can always reach their own money cheaply. Only the earnings portion taken
// before unlock is haircut, at PenaltyRate.
type VaultWithdrawalPlan struct {
	VaultID              uuid.UUID       `json:"vault_id"`
	EnrollmentID         uuid.UUID       `json:"enrollment_id"`
	GrossUSD             decimal.Decimal `json:"gross_usd"`
	PrincipalReturnedUSD decimal.Decimal `json:"principal_returned_usd"`
	EarningsReturnedUSD  decimal.Decimal `json:"earnings_returned_usd"`
	PenaltyRate          decimal.Decimal `json:"penalty_rate"`
	PenaltyUSD           decimal.Decimal `json:"penalty_usd"`
	NetToUserUSD         decimal.Decimal `json:"net_to_user_usd"`
	Locked               bool            `json:"locked"`
	UnlockDate           time.Time       `json:"unlock_date"`
	// SettlementAccount is the Rail-controlled recipient the provider withdraws
	// to; DestinationAccount is where the user's net proceeds are credited once
	// the money lands.
	SettlementAccount  string    `json:"-"`
	DestinationAccount string    `json:"destination_account"`
	PenaltyEventID     uuid.UUID `json:"penalty_event_id"`
	AuthorizationKey   string    `json:"-"`
}

// VaultView is the single read model the app renders. It never contains chain,
// ticker or provider vocabulary.
type VaultView struct {
	VaultID                  uuid.UUID       `json:"vault_id"`
	Name                     string          `json:"name"`
	Tier                     VaultTier       `json:"tier"`
	TierLabel                string          `json:"tier_label"`
	Status                   VaultStatus     `json:"status"`
	PrincipalUSD             decimal.Decimal `json:"principal_usd"`
	MarketValueUSD           decimal.Decimal `json:"market_value_usd"`
	EarningsUSD              decimal.Decimal `json:"earnings_usd"`
	UnlockDate               *time.Time      `json:"unlock_date,omitempty"`
	Locked                   bool            `json:"locked"`
	PenaltyIfWithdrawnNowUSD decimal.Decimal `json:"penalty_if_withdrawn_now_usd"`
	PenaltyRate              decimal.Decimal `json:"penalty_rate"`
	AutoContributionPct      decimal.Decimal `json:"auto_contribution_pct"`
	RetirementAge            int             `json:"retirement_age"`
	MinLockYears             int             `json:"min_lock_years"`
	AsOf                     time.Time       `json:"as_of"`
	Source                   string          `json:"source"`
	Stale                    bool            `json:"stale"`
}

// VaultWithdrawalResult is returned by a vault withdrawal request.
type VaultWithdrawalResult struct {
	Plan        *VaultWithdrawalPlan `json:"plan"`
	ExecutionID *uuid.UUID           `json:"execution_id,omitempty"`
	OperationID string               `json:"operation_id,omitempty"`
	Status      string               `json:"status"`
}

// VaultContributionResult reports one funded contribution.
type VaultContributionResult struct {
	LotID      uuid.UUID       `json:"lot_id"`
	AmountUSD  decimal.Decimal `json:"amount_usd"`
	FundedAt   time.Time       `json:"funded_at"`
	UnlockDate *time.Time      `json:"unlock_date,omitempty"`
}

// VaultStrategyOption is a selectable tier for the app's chooser.
type VaultStrategyOption struct {
	Tier        VaultTier `json:"tier"`
	Label       string    `json:"label"`
	Description string    `json:"description"`
}

// ---------------------------------------------------------------------------
// Requests
// ---------------------------------------------------------------------------

// VaultCreateRequest opens a retirement vault and enrolls the user.
type VaultCreateRequest struct {
	Name                string          `json:"name,omitempty"`
	Tier                VaultTier       `json:"tier"`
	RetirementAge       int             `json:"retirement_age,omitempty"`
	AutoContributionPct decimal.Decimal `json:"auto_contribution_pct,omitempty"`
	// ConfirmationToken replays the enrollment confirmation. Opening a vault is
	// a two-stage, confirmed action, exactly like every other portfolio mutation.
	ConfirmationToken string `json:"confirmation_token,omitempty"`
}

// VaultCreateResponse is returned by the create-vault flow. When Status is
// AWAITING_CONFIRMATION the caller must confirm and replay.
type VaultCreateResponse struct {
	Status       InvestmentActionStatus   `json:"status"`
	View         *VaultView               `json:"vault,omitempty"`
	Confirmation *InvestmentPendingAction `json:"confirmation,omitempty"`
}

// VaultActivityEntry is one line of the plain-language activity feed.
type VaultActivityEntry struct {
	Kind       string          `json:"kind"`
	At         time.Time       `json:"at"`
	AmountUSD  decimal.Decimal `json:"amount_usd"`
	PenaltyUSD decimal.Decimal `json:"penalty_usd"`
}

// VaultUpdateRequest changes the contribution rule or retirement age.
type VaultUpdateRequest struct {
	AutoContributionPct *decimal.Decimal `json:"auto_contribution_pct,omitempty"`
	RetirementAge       *int             `json:"retirement_age,omitempty"`
}

// VaultWithdrawRequest asks for money out of the vault.
type VaultWithdrawRequest struct {
	AmountUSD          decimal.Decimal `json:"amount_usd"`
	DestinationAccount string          `json:"destination_account,omitempty"`
}
