package entities

import (
	"time"

	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Extended Glider V2 boundary types.
//
// Shapes below mirror api.txt verbatim (paths, field names, decimal-string
// money). They cover the read and two-stage endpoints the thin client did not
// previously expose. Nothing here is invented: every struct maps to a real
// METHOD + PATH documented in api.txt.
// ---------------------------------------------------------------------------

// GliderStrategyVersion is one entry of GET /strategies/{id}/versions
// (newest first; exactly one has IsHead=true).
type GliderStrategyVersion struct {
	Version    int              `json:"version"`
	Allocation GliderAllocation `json:"allocation"`
	ChangeLog  *string          `json:"changeLog"`
	IsHead     bool             `json:"isHead"`
	CreatedAt  *time.Time       `json:"createdAt,omitempty"`
}

// GliderPerformancePoint is one daily point of a performance curve.
type GliderPerformancePoint struct {
	Date          string           `json:"date"`
	PercentChange *decimal.Decimal `json:"percentChange"` // null when unavailable
	ValueUSD      *decimal.Decimal `json:"valueUsd,omitempty"`
	CashFlowUSD   *decimal.Decimal `json:"cashFlowUsd,omitempty"`
	IsLive        bool             `json:"isLive,omitempty"`
}

// GliderPerformanceWindow is one lookback entry (1d..12m, all).
type GliderPerformanceWindow struct {
	Window        string          `json:"window"`
	PercentChange decimal.Decimal `json:"percentChange"`
	Since         string          `json:"since"`
}

// GliderPerformanceMeta describes the curve calculation.
type GliderPerformanceMeta struct {
	Method     string     `json:"method"` // TWR or MWR
	Currency   string     `json:"currency"`
	Resolution string     `json:"resolution"`
	AsOf       *time.Time `json:"asOf,omitempty"`
}

// GliderStrategyPerformance is GET /strategies/{strategyId}/performance.
type GliderStrategyPerformance struct {
	StrategyID string                    `json:"strategyId"`
	Schedule   GliderSchedule            `json:"schedule"`
	Meta       GliderPerformanceMeta     `json:"meta"`
	Points     []GliderPerformancePoint  `json:"points"`
	Summary    *GliderPerformanceSummary `json:"summary,omitempty"`
}

// GliderPerformanceSummary carries windowed lookbacks.
type GliderPerformanceSummary struct {
	Windows []GliderPerformanceWindow `json:"windows"`
}

// GliderPortfolioPerformance is GET /portfolios/{id}/performance.
// returnMethod query: MWR (default) or TWR.
type GliderPortfolioPerformance struct {
	PortfolioID string                    `json:"portfolioId"`
	StrategyID  string                    `json:"strategyId"`
	Meta        GliderPerformanceMeta     `json:"meta"`
	Points      []GliderPerformancePoint  `json:"points"`
	Summary     *GliderPerformanceSummary `json:"summary,omitempty"`
}

// GliderFeeView is GET /strategies/{id}/fees and GET /tenant/fees:
// { swapBps: int|null } — basis points 30-300, null when unconfigured.
type GliderFeeView struct {
	SwapBps *int `json:"swapBps"`
}

// GliderFeePatch is PATCH .../fees. Nil pointer = omit (preserve);
// non-nil pointer to nil value is not expressible in Go structs, so clearing
// is done via GliderFeeClear (explicit null body).
type GliderFeePatch struct {
	SwapBps *int `json:"swapBps,omitempty"`
}

// GliderStrategyPatch is PATCH /strategies/{strategyId}: display metadata
// only (name, description, isPublic). Allocation goes through versions;
// schedule through PUT .../schedule; prefs through PATCH .../preferences.
type GliderStrategyPatch struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	IsPublic    *bool   `json:"isPublic,omitempty"`
}

// GliderPortfolioPatch is PATCH /portfolios/{portfolioId}: portfolioName only.
type GliderPortfolioPatch struct {
	PortfolioName *string `json:"portfolioName,omitempty"`
}

