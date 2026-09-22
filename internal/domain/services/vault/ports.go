package vault

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

// Config is the vault's policy. All of it is server-side: the lock age, the
// minimum lock, the penalty rate and the funding source can never be set by a
// caller or an agent.
type Config struct {
	Enabled bool

	DefaultRetirementAge int
	MinLockYears         int

	// PenaltyRate is the haircut on earnings taken before unlock (0.10 = 10%).
	PenaltyRate decimal.Decimal

	DefaultAutoContributionPct decimal.Decimal
	MaxAutoContributionPct     decimal.Decimal
	MinContributionUSD         decimal.Decimal

	// ContributionSource is the ledger bucket a contribution is debited from.
	// It is deliberately "spending": the stash bucket is protected by the
	// 90-day lock, and moving retirement money out of stash would either be
	// blocked or charged an emergency fee.
	ContributionSource string

	// SettlementAccount is the Rail-controlled address the provider withdraws
	// to. It is the only recipient a vault withdrawal may use, which is what
	// lets Rail compute and retain the penalty before anything reaches a user.
	SettlementAccount string

	AuthorizationTTL time.Duration
	StaleAfter       time.Duration

	// MinSpendableUSD is the floor the automatic-savings hook leaves in
	// spendable. A take that would breach it is skipped, never partialled.
	MinSpendableUSD decimal.Decimal

	// HealthDriftThresholdPct flags a vault when provider value drifts this
	// far from ledger basis (percent). Zero disables the drift flag.
	HealthDriftThresholdPct decimal.Decimal

	// TierFiles are checked-in YAML tier definitions the bootstrap validates
	// against the catalog before calling EnsureRailStrategy. Empty disables.
	TierFiles []string

	Strategies []StrategyDefinition
}

// StrategyDefinition is a Rail-owned allocation the vault enrolls users into.
type StrategyDefinition struct {
	Tier    entities.VaultTier
	Name    string
	Risk    string
	Horizon string
	Legs    []entities.InvestmentAllocationLeg
}

// ---------------------------------------------------------------------------
// Ports
// ---------------------------------------------------------------------------

