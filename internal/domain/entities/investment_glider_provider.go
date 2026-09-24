package entities

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Provider (Glider V2) boundary types.
//
// These mirror the provider's wire shapes so the adapter can stay thin and the
// domain can consume provider state without importing infrastructure. Monetary
// values arrive as decimal strings and decode into decimal.Decimal.
// ---------------------------------------------------------------------------

// GliderSwapPreferences are the provider's per-strategy swap constraints.
type GliderSwapPreferences struct {
	SlippageBps    *int             `json:"slippageBps,omitempty"`
	PriceImpactBps *int             `json:"priceImpactBps,omitempty"`
	ThresholdUSD   *decimal.Decimal `json:"thresholdUsd,omitempty"`
}

// GliderSchedule is the provider's rebalance schedule. Runtime visibility
// (nextDueAt/lastRebalanceAt) only appears on a portfolio, not a strategy. The
// portfolio-level Status is the provider's automation state: "active" (the
// scheduler ticks) or "paused" (the integrator stopped automation).
type GliderSchedule struct {
	Type            string     `json:"type,omitempty"`
	Frequency       string     `json:"frequency,omitempty"`
	Status          string     `json:"status,omitempty"`
	NextDueAt       *time.Time `json:"nextDueAt,omitempty"`
	LastRebalanceAt *time.Time `json:"lastRebalanceAt,omitempty"`
}

// GliderStrategy is a provider strategy template.
type GliderStrategy struct {
	StrategyID  string            `json:"strategyId"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Allocation  GliderAllocation  `json:"allocation"`
	Schedule    GliderSchedule    `json:"schedule,omitempty"`
	Preferences GliderPreferences `json:"preferences,omitempty"`
	IsPublic    bool              `json:"isPublic,omitempty"`
	Version     int               `json:"version,omitempty"`
	MaxAPY      *decimal.Decimal  `json:"maxApy,omitempty"`
	CreatedAt   *time.Time        `json:"createdAt,omitempty"`
}

// GliderAllocation is the provider's allocation object.
type GliderAllocation struct {
	Assets []InvestmentAllocationLeg `json:"assets"`
}

// gliderAssetLegWire is the provider-native asset leg: { assetId, weight }.
// Glider requires the CAIP-19 id under "assetId" and the weight as a decimal
// STRING (its API convention: monetary values are never JSON numbers).
type gliderAssetLegWire struct {
	AssetID string `json:"assetId"`
	Weight  string `json:"weight"`
}

func (a GliderAllocation) MarshalJSON() ([]byte, error) {
	legs := make([]gliderAssetLegWire, 0, len(a.Assets))
	for _, leg := range a.Assets {
		id := leg.CAIP19
		if id == "" {
			id = leg.AssetID
		}
		legs = append(legs, gliderAssetLegWire{AssetID: id, Weight: leg.Weight.String()})
	}
	return json.Marshal(map[string][]gliderAssetLegWire{"assets": legs})
}

func (a *GliderAllocation) UnmarshalJSON(data []byte) error {
	var wire struct {
		Assets []gliderAssetLegWire `json:"assets"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	legs := make([]InvestmentAllocationLeg, 0, len(wire.Assets))
	for _, l := range wire.Assets {
		w, err := decimal.NewFromString(l.Weight)
		if err != nil {
			return fmt.Errorf("glider allocation weight %q: %w", l.Weight, err)
		}
		legs = append(legs, InvestmentAllocationLeg{CAIP19: l.AssetID, Weight: w})
	}
	a.Assets = legs
	return nil
}

// GliderPreferences wraps the provider's preference namespaces.
type GliderPreferences struct {
	Swap GliderSwapPreferenceView `json:"swap"`
}

// GliderSwapPreferenceView is the provider's nullable swap preference view.
type GliderSwapPreferenceView struct {
	SlippageBps    *int             `json:"slippageBps"`
	PriceImpactBps *int             `json:"priceImpactBps"`
	ThresholdUSD   *decimal.Decimal `json:"thresholdUsd"`
}

// GliderStrategyInput is a create-strategy request body. The provider accepts
// name, allocation, schedule, preferences and isPublic here. Version publishing
// uses GliderPublishVersionInput instead: POST /strategies/{id}/versions accepts
// ONLY allocation and changeLog and rejects any other key with 400.
type GliderStrategyInput struct {
	Name        string                 `json:"name,omitempty"`
	Description *string                `json:"description,omitempty"`
	Allocation  GliderAllocation       `json:"allocation,omitempty"`
	Schedule    *GliderSchedule        `json:"schedule,omitempty"`
	Preferences *GliderPreferencesWire `json:"preferences,omitempty"`
	IsPublic    *bool                  `json:"isPublic,omitempty"`
}

// GliderPublishVersionInput is the publish-version request body. The endpoint
// is strict: unknown keys (including name, schedule, preferences or a
// client-supplied version) return 400.
type GliderPublishVersionInput struct {
	Allocation GliderAllocation `json:"allocation"`
	ChangeLog  string           `json:"changeLog,omitempty"` // max 500 chars
}