// GliderPortfolioListFilter maps to GET /portfolios query params:
// ownerAccountId, strategyId, status, cursor, limit.
type GliderPortfolioListFilter struct {
	OwnerAccountID string
	StrategyID     string
	Status         string // active|paused
	Cursor         string
	Limit          int
}

// GliderStrategyListFilter maps to GET /strategies cursor pagination.
type GliderStrategyListFilter struct {
	Cursor string
	Limit  int
}

// GliderChainActivationMessage is POST .../chains/signature data.message:
// ECDSA {kind:ecdsa, raw} or ERC-1271 {kind:typed-data, typedData}.
type GliderChainActivationMessage struct {
	Kind      string `json:"kind,omitempty"`
	Raw       string `json:"raw,omitempty"`
	TypedData any    `json:"typedData,omitempty"`
}

// GliderChainActivationInput is POST .../chains/signature body.
type GliderChainActivationInput struct {
	ChainIDs []int `json:"chainIds"`
}

// GliderChainActivationSubmit is POST .../chains body: chainIds must exactly
// match the stage-1 call; signature is EIP-191 over raw (ECDSA) or ERC-1271.
type GliderChainActivationSubmit struct {
	ChainIDs  []int  `json:"chainIds"`
	Signature string `json:"signature"`
}

// GliderLiquidateSignatureInput is POST .../liquidate-all/signature body:
// server enumerates holdings above the tenant swap threshold on the
// recipient's chain. No asset list from the caller.
type GliderLiquidateSignatureInput struct {
	RecipientAccountID string `json:"recipientAccountId"`
	SettlementAssetID  string `json:"settlementAssetId,omitempty"`
}

// GliderScope is one entry of GET /scopes (no auth required).
type GliderScope struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Tier        string `json:"tier"`
}

// GliderSectorRow is one row of GET .../sector-exposure.
type GliderSectorRow struct {
	DisplaySector           string          `json:"displaySector"`
	MarketValueUSD          decimal.Decimal `json:"marketValueUsd"`
	Weight                  decimal.Decimal `json:"weight"`
	ClassifiedPositionCount int             `json:"classifiedPositionCount"`
	TotalPositionCount      int             `json:"totalPositionCount"`
}

// GliderSectorExposure is GET /portfolios/{id}/sector-exposure (raw passthrough;
// rows carry the canonical taxonomy internal_display_sector_v1).
type GliderSectorExposure struct {
	PortfolioID                string            `json:"portfolioId"`
	AsOfDate                   *time.Time        `json:"asOfDate,omitempty"`
	Taxonomy                   string            `json:"taxonomy"`
	TotalMarketValueUSD        decimal.Decimal   `json:"totalMarketValueUsd"`
	ClassifiedMarketValueUSD   decimal.Decimal   `json:"classifiedMarketValueUsd"`
	UnclassifiedMarketValueUSD decimal.Decimal   `json:"unclassifiedMarketValueUsd"`
	Rows                       []GliderSectorRow `json:"rows"`
}

// GliderBreakdownHolding is one holding of POST /assets/allocation-breakdown.
type GliderBreakdownHolding struct {
	AssetCanonicalID *string `json:"assetCanonicalId,omitempty"`
	CaipAssetID      *string `json:"caipAssetId,omitempty"`
	Symbol           *string `json:"symbol,omitempty"`
	Name             *string `json:"name,omitempty"`
	Weight           *string `json:"weight,omitempty"`
	MarketValueUSD   *string `json:"marketValueUsd,omitempty"`
}

// GliderBreakdownInput is POST /assets/allocation-breakdown body.
type GliderBreakdownInput struct {
	Holdings          []GliderBreakdownHolding `json:"holdings"`
	Dimensions        []string                 `json:"dimensions,omitempty"`
	IncludeHoldings   *bool                    `json:"includeHoldings,omitempty"`
	MultiCategoryMode string                   `json:"multiCategoryMode,omitempty"` // primary|apportioned|overlap
}
