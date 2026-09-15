package entities

import (
	"time"

	"github.com/shopspring/decimal"
)

// InvestmentConfirmationToken is the header a caller uses to replay a
// confirmed action. The token binds to the exact payload hash it was issued
// for, so a changed payload forces a fresh confirmation.
const InvestmentConfirmationHeader = "X-Investment-Confirmation"

// InvestmentCreateStrategyRequest is Miriam's strategy proposal. Every field
// is validated deterministically before anything is persisted.
type InvestmentCreateStrategyRequest struct {
	Name              string                      `json:"name"`
	Description       string                      `json:"description,omitempty"`
	Objective         string                      `json:"objective,omitempty"`
	Risk              string                      `json:"risk,omitempty"`
	Horizon           string                      `json:"horizon,omitempty"`
	TargetAllocation  []InvestmentAllocationLeg   `json:"target_allocation"`
	RebalanceRules    InvestmentRebalanceRules    `json:"rebalance_rules,omitempty"`
	ContributionRules InvestmentContributionRules `json:"contribution_rules,omitempty"`
	ExecutionRules    InvestmentExecutionRules    `json:"execution_rules,omitempty"`
	Rationale         string                      `json:"rationale,omitempty"`
	ConfirmationToken string                      `json:"confirmation_token,omitempty"`
}

// InvestmentUpdateStrategyRequest publishes a new immutable version.
type InvestmentUpdateStrategyRequest struct {
	TargetAllocation  []InvestmentAllocationLeg    `json:"target_allocation"`
	Rationale         string                       `json:"rationale,omitempty"`
	RebalanceRules    *InvestmentRebalanceRules    `json:"rebalance_rules,omitempty"`
	ContributionRules *InvestmentContributionRules `json:"contribution_rules,omitempty"`
	ExecutionRules    *InvestmentExecutionRules    `json:"execution_rules,omitempty"`
	ConfirmationToken string                       `json:"confirmation_token,omitempty"`
}

// InvestmentEnrollRequest asks to enroll the user into a strategy and fund it.
type InvestmentEnrollRequest struct {
	StrategyID        string          `json:"strategy_id"`
	AmountUSD         decimal.Decimal `json:"amount_usd,omitempty"`
	Source            string          `json:"source,omitempty"` // stash, spending
	IdempotencyKey    string          `json:"idempotency_key,omitempty"`
	ConfirmationToken string          `json:"confirmation_token,omitempty"`
}

// InvestmentOrderRequest is a single-asset allocation change. Glider has no
// order API: a buy raises the asset's target weight, a sell lowers it, and the
// provider re-targets on its next rebalance (or a manual one).
type InvestmentOrderRequest struct {
	StrategyID        string          `json:"strategy_id,omitempty"`
	AssetID           string          `json:"asset_id,omitempty"`
	Symbol            string          `json:"symbol,omitempty"`
	Side              string          `json:"side"` // buy, sell
	AmountUSD         decimal.Decimal `json:"amount_usd"`
	IdempotencyKey    string          `json:"idempotency_key,omitempty"`
	ConfirmationToken string          `json:"confirmation_token,omitempty"`
}

// InvestmentMultiOrderRequest sets several target weights at once.
type InvestmentMultiOrderRequest struct {
	StrategyID        string                    `json:"strategy_id,omitempty"`
	Targets           []InvestmentAllocationLeg `json:"targets"`
	Rationale         string                    `json:"rationale,omitempty"`
	IdempotencyKey    string                    `json:"idempotency_key,omitempty"`
	ConfirmationToken string                    `json:"confirmation_token,omitempty"`
}

// InvestmentWithdrawalRequest takes money out of a portfolio. Withdrawals can
// never be authorised from a chat message: the handler requires an interactive
// session plus step-up authentication before this is called.
type InvestmentWithdrawalRequest struct {
	StrategyID        string          `json:"strategy_id,omitempty"`
	AssetID           string          `json:"asset_id,omitempty"`
	Symbol            string          `json:"symbol,omitempty"`
	AmountUSD         decimal.Decimal `json:"amount_usd,omitempty"`
	LiquidateAll      bool            `json:"liquidate_all,omitempty"`
	IdempotencyKey    string          `json:"idempotency_key,omitempty"`
	ConfirmationToken string          `json:"confirmation_token,omitempty"`
}

// InvestmentWithdrawalResponse is returned by the withdrawal flow. It always
// carries the policy verdict so the caller can explain why it could not
// proceed from a messaging channel.
type InvestmentWithdrawalResponse struct {
	Status       InvestmentActionStatus       `json:"status"`
	Policy       *InvestmentPolicyDecision    `json:"policy,omitempty"`
	Execution    *InvestmentExecution         `json:"execution,omitempty"`
	Preview      *InvestmentAllocationPreview `json:"preview,omitempty"`
	Recipient    string                       `json:"recipient,omitempty"`
	OperationID  string                       `json:"operation_id,omitempty"`
	Confirmation *InvestmentPendingAction     `json:"confirmation,omitempty"`
}

