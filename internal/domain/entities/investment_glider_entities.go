package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Provenance labels (spec §6): never fabricate investor activity.
// ---------------------------------------------------------------------------

// InvestmentProvenance labels how a piece of investment data was obtained.
type InvestmentProvenance string

const (
	// InvestmentProvenanceVerified is data we actually observed (provider API,
	// on-chain read, or our own execution records).
	InvestmentProvenanceVerified InvestmentProvenance = "VERIFIED"
	// InvestmentProvenanceInferred is data derived from observable behaviour.
	InvestmentProvenanceInferred InvestmentProvenance = "INFERRED"
	// InvestmentProvenanceUnavailable is data we cannot verify at all.
	InvestmentProvenanceUnavailable InvestmentProvenance = "UNAVAILABLE"
)

// ---------------------------------------------------------------------------
// Strategy lifecycle (spec §14)
// ---------------------------------------------------------------------------

// InvestmentStrategyStatus is the lifecycle state of a strategy.
type InvestmentStrategyStatus string

const (
	InvestmentStrategyDraft       InvestmentStrategyStatus = "DRAFT"
	InvestmentStrategyProposed    InvestmentStrategyStatus = "PROPOSED"
	InvestmentStrategyUserReview  InvestmentStrategyStatus = "USER_REVIEW"
	InvestmentStrategyActive      InvestmentStrategyStatus = "ACTIVE"
	InvestmentStrategyPaused      InvestmentStrategyStatus = "PAUSED"
	InvestmentStrategyRebalancing InvestmentStrategyStatus = "REBALANCING"
	InvestmentStrategyUpdated     InvestmentStrategyStatus = "UPDATED"
	InvestmentStrategyClosed      InvestmentStrategyStatus = "CLOSED"
	InvestmentStrategyRejected    InvestmentStrategyStatus = "REJECTED"
)

// InvestmentStrategyOwnerType distinguishes user-created strategies from
// Rail-curated and Glider-discovered (public) strategies.
type InvestmentStrategyOwnerType string

const (
	InvestmentOwnerUser          InvestmentStrategyOwnerType = "user"
	InvestmentOwnerRail          InvestmentStrategyOwnerType = "rail"
	InvestmentOwnerGliderPublic  InvestmentStrategyOwnerType = "glider_public"
	InvestmentOwnerUserPortfolio InvestmentStrategyOwnerType = "user_custom"
)

// InvestmentEnrollmentStatus is the state of a user's portfolio enrollment.
type InvestmentEnrollmentStatus string

const (
	InvestmentEnrollmentPendingSignature InvestmentEnrollmentStatus = "PENDING_SIGNATURE"
	InvestmentEnrollmentActive           InvestmentEnrollmentStatus = "ACTIVE"
	InvestmentEnrollmentPaused           InvestmentEnrollmentStatus = "PAUSED"
	InvestmentEnrollmentClosing          InvestmentEnrollmentStatus = "CLOSING"
	InvestmentEnrollmentClosed           InvestmentEnrollmentStatus = "CLOSED"
	InvestmentEnrollmentFailed           InvestmentEnrollmentStatus = "FAILED"
)

// ---------------------------------------------------------------------------
// Execution records (spec §17, §23)
// ---------------------------------------------------------------------------

// InvestmentExecutionKind classifies what a portfolio action actually did.
type InvestmentExecutionKind string

const (
	InvestmentExecutionInvestIn  InvestmentExecutionKind = "INVEST_IN"
	InvestmentExecutionRebalance InvestmentExecutionKind = "REBALANCE"
	InvestmentExecutionTrade     InvestmentExecutionKind = "TRADE"
	InvestmentExecutionWithdraw  InvestmentExecutionKind = "WITHDRAW"
	InvestmentExecutionLiquidate InvestmentExecutionKind = "LIQUIDATE"
)

// InvestmentExecutionStatus is the state machine of one auditable action.
type InvestmentExecutionStatus string

