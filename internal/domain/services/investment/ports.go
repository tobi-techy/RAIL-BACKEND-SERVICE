// Package investment is Rail's deterministic investment infrastructure.
//
// The split it enforces (spec §29): the AI agent decides what should happen and
// asks; this package decides whether it is allowed and then executes. Nothing
// here trusts caller-supplied provider parameters, and every financial action
// is validated, policy-checked, explicitly confirmed, recorded and idempotent.
package investment

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

// Config is the investment engine's configuration. Policy-relevant values live
// here (or in per-user limits) so compliance rules never live in a prompt.
type Config struct {
	Enabled      bool
	Simulation   bool
	DefaultChain string
	// SolanaChainIDs are the numeric chain ids sent to the provider for Solana
	// enrollment; the provider contract expects a subset of its configured
	// chains, and SVM uses a compatibility placeholder.
	SolanaChainIDs []int
	// OwnerAccountPrefix is the CAIP-2/CAIP-10 prefix for owner accounts, e.g.
	// "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp".
	OwnerAccountPrefix string
	// SettlementSymbol is the cash leg symbol (USDC).
	SettlementSymbol string

	DefaultSlippageBps int
	DefaultFeeBps      decimal.Decimal

	HighValueThresholdUSD decimal.Decimal
	ConfirmationTTL       time.Duration

	// AllowedCountries is an allowlist of ISO-3166 alpha-2 codes; empty means
	// no jurisdiction restriction is enforced by this layer.
	AllowedCountries []string

	MinAllocationLegs int
	MaxAllocationLegs int

	StaleMarketDataAfter time.Duration

	DefaultLimits Limits
}

// Limits are the fallback per-user investment limits.
type Limits struct {
	MaxPositionPct    decimal.Decimal
	MaxStrategyPct    decimal.Decimal
	MaxTransactionUSD decimal.Decimal
	MaxDailyVolumeUSD decimal.Decimal
	MinCashReserveUSD decimal.Decimal
	MinOrderAmountUSD decimal.Decimal
	MaxEnrollments    int
}

// ---------------------------------------------------------------------------
// Provider port (implemented by infrastructure/adapters/glider)
// ---------------------------------------------------------------------------

// Provider is the investment execution venue. Only this package may call it,
// and only with parameters derived from stored state.
type Provider interface {
	Whoami(ctx context.Context) (*entities.GliderIdentity, error)
	CreateStrategy(ctx context.Context, in entities.GliderStrategyInput) (*entities.GliderStrategy, error)
	PublishStrategyVersion(ctx context.Context, strategyID string, in entities.GliderStrategyInput) (*entities.GliderStrategy, error)
	GetStrategy(ctx context.Context, strategyID string) (*entities.GliderStrategy, error)
	SetStrategySchedule(ctx context.Context, strategyID, frequency string) error
	DiscoverStrategies(ctx context.Context, collection, cursor string, limit int) ([]entities.GliderDiscoveredStrategy, string, error)

	PrepareEnrollment(ctx context.Context, in entities.GliderEnrollSignatureInput) (*entities.GliderEnrollAuthorization, error)
	SubmitEnrollment(ctx context.Context, in entities.GliderEnrollSubmitInput) (*entities.GliderPortfolio, error)
	GetPortfolio(ctx context.Context, portfolioID string) (*entities.GliderPortfolio, error)
	ListPortfolios(ctx context.Context) ([]entities.GliderPortfolio, error)
	GetPositions(ctx context.Context, portfolioID string) (*entities.GliderPositions, error)
	StartPortfolio(ctx context.Context, portfolioID string) error
	StopPortfolio(ctx context.Context, portfolioID string) error
	TriggerRebalance(ctx context.Context, portfolioID string) (*entities.GliderOperationHandle, error)
	GetOperation(ctx context.Context, portfolioID, operationID string) (*entities.GliderOperationState, error)

	PrepareWithdrawal(ctx context.Context, portfolioID string, in entities.GliderWithdrawSignatureInput) (*entities.GliderWithdrawAuthorization, error)
	SubmitWithdrawal(ctx context.Context, portfolioID string, in entities.GliderWithdrawSubmitInput) (*entities.GliderOperationHandle, error)
}