// GliderPublishedVersion is the publish-version response: the server-assigned
// version number that just became the strategy's active version.
type GliderPublishedVersion struct {
	Version   int        `json:"version"`
	IsHead    bool       `json:"isHead,omitempty"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
}

// GliderPreferencesWire matches the provider's request shape: the swap
// constraints live under the "swap" key.
type GliderPreferencesWire struct {
	Swap *GliderSwapPreferences `json:"swap,omitempty"`
}

// GliderSmartAccount is one provider smart account for a portfolio.
type GliderSmartAccount struct {
	AccountID        string `json:"accountId"`
	DepositAccountID string `json:"depositAccountId,omitempty"`
	SwigRoleID       *int   `json:"swigRoleId,omitempty"`
}

// GliderPortfolio is one enrolled user portfolio. The real GET /portfolios/{id}
// response has no top-level status, accountType or totalValueUsd: automation
// state lives at Schedule.Status and live balances at the positions endpoint.
type GliderPortfolio struct {
	PortfolioID         string               `json:"portfolioId"`
	PortfolioName       string               `json:"portfolioName,omitempty"`
	OwnerAccountID      string               `json:"ownerAccountId,omitempty"`
	StrategyID          string               `json:"strategyId,omitempty"`
	StrategyName        string               `json:"strategyName,omitempty"`
	StrategyDescription string               `json:"strategyDescription,omitempty"`
	StrategyVersion     int                  `json:"strategyVersion,omitempty"`
	SmartAccounts       []GliderSmartAccount `json:"smartAccounts,omitempty"`
	Schedule            GliderSchedule       `json:"schedule,omitempty"`
	CreatedAt           *time.Time           `json:"createdAt,omitempty"`
	UpdatedAt           *time.Time           `json:"updatedAt,omitempty"`
}

// GliderPosition is one live asset position inside a portfolio.
type GliderPosition struct {
	AssetID    string          `json:"assetId"`
	Symbol     string          `json:"symbol"`
	Name       string          `json:"name,omitempty"`
	Decimals   int             `json:"decimals"`
	Balance    decimal.Decimal `json:"balance"`
	BalanceRaw string          `json:"balanceRaw,omitempty"`
	PriceUSD   decimal.Decimal `json:"priceUsd"`
	ValueUSD   decimal.Decimal `json:"valueUsd"`
}

// GliderPositions is the provider's live position read for a portfolio.
type GliderPositions struct {
	PortfolioID   string           `json:"portfolioId"`
	TotalValueUSD decimal.Decimal  `json:"totalValueUsd"`
	Assets        []GliderPosition `json:"assets"`
	FetchedAt     *time.Time       `json:"fetchedAt,omitempty"`
	Warnings      []GliderWarning  `json:"warnings,omitempty"`
	AsOf          time.Time        `json:"-"`
}

// GliderWarning is one entry of the positions endpoint's structured warnings
// array. The provider surfaces partial failures here (RPC_ERROR,
// MISSING_PRICE, IN_TRANSIT_READ_FAILED, ...) instead of failing the request.
type GliderWarning struct {
	Kind    string `json:"kind"`
	Message string `json:"message,omitempty"`
}

// GliderOperationHandle is the provider's async operation handle.
type GliderOperationHandle struct {
	OperationID string     `json:"operationId"`
	SubmittedAt *time.Time `json:"submittedAt,omitempty"`
}

// GliderOperationState is the polled state of a provider operation.
type GliderOperationState struct {
	OperationID string     `json:"operationId"`
	PortfolioID string     `json:"portfolioId,omitempty"`
	Kind        string     `json:"kind,omitempty"`
	State       string     `json:"state"`
	Error       *string    `json:"error,omitempty"`
	CreatedAt   *time.Time `json:"createdAt,omitempty"`
	UpdatedAt   *time.Time `json:"updatedAt,omitempty"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
}

// GliderAuthorization is the stage-1 authorization union returned by the
// withdraw flow: "ecdsa" (EVM / Solana Model A — sign Raw via EIP-191) or
// "solana-message" (Solana Model B — sign Text verbatim as UTF-8 via ed25519).
// Message carries the provider's structured signed-message object verbatim; it
// is what stage 2 echoes back as its "message" field.
type GliderAuthorization struct {
	Kind    string          `json:"kind,omitempty"`
	Raw     string          `json:"raw,omitempty"`
	Text    string          `json:"text,omitempty"`
	Message json.RawMessage `json:"message,omitempty"`
}

// GliderEnrollMessage is the provider's stage-1 signable message object
// (e.g. { kind: "solana-message", text: ... } or { kind: "ecdsa", raw: ... }).
type GliderEnrollMessage struct {
	Kind string `json:"kind,omitempty"`
	Raw  string `json:"raw,omitempty"`
	Text string `json:"text,omitempty"`
}