const (
	InvestmentExecutionRequested            InvestmentExecutionStatus = "REQUESTED"
	InvestmentExecutionValidated            InvestmentExecutionStatus = "VALIDATED"
	InvestmentExecutionAwaitingConfirmation InvestmentExecutionStatus = "AWAITING_CONFIRMATION"
	InvestmentExecutionAwaitingSignature    InvestmentExecutionStatus = "AWAITING_SIGNATURE"
	InvestmentExecutionSubmitted            InvestmentExecutionStatus = "SUBMITTED"
	InvestmentExecutionExecuting            InvestmentExecutionStatus = "EXECUTING"
	InvestmentExecutionPartiallyFilled      InvestmentExecutionStatus = "PARTIALLY_FILLED"
	InvestmentExecutionFilled               InvestmentExecutionStatus = "FILLED"
	InvestmentExecutionFailed               InvestmentExecutionStatus = "FAILED"
	InvestmentExecutionCancelled            InvestmentExecutionStatus = "CANCELLED"
)

// InvestmentActor identifies who initiated an action.
type InvestmentActor string

const (
	InvestmentActorMiriam InvestmentActor = "miriam"
	InvestmentActorUser   InvestmentActor = "user"
	InvestmentActorSystem InvestmentActor = "system"
	InvestmentActorWorker InvestmentActor = "worker"
	InvestmentActorAdmin  InvestmentActor = "admin"
)

// ---------------------------------------------------------------------------
// Compliance policy verdicts (spec §27)
// ---------------------------------------------------------------------------

// InvestmentVerdict is the deterministic policy answer to "may this happen?".
type InvestmentVerdict string

const (
	InvestmentVerdictAllowed                  InvestmentVerdict = "ALLOWED"
	InvestmentVerdictRequiresConfirmation     InvestmentVerdict = "REQUIRES_CONFIRMATION"
	InvestmentVerdictRequiresAuthentication   InvestmentVerdict = "REQUIRES_AUTHENTICATION"
	InvestmentVerdictRequiresComplianceReview InvestmentVerdict = "REQUIRES_COMPLIANCE_REVIEW"
	InvestmentVerdictNotSupported             InvestmentVerdict = "NOT_SUPPORTED"
)

// ---------------------------------------------------------------------------
// Rules and constraints
// ---------------------------------------------------------------------------

// InvestmentRebalanceRules describes when a strategy should re-target.
type InvestmentRebalanceRules struct {
	Type string `json:"type"` // threshold, scheduled, contribution, hybrid
	// ThresholdPct triggers a rebalance when a leg drifts further than this
	// from target (e.g. 0.05 = 5 percentage points).
	ThresholdPct *decimal.Decimal `json:"threshold_pct,omitempty"`
	// Frequency is the provider-side schedule: hourly, daily, weekly, monthly.
	Frequency string `json:"frequency,omitempty"`
	// PreferContributions directs new money at underweight legs before selling.
	PreferContributions bool `json:"prefer_contributions,omitempty"`
	// AllowSells permits the engine to propose sells. When false the engine
	// only ever proposes buys, so a drifted portfolio converges slowly but
	// never realises a loss to rebalance.
	AllowSells bool `json:"allow_sells,omitempty"`
}

// InvestmentContributionRules describes recurring contributions.
type InvestmentContributionRules struct {
	Type       string           `json:"type,omitempty"` // fixed, pct_income, pct_balance
	Amount     *decimal.Decimal `json:"amount,omitempty"`
	Percentage *decimal.Decimal `json:"percentage,omitempty"`
	Frequency  string           `json:"frequency,omitempty"` // daily, weekly, monthly
	DayOfWeek  *int             `json:"day_of_week,omitempty"`
	DayOfMonth *int             `json:"day_of_month,omitempty"`
	Source     string           `json:"source,omitempty"` // stash, spending, plan
	Enabled    bool             `json:"enabled,omitempty"`
}