// OwnerSigner is the portfolio owner authority. The plan's decision is
// "Solana, Rail-signed": Rail holds a per-user owner key so enrollment and
// withdrawal can be completed from a messaging channel after an explicit user
// consent step, instead of requiring the user to sign inside a wallet.
//
// This makes Rail the root authority over the user's smart accounts even though
// the assets sit in the user's own accounts. That posture is a deliberate,
// documented product/compliance decision, and it is isolated behind this port
// so it can be replaced by user-held keys later.
type OwnerSigner interface {
	// OwnerAccount returns the chain-bound owner account used at enrollment.
	OwnerAccount(ctx context.Context, userID uuid.UUID) (string, error)
	// SignSolanaMessage signs an ed25519 message (withdrawals).
	SignSolanaMessage(ctx context.Context, userID uuid.UUID, message string) (string, error)
	// SignSolanaTransaction signs an unsigned transaction (enrollment).
	SignSolanaTransaction(ctx context.Context, userID uuid.UUID, unsignedTx string) (string, error)
}

// ---------------------------------------------------------------------------
// Funding port + repositories
// ---------------------------------------------------------------------------

// FundingRequest asks to move USDC from a Rail account into a portfolio's
// deposit account. Destination is derived from the stored enrollment.
type FundingRequest struct {
	UserID         uuid.UUID
	EnrollmentID   uuid.UUID
	AmountUSD      decimal.Decimal
	Source         string // stash, spending
	Destination    string // enrollment.DepositAccountID
	IdempotencyKey string
}

// FundingResult reports the ledger and on-chain legs.
type FundingResult struct {
	TransferID          uuid.UUID
	LedgerTransactionID *uuid.UUID
	OnchainTxRef        string
	Status              string
}

// FundingPort moves money into and out of portfolios.
type FundingPort interface {
	// Available reports how much could be invested from a source today.
	Available(ctx context.Context, userID uuid.UUID, source string) (decimal.Decimal, error)
	// TransferToPortfolio debits the user's ledger balance and delivers USDC to
	// the portfolio deposit account. It must be idempotent on the key.
	TransferToPortfolio(ctx context.Context, in FundingRequest) (*FundingResult, error)
	// RecipientAccount returns the user's Rail-controlled settlement address
	// (CAIP-10) that withdrawal proceeds are sent to.
	RecipientAccount(ctx context.Context, userID uuid.UUID) (string, error)
}

// StrategyRepository persists strategy identities and their versions.
type StrategyRepository interface {
	Create(ctx context.Context, strategy *entities.InvestmentStrategy) error
	Update(ctx context.Context, strategy *entities.InvestmentStrategy) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.InvestmentStrategy, error)
	ListByUser(ctx context.Context, userID uuid.UUID, status string) ([]*entities.InvestmentStrategy, error)
	ListByOwnerType(ctx context.Context, ownerType entities.InvestmentStrategyOwnerType, limit int) ([]*entities.InvestmentStrategy, error)
	FindByGliderID(ctx context.Context, gliderStrategyID string) (*entities.InvestmentStrategy, error)
	CreateVersion(ctx context.Context, version *entities.InvestmentStrategyVersion) error
	GetVersion(ctx context.Context, strategyID uuid.UUID, version int) (*entities.InvestmentStrategyVersion, error)
	ListVersions(ctx context.Context, strategyID uuid.UUID) ([]*entities.InvestmentStrategyVersion, error)
}

// AssetRepository resolves and lists investable assets.
type AssetRepository interface {
	Upsert(ctx context.Context, asset *entities.InvestmentAsset) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.InvestmentAsset, error)
	GetByCAIP19(ctx context.Context, caip19 string) (*entities.InvestmentAsset, error)
	GetBySymbol(ctx context.Context, symbol string) (*entities.InvestmentAsset, error)
	List(ctx context.Context, query string, limit int) ([]*entities.InvestmentAsset, error)
	CountAllowed(ctx context.Context) (int, error)
}

// EnrollmentRepository persists user portfolios.
type EnrollmentRepository interface {
	Create(ctx context.Context, enrollment *entities.InvestmentEnrollment) error
	Update(ctx context.Context, enrollment *entities.InvestmentEnrollment) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.InvestmentEnrollment, error)
	GetByUserAndStrategy(ctx context.Context, userID, strategyID uuid.UUID) (*entities.InvestmentEnrollment, error)
	GetByPortfolioID(ctx context.Context, portfolioID string) (*entities.InvestmentEnrollment, error)
	ListByUser(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentEnrollment, error)
	ListActive(ctx context.Context, limit int) ([]*entities.InvestmentEnrollment, error)
}

// HoldingRepository persists the normalized position read model.
type HoldingRepository interface {
	ReplaceForEnrollment(ctx context.Context, enrollmentID uuid.UUID, holdings []*entities.InvestmentHolding) error
	ListByUser(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentHolding, error)
	ListByEnrollment(ctx context.Context, enrollmentID uuid.UUID) ([]*entities.InvestmentHolding, error)
}