// Repository persists the vault's own records. Nothing here touches the
// provider: this is the Rail-owned truth that keeps the vault answerable when
// the provider is unreachable.
type Repository interface {
	CreateVault(ctx context.Context, vault *entities.RetirementVault) error
	UpdateVault(ctx context.Context, vault *entities.RetirementVault) error
	GetVaultByID(ctx context.Context, id uuid.UUID) (*entities.RetirementVault, error)
	GetActiveVaultByUser(ctx context.Context, userID uuid.UUID) (*entities.RetirementVault, error)
	GetVaultByEnrollment(ctx context.Context, enrollmentID uuid.UUID) (*entities.RetirementVault, error)

	CreateLot(ctx context.Context, lot *entities.VaultContributionLot) error
	UpdateLot(ctx context.Context, lot *entities.VaultContributionLot) error
	GetLotByKey(ctx context.Context, key string) (*entities.VaultContributionLot, error)
	ListOpenLots(ctx context.Context, vaultID uuid.UUID) ([]*entities.VaultContributionLot, error)
	ListLots(ctx context.Context, vaultID uuid.UUID, limit int) ([]*entities.VaultContributionLot, error)

	CreateSnapshot(ctx context.Context, snapshot *entities.VaultEarningsSnapshot) error
	LatestSnapshot(ctx context.Context, vaultID uuid.UUID) (*entities.VaultEarningsSnapshot, error)

	CreatePenaltyEvent(ctx context.Context, event *entities.VaultPenaltyEvent) error
	UpdatePenaltyEvent(ctx context.Context, event *entities.VaultPenaltyEvent) error
	GetPenaltyEvent(ctx context.Context, id uuid.UUID) (*entities.VaultPenaltyEvent, error)
	GetPenaltyEventByExecution(ctx context.Context, executionID uuid.UUID) (*entities.VaultPenaltyEvent, error)

	CreateAuthorization(ctx context.Context, auth *entities.VaultWithdrawalAuthorization) error
	// ConsumeAuthorization atomically claims a single-use authorization. It
	// returns ErrAuthorizationInvalid when the key is unknown, already used,
	// expired, or issued for a different enrollment or amount. Concurrency is
	// resolved by the database: exactly one caller can win a given key.
	ConsumeAuthorization(ctx context.Context, key string, enrollmentID uuid.UUID, grossUSD decimal.Decimal, now time.Time) (*entities.VaultWithdrawalAuthorization, error)
	AttachAuthorizationExecution(ctx context.Context, key string, executionID uuid.UUID) error
	// HasIssuedAuthorization reports whether a live authorization exists for an
	// enrollment, i.e. a withdrawal is already in flight.
	HasIssuedAuthorization(ctx context.Context, enrollmentID uuid.UUID, now time.Time) (bool, error)
	// ExpireAuthorization releases an authorization that will never be used, so a
	// failed attempt does not lock the user out until the TTL lapses.
	ExpireAuthorization(ctx context.Context, key string, now time.Time) error
	GetAuthorizationByExecution(ctx context.Context, executionID uuid.UUID) (*entities.VaultWithdrawalAuthorization, error)
	ListAuthorizations(ctx context.Context, vaultID uuid.UUID, limit int) ([]*entities.VaultWithdrawalAuthorization, error)
	ListPenaltyEvents(ctx context.Context, vaultID uuid.UUID, limit int) ([]*entities.VaultPenaltyEvent, error)

	CreateOnrampTransfer(ctx context.Context, transfer *entities.VaultOnrampTransfer) error
	UpdateOnrampTransfer(ctx context.Context, transfer *entities.VaultOnrampTransfer) error
	FindOnrampByIdempotencyKey(ctx context.Context, key string) (*entities.VaultOnrampTransfer, error)

	// Tiers persist which provider strategy each tier resolved to.
	UpsertTier(ctx context.Context, tier *entities.VaultTierBinding) error
	GetTier(ctx context.Context, tier entities.VaultTier) (*entities.VaultTierBinding, error)
	ListTiers(ctx context.Context) ([]*entities.VaultTierBinding, error)

	// Skips record automatic-saving takes the floor refused.
	CreateSkip(ctx context.Context, skip *entities.VaultSkip) error
	FindSkipByPayment(ctx context.Context, paymentID uuid.UUID) (*entities.VaultSkip, error)
	ListSkips(ctx context.Context, vaultID uuid.UUID, limit int) ([]*entities.VaultSkip, error)
	CountRecentSkips(ctx context.Context, vaultID uuid.UUID, since time.Time) (int, error)

	// Health is the ops/user-actionable state of one vault.
	UpsertHealth(ctx context.Context, health *entities.VaultHealth) error
	GetHealth(ctx context.Context, vaultID uuid.UUID) (*entities.VaultHealth, error)

	// Enroll failures are ops-observable open attempts that never produced a
	// vault row (so they cannot own vault_health, which is keyed by vault).
	GetEnrollFailure(ctx context.Context, userID uuid.UUID) (*entities.VaultEnrollFailure, error)
	UpsertEnrollFailure(ctx context.Context, failure *entities.VaultEnrollFailure) error

	// DeleteVault removes a vault row created before its portfolio link failed,
	// so a failed open never leaves a row that later reads can mistake for a
	// plan. Only pending rows may be removed.
	DeleteVault(ctx context.Context, vaultID uuid.UUID) error
}

// InvestmentEngine is the slice of the investment service the vault drives.
// The vault never calls the provider directly: it goes through the same
// deterministic, audited engine every other portfolio does.
type InvestmentEngine interface {
	EnsureRailStrategy(ctx context.Context, req entities.InvestmentRailStrategyRequest) (*entities.InvestmentStrategy, error)
	ListRailStrategies(ctx context.Context) ([]*entities.InvestmentStrategy, error)
	Enroll(ctx context.Context, userID uuid.UUID, req *entities.InvestmentEnrollRequest, actor entities.InvestmentActor) (*entities.InvestmentEnrollResponse, error)
	Fund(ctx context.Context, userID, enrollmentID uuid.UUID, amountUSD decimal.Decimal, source, idempotencyKey string, actor entities.InvestmentActor) (*entities.InvestmentFundingTransfer, error)
	Withdraw(ctx context.Context, userID uuid.UUID, req *entities.InvestmentWithdrawalRequest, stepUpVerified bool, actor entities.InvestmentActor) (*entities.InvestmentWithdrawalResponse, error)
	// LinkVaultEnrollment records which vault a portfolio belongs to. This is the
	// join that lets ops answer "where is this user's money" in one query.
	LinkVaultEnrollment(ctx context.Context, userID, enrollmentID, vaultID uuid.UUID) error
	// GetAssetByCAIP19 resolves one catalog row by provider id. The tier
	// bootstrap uses it to prove every YAML leg names a real catalog asset; it
	// never invents addresses.
	GetAssetByCAIP19(ctx context.Context, caip19 string) (*entities.InvestmentAsset, error)
}