// InvestmentExecutionRules are the provider swap constraints passed to Glider.
type InvestmentExecutionRules struct {
	SlippageBps    *int             `json:"slippage_bps,omitempty"`
	PriceImpactBps *int             `json:"price_impact_bps,omitempty"`
	ThresholdUSD   *decimal.Decimal `json:"threshold_usd,omitempty"`
}

// InvestmentPlanConstraints are the hard, non-AI-overridable limits attached
// to a strategy (spec §15).
type InvestmentPlanConstraints struct {
	MaxPositionPct   *decimal.Decimal `json:"max_position_pct,omitempty"`
	MaxStrategyPct   *decimal.Decimal `json:"max_strategy_pct,omitempty"`
	MinCashReserve   *decimal.Decimal `json:"min_cash_reserve,omitempty"`
	MaxDailyVolume   *decimal.Decimal `json:"max_daily_volume,omitempty"`
	MinOrderAmount   *decimal.Decimal `json:"min_order_amount,omitempty"`
	AllowedAssets    []string         `json:"allowed_assets,omitempty"`
	ProhibitedAssets []string         `json:"prohibited_assets,omitempty"`
}

// ---------------------------------------------------------------------------
// Core investment objects
// ---------------------------------------------------------------------------

// InvestmentAsset is one investable asset the engine is allowed to consider.
type InvestmentAsset struct {
	ID          uuid.UUID `json:"asset_id" db:"id"`
	CAIP19      string    `json:"caip19" db:"caip19"`
	Symbol      string    `json:"symbol" db:"symbol"`
	Name        string    `json:"name" db:"name"`
	AssetClass  string    `json:"asset_class" db:"asset_class"` // equity, treasury, crypto, rwa, cash
	Chain       string    `json:"chain" db:"chain"`             // solana, eip155:1, ...
	Decimals    int       `json:"decimals" db:"decimals"`
	Allowlisted bool      `json:"allowlisted" db:"allowlisted"`
	Prohibited  bool      `json:"prohibited" db:"prohibited"`
	Source      string    `json:"source" db:"source"` // glider, rail, external
	Priority    int       `json:"priority" db:"priority"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// InvestmentAllocationLeg is one target weight inside a strategy version.
// Weight is a percentage (0-100) with at most two decimal places, so an
// allocation's weights must sum to exactly 100.
type InvestmentAllocationLeg struct {
	AssetID string          `json:"asset_id,omitempty"` // Rail asset UUID
	CAIP19  string          `json:"caip19,omitempty"`   // provider-native identifier
	Symbol  string          `json:"symbol,omitempty"`
	Weight  decimal.Decimal `json:"weight"`
}

// InvestmentStrategy is the stable identity of an investment strategy. Its
// allocation lives in immutable versions.
type InvestmentStrategy struct {
	ID               uuid.UUID                   `json:"strategy_id" db:"id"`
	UserID           *uuid.UUID                  `json:"user_id,omitempty" db:"user_id"`
	OwnerType        InvestmentStrategyOwnerType `json:"owner_type" db:"owner_type"`
	GliderStrategyID *string                     `json:"glider_strategy_id,omitempty" db:"glider_strategy_id"`
	Name             string                      `json:"name" db:"name"`
	Description      string                      `json:"description,omitempty" db:"description"`
	Objective        string                      `json:"objective,omitempty" db:"objective"`
	Risk             string                      `json:"risk,omitempty" db:"risk"` // low, medium, high
	Horizon          string                      `json:"horizon,omitempty" db:"horizon"`
	Status           InvestmentStrategyStatus    `json:"status" db:"status"`
	CurrentVersion   int                         `json:"current_version" db:"current_version"`
	IsPublic         bool                        `json:"is_public" db:"is_public"`
	CreatedBy        InvestmentActor             `json:"created_by" db:"created_by"`
	CreatedAt        time.Time                   `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time                   `json:"updated_at" db:"updated_at"`
	ClosedAt         *time.Time                  `json:"closed_at,omitempty" db:"closed_at"`
}