// ExecutionRepository persists auditable portfolio actions.
type ExecutionRepository interface {
	Create(ctx context.Context, execution *entities.InvestmentExecution) error
	Update(ctx context.Context, execution *entities.InvestmentExecution) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.InvestmentExecution, error)
	FindByIdempotencyKey(ctx context.Context, key string) (*entities.InvestmentExecution, error)
	ListByUser(ctx context.Context, userID uuid.UUID, status string, limit int) ([]*entities.InvestmentExecution, error)
}

// FundingTransferRepository persists funding legs.
type FundingTransferRepository interface {
	Create(ctx context.Context, transfer *entities.InvestmentFundingTransfer) error
	Update(ctx context.Context, transfer *entities.InvestmentFundingTransfer) error
	FindByIdempotencyKey(ctx context.Context, key string) (*entities.InvestmentFundingTransfer, error)
	ListByEnrollment(ctx context.Context, enrollmentID uuid.UUID) ([]*entities.InvestmentFundingTransfer, error)
	SumDepositsSince(ctx context.Context, userID uuid.UUID, since time.Time) (decimal.Decimal, error)
}

// SignatureRequestRepository records two-stage owner authorizations.
type SignatureRequestRepository interface {
	Create(ctx context.Context, request *entities.InvestmentSignatureRequest) error
	Update(ctx context.Context, request *entities.InvestmentSignatureRequest) error
	FindByFlow(ctx context.Context, flow, providerFlowID string) (*entities.InvestmentSignatureRequest, error)
}

// ConfirmationRepository persists explicit user confirmations.
type ConfirmationRepository interface {
	Create(ctx context.Context, confirmation *entities.InvestmentConfirmation) error
	GetByToken(ctx context.Context, token string) (*entities.InvestmentConfirmation, error)
	Consume(ctx context.Context, token string, now time.Time) error
	// FindPending returns the newest unconsumed, unexpired confirmation for the
	// same user/action/payload so a retried identical proposal reuses one token
	// instead of minting a second one that could also be confirmed.
	FindPending(ctx context.Context, userID uuid.UUID, action, actionHash string) (*entities.InvestmentConfirmation, error)
}

// OperationRepository mirrors provider async operations.
type OperationRepository interface {
	Upsert(ctx context.Context, operation *entities.GliderOperation) error
	ListOpen(ctx context.Context, limit int) ([]*entities.GliderOperation, error)
	GetByProviderID(ctx context.Context, providerOperationID string) (*entities.GliderOperation, error)
}

// AuditRepository records the investment event trail.
type AuditRepository interface {
	Record(ctx context.Context, event *entities.InvestmentAuditEvent) error
	ListByUser(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.InvestmentAuditEvent, error)
}

// LimitsRepository stores per-user limit overrides.
type LimitsRepository interface {
	Get(ctx context.Context, userID uuid.UUID) (*entities.InvestmentLimits, error)
	Upsert(ctx context.Context, userID uuid.UUID, limits *entities.InvestmentLimits) error
}

// UserProfile is the slice of user state the policy layer needs.
type UserProfile struct {
	UserID    uuid.UUID
	KYCTier   string
	KYCStatus string
	Country   string
}

// UserProfileReader supplies policy inputs from the user record.
type UserProfileReader interface {
	GetInvestmentProfile(ctx context.Context, userID uuid.UUID) (*UserProfile, error)
}

// AssetResolver resolves an allocation leg to a catalog asset.
type AssetResolver interface {
	Resolve(ctx context.Context, assetID, caip19, symbol string) (*entities.InvestmentAsset, error)
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

var (
	// ErrDisabled means the investment feature is off for this deployment.
	ErrDisabled = errors.New("investment: feature disabled")
	// ErrConfirmationInvalid means a confirmation token was missing, expired,
	// already used, or issued for a different payload.
	ErrConfirmationInvalid = errors.New("investment: confirmation invalid")
	// ErrNotFound means the resource does not exist or belongs to another user.
	ErrNotFound = errors.New("investment: not found")
	// ErrValidationFailed means the deterministic validator rejected the request.
	ErrValidationFailed = errors.New("investment: validation failed")
	// ErrPolicyBlocked means the compliance layer refused the action.
	ErrPolicyBlocked = errors.New("investment: policy blocked")
	// ErrProviderConflict means the provider reported an in-flight duplicate.
	ErrProviderConflict = errors.New("investment: provider conflict")
	// ErrProviderCooldown means the provider rate-limited the action.
	ErrProviderCooldown = errors.New("investment: provider cooldown")
)

// nowOr returns the service clock's time.
func (s *Service) nowOr() time.Time {
	if s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock().UTC()
}