// Ledger is the double-entry book the vault settles through.
type Ledger interface {
	GetUserBalances(ctx context.Context, userID uuid.UUID) (*entities.UserBalances, error)
	GetOrCreateUserAccount(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (*entities.LedgerAccount, error)
	GetSystemAccount(ctx context.Context, accountType entities.AccountType) (*entities.LedgerAccount, error)
	CreateTransaction(ctx context.Context, req *entities.CreateTransactionRequest) (*entities.LedgerTransaction, error)
}

// UserReader supplies the date of birth that anchors the retirement-age leg of
// the unlock policy. It deliberately returns the date itself rather than a whole
// user, because the date is the only thing the lock depends on: a missing date
// of birth fails the vault closed rather than guessing an unlock date.
type UserReader interface {
	GetDateOfBirth(ctx context.Context, userID uuid.UUID) (*time.Time, error)
}

// Notifier delivers user-facing messages. Optional: nil is a safe no-op.
type Notifier interface {
	NotifyVaultContribution(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, unlockDate *time.Time) error
	NotifyVaultUnlocked(ctx context.Context, userID uuid.UUID, unlockDate time.Time) error
	NotifyVaultWithdrawal(ctx context.Context, userID uuid.UUID, net, penalty decimal.Decimal, early bool) error
	NotifyVaultActionRequired(ctx context.Context, userID uuid.UUID, title, body string) error
}

// Onramp is the swappable NGN→USD conversion rail. The in-flight Rail rails
// (Graph, RampHub, Paj) implement it; Yellow Card and Kotani slot in behind the
// same interface without touching the vault.
type Onramp interface {
	Quote(ctx context.Context, req OnrampQuoteRequest) (*OnrampQuote, error)
	Initiate(ctx context.Context, req OnrampTransferRequest) (*entities.VaultOnrampTransfer, error)
	Status(ctx context.Context, providerRef string) (string, error)
}

// OnrampQuoteRequest asks for a conversion price.
type OnrampQuoteRequest struct {
	FromCurrency string
	ToCurrency   string
	NGNAmount    decimal.Decimal
	Country      string
}

// OnrampQuote is a priced conversion with an expiry.
type OnrampQuote struct {
	Provider  entities.OnrampProvider
	Rate      decimal.Decimal
	FeeNGN    decimal.Decimal
	NetUSDC   decimal.Decimal
	ExpiresAt time.Time
}

// OnrampTransferRequest asks to execute a conversion.
type OnrampTransferRequest struct {
	UserID         uuid.UUID
	VaultID        uuid.UUID
	NGNAmount      decimal.Decimal
	QuoteID        string
	Destination    string
	IdempotencyKey string
}

// ---------------------------------------------------------------------------
// Errors (mapped to human, non-technical messages at the HTTP edge)
// ---------------------------------------------------------------------------

var (
	// ErrDisabled means the retirement vault is off for this deployment.
	ErrDisabled = errors.New("vault: feature disabled")
	// ErrNotFound means the vault does not exist or belongs to another user.
	ErrNotFound = errors.New("vault: not found")
	// ErrAlreadyExists means the user already has an active vault.
	ErrAlreadyExists = errors.New("vault: an active retirement plan already exists")
	// ErrValidation means the request is malformed or breaks a policy bound.
	ErrValidation = errors.New("vault: invalid request")
	// ErrAuthorizationInvalid means a withdrawal authorization was missing,
	// unknown, expired, already used, or issued for something else. The gate
	// refuses on any of these.
	ErrAuthorizationInvalid = errors.New("vault: withdrawal is not authorized")
	// ErrStepUpRequired means the withdrawal needs in-app step-up auth.
	ErrStepUpRequired = errors.New("vault: step-up authentication required")
	// ErrStrategyUnavailable means a tier has no bootstrapped strategy.
	ErrStrategyUnavailable = errors.New("vault: this plan is not available yet")
	// ErrOnrampUnavailable means the conversion provider is not configured.
	ErrOnrampUnavailable = errors.New("vault: conversion provider unavailable")
)