// InvestmentStrategyVersion is one immutable allocation revision. Strategies
// are never silently mutated: every change writes a new version (spec §14).
type InvestmentStrategyVersion struct {
	ID                    uuid.UUID                   `json:"version_id" db:"id"`
	StrategyID            uuid.UUID                   `json:"strategy_id" db:"strategy_id"`
	Version               int                         `json:"version" db:"version"`
	TargetAllocation      []InvestmentAllocationLeg   `json:"target_allocation"`
	Risk                  string                      `json:"risk,omitempty" db:"risk"`
	Horizon               string                      `json:"horizon,omitempty" db:"horizon"`
	RebalanceRules        InvestmentRebalanceRules    `json:"rebalance_rules"`
	ContributionRules     InvestmentContributionRules `json:"contribution_rules"`
	ExecutionRules        InvestmentExecutionRules    `json:"execution_rules"`
	Constraints           InvestmentPlanConstraints   `json:"constraints"`
	Rationale             string                      `json:"rationale,omitempty" db:"rationale"`
	ValidationReport      *InvestmentValidationReport `json:"validation_report,omitempty"`
	GliderStrategyVersion *int                        `json:"glider_strategy_version,omitempty" db:"glider_strategy_version"`
	CreatedBy             InvestmentActor             `json:"created_by" db:"created_by"`
	CreatedAt             time.Time                   `json:"created_at" db:"created_at"`
}

// InvestmentValidationReport is the deterministic verdict on a proposed
// allocation. The AI cannot override a report with Valid=false.
type InvestmentValidationReport struct {
	Valid                bool                        `json:"valid"`
	Violations           []InvestmentValidationIssue `json:"violations,omitempty"`
	NormalizedAllocation []InvestmentAllocationLeg   `json:"normalized_allocation,omitempty"`
	Notes                []string                    `json:"notes,omitempty"`
	CheckedAt            time.Time                   `json:"checked_at"`
}

// InvestmentValidationIssue is one reason a proposal was rejected.
type InvestmentValidationIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	AssetID string `json:"asset_id,omitempty"`
}

// InvestmentLimits are the user's effective investment limits.
type InvestmentLimits struct {
	MaxPositionPct    decimal.Decimal `json:"max_position_pct"`
	MaxStrategyPct    decimal.Decimal `json:"max_strategy_pct"`
	MaxTransactionUSD decimal.Decimal `json:"max_transaction_usd"`
	MaxDailyVolumeUSD decimal.Decimal `json:"max_daily_volume_usd"`
	MinCashReserveUSD decimal.Decimal `json:"min_cash_reserve_usd"`
	MinOrderAmountUSD decimal.Decimal `json:"min_order_amount_usd"`
	MaxEnrollments    int             `json:"max_enrollments"`
	DailyVolumeUSD    decimal.Decimal `json:"daily_volume_usd"`
}

// InvestmentPolicyDecision is the compliance layer's answer (spec §27).
type InvestmentPolicyDecision struct {
	Verdict     InvestmentVerdict `json:"verdict"`
	Reasons     []string          `json:"reasons,omitempty"`
	Disclosures []string          `json:"disclosures,omitempty"`
	EvaluatedAt time.Time         `json:"evaluated_at"`
}

// ---------------------------------------------------------------------------
// Preview (spec §8, §12): everything is calculated before anything executes.
// ---------------------------------------------------------------------------

// InvestmentProposedTrade is one estimated leg of a rebalance.
type InvestmentProposedTrade struct {
	AssetID      string          `json:"asset_id,omitempty"`
	Symbol       string          `json:"symbol,omitempty"`
	Action       string          `json:"action"` // buy, sell
	EstAmountUSD decimal.Decimal `json:"est_amount_usd"`
	EstUnits     decimal.Decimal `json:"est_units,omitempty"`
	Reason       string          `json:"reason,omitempty"`
}