// GliderEnrollAuthorization is stage 1 of the two-stage enroll flow. The
// flowId is the stage-2 idempotency anchor with a 24h TTL.
type GliderEnrollAuthorization struct {
	FlowID         string `json:"flowId"`
	AccountIndex   string `json:"accountIndex"`
	AgentAccountID string `json:"agentAccountId"`
	AccountType    string `json:"accountType,omitempty"`
	SwigAccountID  string `json:"swigAccountId,omitempty"`
	// DepositAccountID and SwigRoleID appear on Solana (SVM subaccount)
	// enrollments; ReusedSwig reports whether an existing Swig was reused.
	DepositAccountID string               `json:"depositAccountId,omitempty"`
	SwigRoleID       *int                 `json:"swigRoleId,omitempty"`
	ReusedSwig       bool                 `json:"reusedSwig,omitempty"`
	Message          *GliderEnrollMessage `json:"message,omitempty"`
	// Solana Model B: the base64 serialized transaction the owner signs and
	// that stage 2 submits via signedSolanaTransaction.
	SolanaTransaction string `json:"solanaTransaction,omitempty"`
}

// GliderEnrollSignatureInput is stage 1's request body.
type GliderEnrollSignatureInput struct {
	StrategyID     string `json:"strategyId"`
	OwnerAccountID string `json:"ownerAccountId"`
	ChainIDs       []int  `json:"chainIds"`
	AccountType    string `json:"accountType,omitempty"`
}

// GliderEnrollSubmitInput is stage 2's request body. Every stage-1 round-trip
// field must be echoed verbatim.
type GliderEnrollSubmitInput struct {
	FlowID         string `json:"flowId"`
	AccountIndex   string `json:"accountIndex"`
	AgentAccountID string `json:"agentAccountId"`
	ChainIDs       []int  `json:"chainIds"`
	// OwnerAccountID and StrategyID are required by stage 2 and must echo the
	// values used to start the flow.
	OwnerAccountID          string `json:"ownerAccountId"`
	StrategyID              string `json:"strategyId"`
	AccountType             string `json:"accountType,omitempty"`
	SignedSolanaTransaction string `json:"signedSolanaTransaction,omitempty"`
	Signature               string `json:"signature,omitempty"`
}

// GliderWithdrawAsset is one asset in a withdrawal request.
type GliderWithdrawAsset struct {
	AssetID   string `json:"assetId"`
	AmountRaw string `json:"amountRaw"`
}

// GliderWithdrawSignatureInput is withdrawal stage 1's request body.
type GliderWithdrawSignatureInput struct {
	RecipientAccountID string                `json:"recipientAccountId"`
	Assets             []GliderWithdrawAsset `json:"assets"`
	Liquidate          bool                  `json:"liquidate,omitempty"`
	SettlementAssetID  string                `json:"settlementAssetId,omitempty"`
}

// GliderWithdrawAuthorization is withdrawal stage 1's response. For EVM the
// signable payload is TypedData (EIP-712); for Solana it is Authorization —
// sign Authorization.Text (Model B, ed25519, base58 sig) or Authorization.Raw
// (Model A, EIP-191). Authorization.Message is the provider's structured
// signed-message object and MUST be echoed verbatim to stage 2.
type GliderWithdrawAuthorization struct {
	AuthorizationID string               `json:"authorizationId"`
	ExpiresAt       *time.Time           `json:"expiresAt,omitempty"`
	TypedData       json.RawMessage      `json:"typedData,omitempty"`
	Authorization   *GliderAuthorization `json:"authorization,omitempty"`
}

// GliderWithdrawSubmitInput is withdrawal stage 2's request body. It carries
// ONLY the stage-1 message object echoed verbatim and the owner's signature —
// assets, recipient, liquidate and settlement are bound inside the signed
// message, so re-stating them would fail signature verification.
type GliderWithdrawSubmitInput struct {
	Message   json.RawMessage `json:"message"`
	Signature string          `json:"signature"`
}

// GliderDiscoveredMetrics are the observable metrics of a public strategy.
type GliderDiscoveredMetrics struct {
	TVLUSD         *decimal.Decimal `json:"tvlUsd,omitempty"`
	PortfolioCount *int             `json:"portfolioCount,omitempty"`
	Performance    *struct {
		Summary struct {
			Windows []struct {
				Window        string          `json:"window"`
				PercentChange decimal.Decimal `json:"percentChange"`
				Since         string          `json:"since"`
			} `json:"windows"`
		} `json:"summary"`
	} `json:"performance,omitempty"`
}

// GliderDiscoveredStrategy is one mirrorable public strategy.
type GliderDiscoveredStrategy struct {
	StrategyID  string                  `json:"strategyId"`
	Name        string                  `json:"name"`
	Description *string                 `json:"description,omitempty"`
	Allocation  GliderAllocation        `json:"allocation"`
	Metrics     GliderDiscoveredMetrics `json:"metrics"`
	CanMirror   bool                    `json:"canMirror"`
	MaxAPY      *decimal.Decimal        `json:"maxApy,omitempty"`
	CreatedAt   *time.Time              `json:"createdAt,omitempty"`
}

// GliderIdentity is GET /whoami.
type GliderIdentity struct {
	APIKeyID    string   `json:"apiKeyId"`
	TenantName  string   `json:"tenantName"`
	TenantEmail string   `json:"tenantEmail"`
	Scopes      []string `json:"scopes"`
}
