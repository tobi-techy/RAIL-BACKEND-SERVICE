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
// (nextDueAt/lastRebalanceAt) only appears on a portfolio, not a strategy.
type GliderSchedule struct {
	Type            string     `json:"type,omitempty"`
	Frequency       string     `json:"frequency,omitempty"`
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

// GliderStrategyInput is a create-strategy or publish-version request body.
type GliderStrategyInput struct {
	Name        string                 `json:"name,omitempty"`
	Description *string                `json:"description,omitempty"`
	Allocation  GliderAllocation       `json:"allocation,omitempty"`
	Schedule    *GliderSchedule        `json:"schedule,omitempty"`
	Preferences *GliderPreferencesWire `json:"preferences,omitempty"`
	IsPublic    *bool                  `json:"isPublic,omitempty"`
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

// GliderPortfolio is one enrolled user portfolio.
type GliderPortfolio struct {
	PortfolioID     string               `json:"portfolioId"`
	StrategyID      string               `json:"strategyId,omitempty"`
	StrategyVersion int                  `json:"strategyVersion,omitempty"`
	Status          string               `json:"status,omitempty"`
	AccountType     string               `json:"accountType,omitempty"`
	SmartAccounts   []GliderSmartAccount `json:"smartAccounts,omitempty"`
	Schedule        GliderSchedule       `json:"schedule,omitempty"`
	TotalValueUSD   *decimal.Decimal     `json:"totalValueUsd,omitempty"`
	CreatedAt       *time.Time           `json:"createdAt,omitempty"`
	UpdatedAt       *time.Time           `json:"updatedAt,omitempty"`
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
	Warnings      []string         `json:"warnings,omitempty"`
	AsOf          time.Time        `json:"-"`
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

// GliderAuthorization is the off-chain authorization a Solana portfolio owner
// signs: kind "ecdsa" (EVM-rooted, sign raw via EIP-191) or "solana-message"
// (Solana-rooted, sign text via ed25519).
type GliderAuthorization struct {
	Kind    string `json:"kind,omitempty"`
	Raw     string `json:"raw,omitempty"`
	Text    string `json:"text,omitempty"`
	Message string `json:"message,omitempty"`
}

// GliderEnrollMessage is the provider's stage-1 signable message object
// (e.g. { kind: "solana-message", text: ... } or { kind: "ecdsa", raw: ... }).
type GliderEnrollMessage struct {
	Kind string `json:"kind,omitempty"`
	Raw  string `json:"raw,omitempty"`
	Text string `json:"text,omitempty"`
}

// GliderEnrollAuthorization is stage 1 of the two-stage enroll flow.
type GliderEnrollAuthorization struct {
	FlowID           string               `json:"flowId"`
	AccountIndex     string               `json:"accountIndex"`
	AgentAccountID   string               `json:"agentAccountId"`
	ChainIDs         []int                `json:"chainIds,omitempty"`
	AccountType      string               `json:"accountType,omitempty"`
	SwigAccountID    string               `json:"swigAccountId,omitempty"`
	DepositAccountID string               `json:"depositAccountId,omitempty"`
	SwigRoleID       *int                 `json:"swigRoleId,omitempty"`
	ReusedSwig       bool                 `json:"reusedSwig,omitempty"`
	Message          *GliderEnrollMessage `json:"message,omitempty"`
	// Solana Model B: the base64 serialized transaction the owner signs and
	// that stage 2 submits via signedSolanaTransaction.
	SolanaTransaction  string               `json:"solanaTransaction,omitempty"`
	Authorization      *GliderAuthorization `json:"authorization,omitempty"`
	ExpiresAt          *time.Time           `json:"expiresAt,omitempty"`
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
	FlowID                  string `json:"flowId"`
	AccountIndex            string `json:"accountIndex"`
	AgentAccountID          string `json:"agentAccountId"`
	ChainIDs                []int  `json:"chainIds"`
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

// GliderWithdrawAuthorization is withdrawal stage 1's response.
type GliderWithdrawAuthorization struct {
	AuthorizationID string               `json:"authorizationId"`
	ExpiresAt       *time.Time           `json:"expiresAt,omitempty"`
	Message         string               `json:"message,omitempty"`
	Authorization   *GliderAuthorization `json:"authorization,omitempty"`
	TypedData       json.RawMessage      `json:"typedData,omitempty"`
}

// GliderWithdrawSubmitInput is withdrawal stage 2's request body.
type GliderWithdrawSubmitInput struct {
	Message            string                `json:"message"`
	Signature          string                `json:"signature"`
	RecipientAccountID string                `json:"recipientAccountId"`
	Assets             []GliderWithdrawAsset `json:"assets"`
	Liquidate          bool                  `json:"liquidate,omitempty"`
	SettlementAssetID  string                `json:"settlementAssetId,omitempty"`
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

// GliderAssetBreakdownRequest is the allocation-breakdown pre-flight.
type GliderAssetBreakdownRequest struct {
	Symbols []string `json:"symbols"`
}