// InvestmentAllocationPreview is the deterministic pre-trade picture.
type InvestmentAllocationPreview struct {
	StrategyID                  uuid.UUID                 `json:"strategy_id"`
	Version                     int                       `json:"version"`
	CurrentAllocation           []InvestmentAllocationLeg `json:"current_allocation"`
	TargetAllocation            []InvestmentAllocationLeg `json:"target_allocation"`
	DriftPct                    decimal.Decimal           `json:"drift"` // max |current - target|
	ProposedTrades              []InvestmentProposedTrade `json:"proposed_trades"`
	EstFeesUSD                  decimal.Decimal           `json:"est_fees_usd"`
	EstSlippageBps              int                       `json:"est_slippage_bps"`
	ExpectedPostTradeAllocation []InvestmentAllocationLeg `json:"expected_post_trade_allocation"`
	ContributionOnly            bool                      `json:"contribution_only"`
	Estimated                   bool                      `json:"estimated"` // always true: this is not provider output
	Assumptions                 []string                  `json:"assumptions,omitempty"`
	MarketDataAsOf              time.Time                 `json:"market_data_as_of"`
	Verdict                     InvestmentVerdict         `json:"verdict"`
	PolicyReasons               []string                  `json:"policy_reasons,omitempty"`
	PolicyDisclosures           []string                  `json:"policy_disclosures,omitempty"`
}

// ---------------------------------------------------------------------------
// Enrollment, holdings, execution, funding
// ---------------------------------------------------------------------------