// InvestmentRebalancePreviewRequest asks for a pre-trade calculation.
type InvestmentRebalancePreviewRequest struct {
	AmountUSD *decimal.Decimal `json:"amount_usd,omitempty"`
}

// InvestmentRebalanceRequest manually triggers a provider rebalance.
type InvestmentRebalanceRequest struct {
	Reason            string `json:"reason,omitempty"`
	ConfirmationToken string `json:"confirmation_token,omitempty"`
}

// InvestmentPendingAction tells the caller what to confirm and how.
type InvestmentPendingAction struct {
	Token       string    `json:"token"`
	Action      string    `json:"action"`
	PayloadHash string    `json:"payload_hash"`
	ExpiresAt   time.Time `json:"expires_at"`
	Instruction string    `json:"instruction"`
}

// InvestmentActionStatus is the coarse state of a mutation attempt.
type InvestmentActionStatus string

const (
	InvestmentActionAwaitingConfirmation InvestmentActionStatus = "AWAITING_CONFIRMATION"
	InvestmentActionCompleted            InvestmentActionStatus = "COMPLETED"
	InvestmentActionRejected             InvestmentActionStatus = "REJECTED"
)

// InvestmentCreateStrategyResponse is returned by create/update strategy.
type InvestmentCreateStrategyResponse struct {
	Status       InvestmentActionStatus      `json:"status"`
	Strategy     *InvestmentStrategy         `json:"strategy,omitempty"`
	Version      *InvestmentStrategyVersion  `json:"version,omitempty"`
	Validation   *InvestmentValidationReport `json:"validation,omitempty"`
	Policy       *InvestmentPolicyDecision   `json:"policy,omitempty"`
	Confirmation *InvestmentPendingAction    `json:"confirmation,omitempty"`
}

// InvestmentEnrollResponse is returned by the enroll flow.
type InvestmentEnrollResponse struct {
	Status       InvestmentActionStatus       `json:"status"`
	Enrollment   *InvestmentEnrollment        `json:"enrollment,omitempty"`
	Preview      *InvestmentAllocationPreview `json:"preview,omitempty"`
	Policy       *InvestmentPolicyDecision    `json:"policy,omitempty"`
	Funding      *InvestmentFundingTransfer   `json:"funding,omitempty"`
	Confirmation *InvestmentPendingAction     `json:"confirmation,omitempty"`
}

// InvestmentOrderResponse is returned by allocation-change flows.
type InvestmentOrderResponse struct {
	Status       InvestmentActionStatus       `json:"status"`
	Execution    *InvestmentExecution         `json:"execution,omitempty"`
	Preview      *InvestmentAllocationPreview `json:"preview,omitempty"`
	Policy       *InvestmentPolicyDecision    `json:"policy,omitempty"`
	Confirmation *InvestmentPendingAction     `json:"confirmation,omitempty"`
}

// InvestmentLimitsResponse is the effective-limits read model.
type InvestmentLimitsResponse struct {
	KYCTier            string                  `json:"kyc_tier"`
	Verdicts           InvestmentLimitVerdicts `json:"verdicts"`
	Limits             InvestmentLimits        `json:"limits"`
	AllowedAssetsCount int                     `json:"allowed_assets_count"`
	Source             string                  `json:"source"`
}

// InvestmentLimitVerdicts answers capability questions without executing.
type InvestmentLimitVerdicts struct {
	CanCreateStrategy bool              `json:"can_create_strategy"`
	CanEnroll         bool              `json:"can_enroll"`
	CanWithdraw       bool              `json:"can_withdraw"`
	CreateStrategy    InvestmentVerdict `json:"create_strategy_verdict"`
	Enroll            InvestmentVerdict `json:"enroll_verdict"`
	Withdraw          InvestmentVerdict `json:"withdraw_verdict"`
}

// InvestmentInvestorListResponse is the discovery read model.
type InvestmentInvestorListResponse struct {
	Investors  []InvestmentInvestor `json:"investors"`
	NextCursor string               `json:"next_cursor,omitempty"`
	Collection string               `json:"collection"`
	Source     string               `json:"source"`
	Note       string               `json:"note,omitempty"`
}

// InvestmentInvestorActivityResponse explains what activity we can and cannot
// verify. An empty list is not the same as "no activity".
type InvestmentInvestorActivityResponse struct {
	InvestorID        string                       `json:"investor_id"`
	Activity          []InvestmentActivityEntry    `json:"activity"`
	Provenance        InvestmentInvestorProvenance `json:"provenance"`
	UnavailableReason string                       `json:"unavailable_reason,omitempty"`
}

// InvestmentActivityEntry is one observable entry/exit event.
type InvestmentActivityEntry struct {
	At         time.Time            `json:"at"`
	Kind       string               `json:"kind"`
	AssetID    string               `json:"asset_id,omitempty"`
	Symbol     string               `json:"symbol,omitempty"`
	Side       string               `json:"side,omitempty"`
	Weight     *decimal.Decimal     `json:"weight,omitempty"`
	Note       string               `json:"note,omitempty"`
	Provenance InvestmentProvenance `json:"provenance"`
}