// InvestmentEnrollment is one user portfolio mirroring one strategy.
type InvestmentEnrollment struct {
	ID                uuid.UUID                  `json:"enrollment_id" db:"id"`
	UserID            uuid.UUID                  `json:"user_id" db:"user_id"`
	StrategyID        uuid.UUID                  `json:"strategy_id" db:"strategy_id"`
	StrategyVersion   int                        `json:"strategy_version" db:"strategy_version"`
	GliderPortfolioID string                     `json:"glider_portfolio_id" db:"glider_portfolio_id"`
	GliderStrategyID  string                     `json:"glider_strategy_id" db:"glider_strategy_id"`
	Chain             string                     `json:"chain" db:"chain"`
	OwnerAccountID    string                     `json:"owner_account_id,omitempty" db:"owner_account_id"`
	AgentAccountID    string                     `json:"agent_account_id,omitempty" db:"agent_account_id"`
	DepositAccountID  string                     `json:"deposit_account_id,omitempty" db:"deposit_account_id"`
	SwigRoleID        *int                       `json:"swig_role_id,omitempty" db:"swig_role_id"`
	Status            InvestmentEnrollmentStatus `json:"status" db:"status"`
	AutomationStatus  string                     `json:"automation_status,omitempty" db:"automation_status"`
	NextDueAt         *time.Time                 `json:"next_due_at,omitempty" db:"next_due_at"`
	LastRebalanceAt   *time.Time                 `json:"last_rebalance_at,omitempty" db:"last_rebalance_at"`
	TotalValueUSD     decimal.Decimal            `json:"total_value_usd" db:"total_value_usd"`
	PositionsAsOf     *time.Time                 `json:"positions_as_of,omitempty" db:"positions_as_of"`
	LastSyncError     string                     `json:"last_sync_error,omitempty" db:"last_sync_error"`
	CreatedAt         time.Time                  `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time                  `json:"updated_at" db:"updated_at"`
	ClosedAt          *time.Time                 `json:"closed_at,omitempty" db:"closed_at"`
}

// InvestmentHolding is one normalized position inside an enrollment. Provider
// and chain state are authoritative; this row is the read model.
type InvestmentHolding struct {
	ID           uuid.UUID       `json:"id" db:"id"`
	UserID       uuid.UUID       `json:"user_id" db:"user_id"`
	EnrollmentID uuid.UUID       `json:"enrollment_id" db:"enrollment_id"`
	AssetID      *uuid.UUID      `json:"asset_id,omitempty" db:"asset_id"`
	CAIP19       string          `json:"caip19" db:"caip19"`
	Symbol       string          `json:"symbol" db:"symbol"`
	Name         string          `json:"name,omitempty" db:"name"`
	Balance      decimal.Decimal `json:"balance" db:"balance"`
	BalanceRaw   string          `json:"balance_raw,omitempty" db:"balance_raw"`
	Decimals     int             `json:"decimals" db:"decimals"`
	PriceUSD     decimal.Decimal `json:"price_usd" db:"price_usd"`
	ValueUSD     decimal.Decimal `json:"value_usd" db:"value_usd"`
	WeightPct    decimal.Decimal `json:"weight" db:"weight_pct"`
	Source       string          `json:"source" db:"source"`
	AsOf         time.Time       `json:"as_of" db:"as_of"`
	UpdatedAt    time.Time       `json:"updated_at" db:"updated_at"`
}

// InvestmentExecution is the audit record for one portfolio action. Provider
// transaction state is authoritative; this record mirrors it.
type InvestmentExecution struct {
	ID                  uuid.UUID                 `json:"order_id" db:"id"`
	UserID              uuid.UUID                 `json:"user_id" db:"user_id"`
	EnrollmentID        *uuid.UUID                `json:"enrollment_id,omitempty" db:"enrollment_id"`
	StrategyID          *uuid.UUID                `json:"strategy_id,omitempty" db:"strategy_id"`
	StrategyVersion     *int                      `json:"strategy_version,omitempty" db:"strategy_version"`
	Kind                InvestmentExecutionKind   `json:"kind" db:"kind"`
	Side                string                    `json:"side,omitempty" db:"side"`
	AssetID             *uuid.UUID                `json:"asset_id,omitempty" db:"asset_id"`
	Symbol              string                    `json:"symbol,omitempty" db:"symbol"`
	RequestedAmountUSD  decimal.Decimal           `json:"requested_amount_usd" db:"requested_amount_usd"`
	ValidatedAmountUSD  decimal.Decimal           `json:"validated_amount_usd" db:"validated_amount_usd"`
	Status              InvestmentExecutionStatus `json:"status" db:"status"`
	IdempotencyKey      string                    `json:"idempotency_key,omitempty" db:"idempotency_key"`
	Policy              *InvestmentPolicyDecision `json:"policy,omitempty"`
	Provider            string                    `json:"provider" db:"provider"`
	ProviderOperationID string                    `json:"provider_operation_id,omitempty" db:"provider_operation_id"`
	ProviderTxRefs      []string                  `json:"provider_tx_refs,omitempty"`
	FailureCode         string                    `json:"failure_code,omitempty" db:"failure_code"`
	FailureReason       string                    `json:"failure_reason,omitempty" db:"failure_reason"`
	MarketDataAsOf      *time.Time                `json:"market_data_as_of,omitempty" db:"market_data_as_of"`
	RequestedBy         InvestmentActor           `json:"requested_by" db:"requested_by"`
	ConfirmationMethod  string                    `json:"confirmation_method,omitempty" db:"confirmation_method"`
	CreatedAt           time.Time                 `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time                 `json:"updated_at" db:"updated_at"`
	CompletedAt         *time.Time                `json:"completed_at,omitempty" db:"completed_at"`
}

// InvestmentFundingTransfer is the ledger + on-chain leg that moves USDC into
// or out of a portfolio's deposit account. The destination is always derived
// from the stored enrollment, never from caller input.
type InvestmentFundingTransfer struct {
	ID                   uuid.UUID       `json:"transfer_id" db:"id"`
	UserID               uuid.UUID       `json:"user_id" db:"user_id"`
	EnrollmentID         uuid.UUID       `json:"enrollment_id" db:"enrollment_id"`
	Direction            string          `json:"direction" db:"direction"` // deposit, withdrawal
	AmountUSD            decimal.Decimal `json:"amount_usd" db:"amount_usd"`
	Asset                string          `json:"asset" db:"asset"`
	SourceAccount        string          `json:"source_account,omitempty" db:"source_account"`
	DestinationAccountID string          `json:"destination_account_id" db:"destination_account_id"`
	LedgerTransactionID  *uuid.UUID      `json:"ledger_transaction_id,omitempty" db:"ledger_transaction_id"`
	OnchainTxRef         string          `json:"onchain_tx_ref,omitempty" db:"onchain_tx_ref"`
	Status               string          `json:"status" db:"status"`
	IdempotencyKey       string          `json:"idempotency_key,omitempty" db:"idempotency_key"`
	FailureReason        string          `json:"failure_reason,omitempty" db:"failure_reason"`
	CreatedAt            time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at" db:"updated_at"`
}

// InvestmentSignatureRequest records a two-stage owner authorization. It is
// the replay-protection anchor for enrollment and withdrawal flows.
type InvestmentSignatureRequest struct {
	ID             uuid.UUID  `json:"signature_request_id" db:"id"`
	UserID         uuid.UUID  `json:"user_id" db:"user_id"`
	EnrollmentID   *uuid.UUID `json:"enrollment_id,omitempty" db:"enrollment_id"`
	Flow           string     `json:"flow" db:"flow"` // enroll, withdraw, liquidate, activate_chains
	ProviderFlowID string     `json:"provider_flow_id,omitempty" db:"provider_flow_id"`
	Payload        []byte     `json:"-" db:"payload"`
	Status         string     `json:"status" db:"status"` // prepared, signed, submitted, expired, failed
	ExpiresAt      *time.Time `json:"expires_at,omitempty" db:"expires_at"`
	FailureReason  string     `json:"failure_reason,omitempty" db:"failure_reason"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`
}

// InvestmentConfirmation binds a validated payload to an explicit user
// confirmation, so a natural-language "yes" can never authorise a payload the
// user did not actually see.
type InvestmentConfirmation struct {
	ID         uuid.UUID         `json:"confirmation_id" db:"id"`
	UserID     uuid.UUID         `json:"user_id" db:"user_id"`
	Token      string            `json:"token" db:"token"`
	Action     string            `json:"action" db:"action"`
	ActionHash string            `json:"-" db:"action_hash"`
	Payload    []byte            `json:"-" db:"payload"`
	Verdict    InvestmentVerdict `json:"verdict" db:"verdict"`
	Preview    []byte            `json:"-" db:"preview"`
	ConsumedAt *time.Time        `json:"consumed_at,omitempty" db:"consumed_at"`
	ExpiresAt  time.Time         `json:"expires_at" db:"expires_at"`
	CreatedAt  time.Time         `json:"created_at" db:"created_at"`
}

// GliderOperation mirrors an async provider operation for polling and audit.
type GliderOperation struct {
	ID                  uuid.UUID  `json:"id" db:"id"`
	UserID              uuid.UUID  `json:"user_id" db:"user_id"`
	EnrollmentID        *uuid.UUID `json:"enrollment_id,omitempty" db:"enrollment_id"`
	ExecutionID         *uuid.UUID `json:"execution_id,omitempty" db:"execution_id"`
	ProviderOperationID string     `json:"provider_operation_id" db:"provider_operation_id"`
	Kind                string     `json:"kind" db:"kind"`
	State               string     `json:"state" db:"state"`
	Error               string     `json:"error,omitempty" db:"error"`
	FinishedAt          *time.Time `json:"finished_at,omitempty" db:"finished_at"`
	CreatedAt           time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at" db:"updated_at"`
}

// InvestmentAuditEvent is the analytics/audit trail for the investment
// lifecycle (spec §22, §23).
type InvestmentAuditEvent struct {
	ID              uuid.UUID       `json:"id" db:"id"`
	UserID          uuid.UUID       `json:"user_id" db:"user_id"`
	EventType       string          `json:"event_type" db:"event_type"`
	Actor           InvestmentActor `json:"actor" db:"actor"`
	StrategyID      *uuid.UUID      `json:"strategy_id,omitempty" db:"strategy_id"`
	StrategyVersion *int            `json:"strategy_version,omitempty" db:"strategy_version"`
	EnrollmentID    *uuid.UUID      `json:"enrollment_id,omitempty" db:"enrollment_id"`
	ExecutionID     *uuid.UUID      `json:"order_id,omitempty" db:"execution_id"`
	Payload         []byte          `json:"-" db:"payload"`
	CorrelationID   string          `json:"correlation_id,omitempty" db:"correlation_id"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
}

// ---------------------------------------------------------------------------
// Discovery (spec §6, §11)
// ---------------------------------------------------------------------------

// InvestmentInvestorProvenance labels each field group of a discovered
// strategy, so Miriam can never imply verified data it does not have.
type InvestmentInvestorProvenance struct {
	Allocation  InvestmentProvenance `json:"allocation"`
	Performance InvestmentProvenance `json:"performance"`
	Fees        InvestmentProvenance `json:"fees"`
	Trades      InvestmentProvenance `json:"trades"`
	Holders     InvestmentProvenance `json:"holders"`
	Methodology InvestmentProvenance `json:"methodology"`
}

// InvestmentPerformanceWindow is one provider performance lookback.
type InvestmentPerformanceWindow struct {
	Window        string          `json:"window"`
	PercentChange decimal.Decimal `json:"percent_change"`
	Since         string          `json:"since,omitempty"`
}

// InvestmentInvestorMetrics are the observable metrics of a public strategy.
type InvestmentInvestorMetrics struct {
	TVLUSD          *decimal.Decimal              `json:"tvl_usd,omitempty"`
	PortfolioCount  *int                          `json:"portfolio_count,omitempty"`
	MaxAPY          *decimal.Decimal              `json:"max_apy,omitempty"`
	Performance     []InvestmentPerformanceWindow `json:"performance,omitempty"`
	PerformanceNote string                        `json:"performance_note,omitempty"`
}

// InvestmentInvestor is a mirrorable public strategy exposed as an "investor"
// profile. Rail only ever shows what the provider actually publishes.
type InvestmentInvestor struct {
	InvestorID  string                       `json:"investor_id"`
	Name        string                       `json:"name"`
	Description string                       `json:"description,omitempty"`
	CanMirror   bool                         `json:"can_mirror"`
	Allocation  []InvestmentAllocationLeg    `json:"allocation"`
	Metrics     InvestmentInvestorMetrics    `json:"metrics"`
	Provenance  InvestmentInvestorProvenance `json:"provenance"`
	CreatedAt   *time.Time                   `json:"created_at,omitempty"`
	Source      string                       `json:"source"`
}

// ---------------------------------------------------------------------------
// Read models
// ---------------------------------------------------------------------------

// InvestmentEnrollmentSummary is a compact enrollment view.
type InvestmentEnrollmentSummary struct {
	EnrollmentID    uuid.UUID                  `json:"enrollment_id"`
	StrategyID      uuid.UUID                  `json:"strategy_id"`
	StrategyName    string                     `json:"strategy_name"`
	Status          InvestmentEnrollmentStatus `json:"status"`
	TotalValueUSD   decimal.Decimal            `json:"total_value_usd"`
	Chain           string                     `json:"chain"`
	NextDueAt       *time.Time                 `json:"next_due_at,omitempty"`
	LastRebalanceAt *time.Time                 `json:"last_rebalance_at,omitempty"`
}

// InvestmentPortfolioSummary is the whole-portfolio read model. Miriam reads
// this instead of computing portfolio state from memory (spec §10).
type InvestmentPortfolioSummary struct {
	TotalValueUSD    decimal.Decimal               `json:"total_value_usd"`
	InvestedValueUSD decimal.Decimal               `json:"invested_value_usd"`
	CashValueUSD     decimal.Decimal               `json:"cash_value_usd"`
	UnrealizedPnLUSD decimal.Decimal               `json:"unrealized_pnl_usd"`
	Enrollments      []InvestmentEnrollmentSummary `json:"enrollments"`
	Positions        []InvestmentHolding           `json:"positions"`
	AsOf             time.Time                     `json:"as_of"`
	Source           string                        `json:"source"`
	Stale            bool                          `json:"stale"`
}
