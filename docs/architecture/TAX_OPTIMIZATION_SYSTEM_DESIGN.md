# Tax Optimization System Design

## Document Information
- **Version**: 1.0
- **Last Updated**: March 23, 2026
- **Status**: Draft
- **Parent Doc**: [Wealth Engine](../WEALTH_ENGINE.md)

---

## 1. Overview

This document specifies the technical implementation of Rail's tax optimization system. It covers tax-lot tracking, tax-loss harvesting (TLH), smart sell ordering, and tax reporting — all built on top of Rail's existing invest engine, auto-invest pipeline, and Alpaca brokerage integration.

### 1.1 Design Principles

1. **Every fill creates a lot.** No share purchase goes untracked.
2. **Sells are always tax-optimized.** Long-term lots first, highest cost basis first.
3. **Harvesting is invisible.** Users never see TLH trades — just lower tax bills.
4. **Wash sale compliance is non-negotiable.** Zero tolerance for violations.
5. **Existing services are extended, not replaced.** Tax optimization hooks into `autoinvest`, `roundup`, `investing`, and `allocation` — it doesn't duplicate them.

### 1.2 System Context

```
┌─────────────────────────────────────────────────────────────────┐
│                     EXISTING RAIL SERVICES                       │
│                                                                  │
│  funding/service.go ──► allocation/service.go (70/30 split)     │
│                              │                                   │
│                    ┌─────────┴──────────┐                        │
│                    ▼                    ▼                         │
│           autoinvest/service.go   roundup/service.go             │
│                    │                    │                         │
│                    ▼                    ▼                         │
│              strategy/engine.go   (accumulate → invest)          │
│                    │                    │                         │
│                    ▼                    ▼                         │
│              Alpaca PlaceMarketOrder                              │
│                    │                                             │
│                    ▼                                             │
│         ┌──────────────────────┐                                 │
│         │   ORDER FILL EVENT   │ ◄── This is where we hook in   │
│         └──────────┬───────────┘                                 │
│                    │                                             │
│  ┌─────────────────┼─────────────────────────────────────────┐  │
│  │                 ▼           NEW TAX OPTIMIZATION LAYER     │  │
│  │                                                            │  │
│  │  ┌──────────────────┐  ┌──────────────────┐               │  │
│  │  │  Tax Lot Tracker │  │  Tax Optimizer   │               │  │
│  │  │  (lot creation)  │  │  (TLH scanner)   │               │  │
│  │  └────────┬─────────┘  └────────┬─────────┘               │  │
│  │           │                     │                          │  │
│  │           ▼                     ▼                          │  │
│  │  ┌──────────────────┐  ┌──────────────────┐               │  │
│  │  │  Smart Seller    │  │  Tax Reporter    │               │  │
│  │  │  (lot selection) │  │  (year-end)      │               │  │
│  │  └──────────────────┘  └──────────────────┘               │  │
│  └────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

---

## 2. Database Schema

### 2.1 Tax Lots Table

```sql
-- Migration: XXXX_create_tax_lots.up.sql

CREATE TABLE tax_lots (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id),
    symbol          VARCHAR(20) NOT NULL,
    quantity        DECIMAL(20,8) NOT NULL,
    cost_basis      DECIMAL(20,8) NOT NULL,       -- price per share at purchase
    acquired_at     TIMESTAMPTZ NOT NULL,
    source          VARCHAR(20) NOT NULL DEFAULT 'autoinvest',
    order_id        UUID,                          -- Alpaca order reference
    remaining_qty   DECIMAL(20,8) NOT NULL,        -- decremented on sells
    closed_at       TIMESTAMPTZ,                   -- set when remaining_qty = 0
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Primary query: open lots for a user (for TLH scanning + sell ordering)
CREATE INDEX idx_tax_lots_user_open ON tax_lots(user_id, symbol)
    WHERE remaining_qty > 0 AND closed_at IS NULL;

-- For wash sale lookback: recent closed lots per user+symbol
CREATE INDEX idx_tax_lots_user_closed ON tax_lots(user_id, symbol, closed_at)
    WHERE closed_at IS NOT NULL;

-- For tax reporting: lots closed in a date range
CREATE INDEX idx_tax_lots_closed_range ON tax_lots(user_id, closed_at)
    WHERE closed_at IS NOT NULL;

COMMENT ON TABLE tax_lots IS 'Individual share purchase records for tax optimization. Every fill from autoinvest, roundup, or DRIP creates a lot.';
COMMENT ON COLUMN tax_lots.cost_basis IS 'Price per share at time of purchase';
COMMENT ON COLUMN tax_lots.remaining_qty IS 'Shares remaining (decremented on partial/full sells)';
COMMENT ON COLUMN tax_lots.source IS 'Origin: autoinvest, roundup, drip, manual, tlh_replacement';
```

### 2.2 Tax Harvest Events Table

```sql
-- Migration: XXXX_create_tax_harvest_events.up.sql

CREATE TABLE tax_harvest_events (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id),
    sold_lot_id         UUID NOT NULL REFERENCES tax_lots(id),
    sold_symbol         VARCHAR(20) NOT NULL,
    sold_quantity       DECIMAL(20,8) NOT NULL,
    sold_proceeds       DECIMAL(20,2) NOT NULL,
    cost_basis_total    DECIMAL(20,2) NOT NULL,
    realized_loss       DECIMAL(20,2) NOT NULL,       -- always negative (it's a loss)
    replacement_symbol  VARCHAR(20) NOT NULL,
    replacement_lot_id  UUID REFERENCES tax_lots(id),  -- the new lot created
    replacement_amount  DECIMAL(20,2) NOT NULL,
    wash_sale_clear     BOOLEAN NOT NULL DEFAULT true,  -- confirmed no wash sale
    status              VARCHAR(20) NOT NULL DEFAULT 'pending',
    executed_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_harvest_events_user ON tax_harvest_events(user_id, created_at DESC);
CREATE INDEX idx_harvest_events_status ON tax_harvest_events(status) WHERE status = 'pending';

COMMENT ON TABLE tax_harvest_events IS 'Audit trail for every tax-loss harvest execution';
```

### 2.3 Wash Sale Tracking Table

```sql
-- Migration: XXXX_create_wash_sale_windows.up.sql

CREATE TABLE wash_sale_windows (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id),
    symbol      VARCHAR(20) NOT NULL,
    event_type  VARCHAR(10) NOT NULL,   -- 'buy' or 'sell'
    event_date  TIMESTAMPTZ NOT NULL,
    lot_id      UUID REFERENCES tax_lots(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The critical query: "was this symbol traded in the last 30 days?"
CREATE INDEX idx_wash_sale_user_symbol ON wash_sale_windows(user_id, symbol, event_date DESC);

COMMENT ON TABLE wash_sale_windows IS 'Tracks buy/sell events per symbol for 30-day wash sale rule compliance';
```

### 2.4 Tax Reports Table (Extends Existing)

The `TaxReport` entity already exists in `portfolio.go`. We add a detail table:

```sql
-- Migration: XXXX_create_tax_report_details.up.sql

CREATE TABLE tax_report_line_items (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id           UUID NOT NULL,  -- references tax_reports.id
    user_id             UUID NOT NULL REFERENCES users(id),
    lot_id              UUID NOT NULL REFERENCES tax_lots(id),
    symbol              VARCHAR(20) NOT NULL,
    quantity            DECIMAL(20,8) NOT NULL,
    cost_basis_total    DECIMAL(20,2) NOT NULL,
    proceeds            DECIMAL(20,2) NOT NULL,
    gain_loss           DECIMAL(20,2) NOT NULL,
    holding_period      VARCHAR(10) NOT NULL,   -- 'short_term' or 'long_term'
    acquired_at         TIMESTAMPTZ NOT NULL,
    disposed_at         TIMESTAMPTZ NOT NULL,
    is_harvest          BOOLEAN NOT NULL DEFAULT false,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_report_items_report ON tax_report_line_items(report_id);
CREATE INDEX idx_report_items_user_year ON tax_report_line_items(user_id, disposed_at);
```

---

## 3. Domain Entities

### 3.1 TaxLot Entity

**File:** `internal/domain/entities/tax_lot_entities.go`

```go
package entities

import (
    "time"
    "github.com/google/uuid"
    "github.com/shopspring/decimal"
)

type TaxLotSource string

const (
    TaxLotSourceAutoInvest   TaxLotSource = "autoinvest"
    TaxLotSourceRoundup      TaxLotSource = "roundup"
    TaxLotSourceDRIP         TaxLotSource = "drip"
    TaxLotSourceManual       TaxLotSource = "manual"
    TaxLotSourceTLHReplace   TaxLotSource = "tlh_replacement"
)

type TaxLot struct {
    ID           uuid.UUID       `json:"id" db:"id"`
    UserID       uuid.UUID       `json:"user_id" db:"user_id"`
    Symbol       string          `json:"symbol" db:"symbol"`
    Quantity     decimal.Decimal `json:"quantity" db:"quantity"`
    CostBasis    decimal.Decimal `json:"cost_basis" db:"cost_basis"`
    AcquiredAt   time.Time       `json:"acquired_at" db:"acquired_at"`
    Source       TaxLotSource    `json:"source" db:"source"`
    OrderID      *uuid.UUID      `json:"order_id,omitempty" db:"order_id"`
    RemainingQty decimal.Decimal `json:"remaining_qty" db:"remaining_qty"`
    ClosedAt     *time.Time      `json:"closed_at,omitempty" db:"closed_at"`
    CreatedAt    time.Time       `json:"created_at" db:"created_at"`
}

// IsLongTerm returns true if held > 365 days
func (t *TaxLot) IsLongTerm() bool {
    return time.Since(t.AcquiredAt) > 365*24*time.Hour
}

// DaysUntilLongTerm returns days remaining, 0 if already long-term
func (t *TaxLot) DaysUntilLongTerm() int {
    threshold := t.AcquiredAt.Add(365 * 24 * time.Hour)
    if time.Now().After(threshold) {
        return 0
    }
    return int(time.Until(threshold).Hours() / 24)
}

// UnrealizedGain calculates gain/loss at a given market price
func (t *TaxLot) UnrealizedGain(currentPrice decimal.Decimal) decimal.Decimal {
    return currentPrice.Sub(t.CostBasis).Mul(t.RemainingQty)
}

// IsOpen returns true if the lot still has remaining shares
func (t *TaxLot) IsOpen() bool {
    return t.RemainingQty.GreaterThan(decimal.Zero) && t.ClosedAt == nil
}

// MarketValue returns current value at a given price
func (t *TaxLot) MarketValue(currentPrice decimal.Decimal) decimal.Decimal {
    return t.RemainingQty.Mul(currentPrice)
}

// TotalCostBasis returns total cost of remaining shares
func (t *TaxLot) TotalCostBasis() decimal.Decimal {
    return t.CostBasis.Mul(t.RemainingQty)
}
```

### 3.2 TaxHarvestEvent Entity

**File:** `internal/domain/entities/tax_harvest_entities.go`

```go
package entities

import (
    "time"
    "github.com/google/uuid"
    "github.com/shopspring/decimal"
)

type HarvestStatus string

const (
    HarvestStatusPending   HarvestStatus = "pending"
    HarvestStatusSold      HarvestStatus = "sold"
    HarvestStatusReplaced  HarvestStatus = "replaced"
    HarvestStatusCompleted HarvestStatus = "completed"
    HarvestStatusFailed    HarvestStatus = "failed"
)

type TaxHarvestEvent struct {
    ID                uuid.UUID       `json:"id" db:"id"`
    UserID            uuid.UUID       `json:"user_id" db:"user_id"`
    SoldLotID         uuid.UUID       `json:"sold_lot_id" db:"sold_lot_id"`
    SoldSymbol        string          `json:"sold_symbol" db:"sold_symbol"`
    SoldQuantity      decimal.Decimal `json:"sold_quantity" db:"sold_quantity"`
    SoldProceeds      decimal.Decimal `json:"sold_proceeds" db:"sold_proceeds"`
    CostBasisTotal    decimal.Decimal `json:"cost_basis_total" db:"cost_basis_total"`
    RealizedLoss      decimal.Decimal `json:"realized_loss" db:"realized_loss"`
    ReplacementSymbol string          `json:"replacement_symbol" db:"replacement_symbol"`
    ReplacementLotID  *uuid.UUID      `json:"replacement_lot_id,omitempty" db:"replacement_lot_id"`
    ReplacementAmount decimal.Decimal `json:"replacement_amount" db:"replacement_amount"`
    WashSaleClear     bool            `json:"wash_sale_clear" db:"wash_sale_clear"`
    Status            HarvestStatus   `json:"status" db:"status"`
    ExecutedAt        *time.Time      `json:"executed_at,omitempty" db:"executed_at"`
    CreatedAt         time.Time       `json:"created_at" db:"created_at"`
}
```

---

## 4. Repository Interfaces

### 4.1 TaxLotRepository

**File:** `internal/domain/repositories/tax_lot_repository.go`

```go
package repositories

import (
    "context"
    "time"
    "github.com/google/uuid"
    "github.com/shopspring/decimal"
    "github.com/rail-service/rail_service/internal/domain/entities"
)

type TaxLotRepository interface {
    // Write operations
    Create(ctx context.Context, lot *entities.TaxLot) error
    DecrementQuantity(ctx context.Context, lotID uuid.UUID, qty decimal.Decimal) error
    CloseLot(ctx context.Context, lotID uuid.UUID) error

    // Read operations — open lots
    GetOpenLotsByUser(ctx context.Context, userID uuid.UUID) ([]*entities.TaxLot, error)
    GetOpenLotsBySymbol(ctx context.Context, userID uuid.UUID, symbol string) ([]*entities.TaxLot, error)

    // Read operations — wash sale
    WasSymbolTradedRecently(ctx context.Context, userID uuid.UUID, symbol string, within time.Duration) (bool, error)
    RecordTradeEvent(ctx context.Context, userID uuid.UUID, symbol string, eventType string, lotID uuid.UUID) error

    // Read operations — reporting
    GetClosedLotsByDateRange(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]*entities.TaxLot, error)
    GetAllLotsByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.TaxLot, error)
}
```

### 4.2 TaxHarvestRepository

**File:** `internal/domain/repositories/tax_harvest_repository.go`

```go
package repositories

import (
    "context"
    "time"
    "github.com/google/uuid"
    "github.com/rail-service/rail_service/internal/domain/entities"
)

type TaxHarvestRepository interface {
    Create(ctx context.Context, event *entities.TaxHarvestEvent) error
    UpdateStatus(ctx context.Context, id uuid.UUID, status entities.HarvestStatus) error
    SetReplacementLot(ctx context.Context, id uuid.UUID, lotID uuid.UUID) error
    GetByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.TaxHarvestEvent, error)
    GetByDateRange(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]*entities.TaxHarvestEvent, error)
    SumRealizedLosses(ctx context.Context, userID uuid.UUID, taxYear int) (*entities.TaxHarvestSummary, error)
}
```

---

## 5. Service Layer

### 5.1 Tax Lot Tracker Service

**File:** `internal/domain/services/taxlot/service.go`

Responsible for creating lots on fills and closing lots on sells. This is the thinnest service — mostly CRUD with validation.

```go
package taxlot

type Service struct {
    lotRepo    TaxLotRepository
    logger     *logger.Logger
}

// CreateFromFill creates a tax lot when an order is filled
// Called by: autoinvest/service.go, roundup/service.go, investing/service.go
func (s *Service) CreateFromFill(ctx context.Context, req CreateLotRequest) (*entities.TaxLot, error)

// SelectLotsForSale returns lots sorted for tax-optimal selling
// Sort order: long-term first → highest cost basis first (least taxable gain)
func (s *Service) SelectLotsForSale(ctx context.Context, userID uuid.UUID, symbol string, targetAmount, currentPrice decimal.Decimal) ([]*entities.TaxLot, error)

// ConsumeLots decrements remaining_qty on selected lots for a sell order
func (s *Service) ConsumeLots(ctx context.Context, lots []*entities.TaxLot, totalQty decimal.Decimal) error

// GetHoldingPeriodAdvisory checks if any lots are near long-term threshold
// Returns advisory message if waiting would save taxes
func (s *Service) GetHoldingPeriodAdvisory(ctx context.Context, userID uuid.UUID, symbol string, sellAmount, currentPrice decimal.Decimal) (*HoldingAdvisory, error)

// GetUserLotSummary returns aggregated lot data for a user
func (s *Service) GetUserLotSummary(ctx context.Context, userID uuid.UUID) (*LotSummary, error)
```

**Key types:**

```go
type CreateLotRequest struct {
    UserID    uuid.UUID
    Symbol    string
    Quantity  decimal.Decimal
    CostBasis decimal.Decimal  // price per share
    Source    entities.TaxLotSource
    OrderID   *uuid.UUID
}

type HoldingAdvisory struct {
    HasNearTermLots  bool
    DaysToWait       int
    EstimatedSavings decimal.Decimal
    Message          string  // "Waiting 12 days saves ~$47 in taxes"
}

type LotSummary struct {
    TotalLots        int
    OpenLots         int
    LongTermLots     int
    ShortTermLots    int
    TotalCostBasis   decimal.Decimal
    TotalMarketValue decimal.Decimal
    UnrealizedGain   decimal.Decimal
    UnrealizedLoss   decimal.Decimal
}
```

### 5.2 Tax Optimizer Service (TLH)

**File:** `internal/domain/services/taxoptimizer/service.go`

Responsible for scanning for harvest candidates and executing harvest trades.

```go
package taxoptimizer

// Correlated ETF pairs for wash-sale-safe swaps
var HarvestPairs = map[string]string{
    "SPY": "VOO",   // S&P 500 (SPDR vs Vanguard)
    "VOO": "SPY",
    "QQQ": "VGT",   // Nasdaq-100 vs Tech sector
    "VGT": "QQQ",
    "BND": "AGG",   // Total bond (Vanguard vs iShares)
    "AGG": "BND",
    "VTI": "ITOT",  // Total US market (Vanguard vs iShares)
    "ITOT": "VTI",
    "VXUS": "IXUS", // International (Vanguard vs iShares)
    "IXUS": "VXUS",
}

type Config struct {
    MinLossThreshold   decimal.Decimal // Minimum loss to harvest (default $10)
    MinBalanceGate     decimal.Decimal // Min invest balance to be eligible (default $500)
    MaxHarvestsPerWeek int             // Rate limit per user (default 3)
    ScanBatchSize      int             // Users per worker batch (default 100)
}

type Service struct {
    lotRepo       TaxLotRepository
    harvestRepo   TaxHarvestRepository
    marketData    MarketDataProvider
    orderPlacer   OrderPlacer
    lotService    *taxlot.Service
    config        Config
    logger        *logger.Logger
}

// ScanForHarvest finds all harvestable lots for a user
func (s *Service) ScanForHarvest(ctx context.Context, userID uuid.UUID) ([]HarvestCandidate, error)

// ExecuteHarvest sells the losing lot and buys the replacement
// 1. Sell losing position via Alpaca
// 2. Close the tax lot
// 3. Buy replacement ETF with same dollar amount
// 4. Create new tax lot for replacement (source: tlh_replacement)
// 5. Record harvest event for audit trail
// 6. Record wash sale window entries for both symbols
func (s *Service) ExecuteHarvest(ctx context.Context, userID uuid.UUID, candidate HarvestCandidate) error

// GetHarvestSummary returns harvest stats for a user in a tax year
func (s *Service) GetHarvestSummary(ctx context.Context, userID uuid.UUID, taxYear int) (*HarvestSummary, error)
```

**Key types:**

```go
type HarvestCandidate struct {
    Lot              *entities.TaxLot
    CurrentPrice     decimal.Decimal
    UnrealizedLoss   decimal.Decimal  // negative value
    ReplacementSym   string
    EstimatedSavings decimal.Decimal  // loss × estimated tax rate
}

type HarvestSummary struct {
    TotalHarvests    int
    TotalLossHarvested decimal.Decimal
    EstimatedTaxSaved  decimal.Decimal
    HarvestEvents    []*entities.TaxHarvestEvent
}

type MarketDataProvider interface {
    GetCurrentPrice(ctx context.Context, symbol string) (decimal.Decimal, error)
}

type OrderPlacer interface {
    PlaceMarketOrder(ctx context.Context, userID uuid.UUID, symbol string, amount decimal.Decimal, clientOrderID string) (*entities.AlpacaOrderResponse, error)
}
```

### 5.3 Tax Reporter Service

**File:** `internal/domain/services/taxreporter/service.go`

Generates year-end tax reports from closed lots and harvest events.

```go
package taxreporter

type Service struct {
    lotRepo     TaxLotRepository
    harvestRepo TaxHarvestRepository
    marketData  MarketDataProvider
    logger      *logger.Logger
}

// GenerateAnnualReport creates a tax report for a user for a given tax year
// Aggregates: short-term gains, long-term gains, harvested losses, net position
func (s *Service) GenerateAnnualReport(ctx context.Context, userID uuid.UUID, taxYear int) (*TaxReport, error)

// GetTaxEstimate returns a real-time estimate of current year tax liability
func (s *Service) GetTaxEstimate(ctx context.Context, userID uuid.UUID) (*TaxEstimate, error)
```

**Key types:**

```go
type TaxReport struct {
    UserID          uuid.UUID
    TaxYear         int
    ShortTermGains  decimal.Decimal
    LongTermGains   decimal.Decimal
    HarvestedLosses decimal.Decimal
    NetGainLoss     decimal.Decimal
    LineItems       []TaxLineItem
    GeneratedAt     time.Time
}

type TaxLineItem struct {
    Symbol        string
    Quantity      decimal.Decimal
    CostBasis     decimal.Decimal
    Proceeds      decimal.Decimal
    GainLoss      decimal.Decimal
    HoldingPeriod string  // "short_term" or "long_term"
    AcquiredAt    time.Time
    DisposedAt    time.Time
    IsHarvest     bool
}

type TaxEstimate struct {
    UnrealizedShortTerm decimal.Decimal
    UnrealizedLongTerm  decimal.Decimal
    RealizedShortTerm   decimal.Decimal
    RealizedLongTerm    decimal.Decimal
    HarvestedLosses     decimal.Decimal
    EstimatedTaxOwed    decimal.Decimal
    PotentialSavings    decimal.Decimal  // from pending harvest candidates
}
```

---

## 6. Integration Hooks

These are the exact code changes needed in existing services.

### 6.1 Auto-Invest Fill → Create Tax Lot

**File:** `internal/domain/services/autoinvest/service.go`

After a successful `PlaceMarketOrder` call, when the fill is confirmed:

```go
// After order placement succeeds and fill data is available:
if taxLotService != nil {
    _, err := taxLotService.CreateFromFill(ctx, taxlot.CreateLotRequest{
        UserID:    req.UserID,
        Symbol:    symbol,
        Quantity:  filledQty,
        CostBasis: filledAvgPrice,
        Source:    entities.TaxLotSourceAutoInvest,
        OrderID:   &orderResp.OrderID,
    })
    if err != nil {
        s.logger.Error("Failed to create tax lot", "error", err, "user_id", req.UserID)
        // Non-blocking: lot creation failure shouldn't fail the investment
    }
}
```

**Dependency injection:** Add `taxLotService *taxlot.Service` to the `Service` struct and `NewService` constructor.

### 6.2 Round-Up Fill → Create Tax Lot

**File:** `internal/domain/services/roundup/service.go`

Same pattern, after round-up investment order fills:

```go
// After round-up investment order is placed and filled:
if s.taxLotService != nil {
    _, err := s.taxLotService.CreateFromFill(ctx, taxlot.CreateLotRequest{
        UserID:    userID,
        Symbol:    investSymbol,
        Quantity:  filledQty,
        CostBasis: filledAvgPrice,
        Source:    entities.TaxLotSourceRoundup,
        OrderID:   &orderID,
    })
    if err != nil {
        s.logger.Error("Failed to create roundup tax lot", "error", err)
    }
}
```

### 6.3 Sell Order → Smart Lot Selection

**File:** `internal/domain/services/investing/service.go`

In the `CreateOrder` method, when `req.Side == OrderSideSell`:

```go
// Before submitting sell to brokerage, select optimal lots:
if s.taxLotService != nil && req.Side == entities.OrderSideSell {
    // Get holding period advisory
    advisory, _ := s.taxLotService.GetHoldingPeriodAdvisory(ctx, userID, basketSymbol, amount, currentPrice)
    if advisory != nil && advisory.HasNearTermLots {
        order.TaxAdvisory = &advisory.Message
    }

    // Select lots for tax-optimal selling
    selectedLots, err := s.taxLotService.SelectLotsForSale(ctx, userID, basketSymbol, amount, currentPrice)
    if err != nil {
        s.logger.Warn("Tax lot selection failed, falling back to FIFO", "error", err)
    } else {
        order.SelectedLotIDs = extractLotIDs(selectedLots)
    }
}
```

After the sell fill is confirmed:

```go
// Consume the selected lots (decrement remaining_qty)
if s.taxLotService != nil && order.SelectedLotIDs != nil {
    s.taxLotService.ConsumeLots(ctx, order.SelectedLotIDs, filledQty)
}
```

### 6.4 Alpaca Event Processor → Lot Updates

**File:** `internal/domain/services/alpaca/event_processor.go`

When processing fill events from Alpaca webhooks, create/update lots:

```go
// In ProcessTradeEvent or similar webhook handler:
// If it's a fill for a buy order → create lot
// If it's a fill for a sell order → consume lots
```

---

## 7. Background Workers

### 7.1 Tax-Loss Harvesting Worker

**File:** `internal/workers/tax_harvest_worker.go`

```go
// Schedule: Weekly (Sunday night, before market open Monday)
// Scope: All users with invest balance > $500

type TaxHarvestWorker struct {
    taxOptimizer *taxoptimizer.Service
    userRepo     UserRepository
    logger       *logger.Logger
}

func (w *TaxHarvestWorker) Run(ctx context.Context) error {
    // 1. Get all eligible users (invest balance > threshold)
    // 2. For each user (batched):
    //    a. ScanForHarvest → get candidates
    //    b. Filter: skip if user already hit weekly harvest limit
    //    c. ExecuteHarvest for each candidate
    //    d. Log results
    // 3. Emit metrics: users_scanned, candidates_found, harvests_executed, total_loss_harvested
}
```

**Worker configuration:**

| Setting | Default | Description |
|---------|---------|-------------|
| Schedule | Weekly (Sunday 22:00 UTC) | Before Monday market open |
| Batch size | 100 users | Process in batches to limit memory |
| Max harvests per user per week | 3 | Rate limit to avoid excessive trading |
| Min loss threshold | $10 | Don't harvest tiny losses |
| Min invest balance | $500 | User must have meaningful portfolio |
| Timeout per user | 30 seconds | Prevent single user from blocking |

### 7.2 Tax Report Generation Worker

**File:** `internal/workers/tax_report_worker.go`

```go
// Schedule: Annually (January 15, after tax year closes)
// Also: On-demand via API

type TaxReportWorker struct {
    taxReporter *taxreporter.Service
    userRepo    UserRepository
    logger      *logger.Logger
}

func (w *TaxReportWorker) Run(ctx context.Context, taxYear int) error {
    // 1. Get all users who had closed lots in the tax year
    // 2. For each user: GenerateAnnualReport
    // 3. Store report
    // 4. Notify user that tax report is ready
}
```

---

## 8. API Endpoints

### 8.1 Tax Summary

```
GET /api/v1/investing/tax-summary
Authorization: Bearer <token>

Response:
{
    "data": {
        "current_year": {
            "short_term_gains": "245.50",
            "long_term_gains": "1200.00",
            "harvested_losses": "-380.25",
            "net_gain_loss": "1065.25",
            "estimated_tax_owed": "213.05"
        },
        "potential_savings": {
            "harvestable_losses": "-150.00",
            "lots_near_long_term": 3,
            "days_to_next_long_term": 12
        },
        "lifetime": {
            "total_harvested": "-1250.75",
            "estimated_total_saved": "312.69"
        }
    }
}
```

### 8.2 Tax Lots (Debug/Admin)

```
GET /api/v1/investing/tax-lots?status=open&limit=50&offset=0
Authorization: Bearer <token>

Response:
{
    "data": {
        "lots": [
            {
                "id": "uuid",
                "symbol": "VOO",
                "quantity": "2.5000",
                "cost_basis": "420.50",
                "acquired_at": "2025-06-15T10:30:00Z",
                "source": "autoinvest",
                "remaining_qty": "2.5000",
                "is_long_term": true,
                "days_until_long_term": 0,
                "unrealized_gain": "125.00"
            }
        ],
        "summary": {
            "total_lots": 47,
            "open_lots": 42,
            "long_term_lots": 18,
            "short_term_lots": 24
        }
    }
}
```

### 8.3 Harvest History

```
GET /api/v1/investing/tax-harvests?year=2026&limit=20
Authorization: Bearer <token>

Response:
{
    "data": {
        "harvests": [
            {
                "id": "uuid",
                "sold_symbol": "SPY",
                "replacement_symbol": "VOO",
                "realized_loss": "-45.20",
                "estimated_tax_saved": "11.30",
                "executed_at": "2026-03-15T22:00:00Z"
            }
        ],
        "year_summary": {
            "total_harvests": 8,
            "total_loss_harvested": "-380.25",
            "estimated_tax_saved": "95.06"
        }
    }
}
```

---

## 9. Sell Order Flow (Updated)

The complete sell flow with tax optimization integrated:

```
User initiates sell (or withdrawal from invest)
    │
    ▼
investing/service.go CreateOrder (side=sell)
    │
    ├─ 1. Validate basket, amount, position
    │
    ├─ 2. [NEW] Get holding period advisory
    │      └─ taxlot.GetHoldingPeriodAdvisory()
    │         └─ "Waiting 12 days saves ~$47"
    │
    ├─ 3. [NEW] Select tax-optimal lots
    │      └─ taxlot.SelectLotsForSale()
    │         Sort: long-term first → highest cost basis
    │
    ├─ 4. Submit sell to Alpaca (existing)
    │      └─ brokerageAPI.PlaceOrder()
    │
    ├─ 5. [NEW] On fill confirmation:
    │      └─ taxlot.ConsumeLots()
    │         └─ Decrement remaining_qty on selected lots
    │         └─ Close lots where remaining_qty = 0
    │
    └─ 6. [NEW] Record in wash sale window
           └─ lotRepo.RecordTradeEvent(sell)
```

---

## 10. Tax-Loss Harvesting Flow

```
Weekly TLH Worker triggers
    │
    ▼
For each eligible user (invest balance > $500):
    │
    ├─ 1. taxoptimizer.ScanForHarvest(userID)
    │      │
    │      ├─ Get all open tax lots
    │      ├─ For each lot:
    │      │   ├─ Get current market price
    │      │   ├─ Calculate unrealized gain/loss
    │      │   ├─ If loss > $10 threshold:
    │      │   │   ├─ Look up replacement symbol (HarvestPairs)
    │      │   │   ├─ Check wash sale window (30 days)
    │      │   │   └─ If clear → add to candidates
    │      │   └─ If gain → skip
    │      └─ Return sorted candidates (biggest loss first)
    │
    ├─ 2. For each candidate (up to 3 per week):
    │      │
    │      └─ taxoptimizer.ExecuteHarvest(userID, candidate)
    │          │
    │          ├─ a. Create TaxHarvestEvent (status: pending)
    │          ├─ b. Sell losing position via Alpaca
    │          │      └─ orderPlacer.PlaceMarketOrder(sell)
    │          ├─ c. Close the tax lot
    │          │      └─ lotRepo.CloseLot()
    │          ├─ d. Record wash sale event (sell of original symbol)
    │          ├─ e. Buy replacement with same dollar amount
    │          │      └─ orderPlacer.PlaceMarketOrder(buy)
    │          ├─ f. Create new TaxLot (source: tlh_replacement)
    │          ├─ g. Record wash sale event (buy of replacement symbol)
    │          └─ h. Update harvest event (status: completed)
    │
    └─ 3. Emit metrics
           ├─ rail_tlh_scanned_users_total
           ├─ rail_tlh_candidates_found_total
           ├─ rail_tlh_harvests_executed_total
           └─ rail_tlh_loss_harvested_dollars_total
```

---

## 11. Wash Sale Compliance

### 11.1 The Rule

IRS wash sale rule: Cannot claim a loss if you buy a "substantially identical" security within 30 days before or after the sale.

### 11.2 How Rail Complies

1. **Different-provider ETFs:** SPY (SPDR) and VOO (Vanguard) both track the S&P 500 but are issued by different companies. The IRS does not consider these "substantially identical."

2. **30-day window tracking:** Every buy and sell is recorded in `wash_sale_windows`. Before any harvest, the system checks:
   ```sql
   SELECT COUNT(*) FROM wash_sale_windows
   WHERE user_id = $1
     AND symbol = $2  -- the REPLACEMENT symbol
     AND event_date > NOW() - INTERVAL '30 days'
   ```
   If count > 0, the harvest is skipped.

3. **Post-harvest lockout:** After a harvest, both the original and replacement symbols are locked from trading for 30 days (recorded in wash_sale_windows).

4. **Zero tolerance:** Any wash sale violation is logged as a critical alert. The worker pauses for the affected user.

### 11.3 Edge Cases

| Scenario | Handling |
|----------|----------|
| User manually buys SPY, then TLH tries to sell SPY | Check wash_sale_windows — if SPY was bought in last 30 days, skip |
| TLH sells SPY→VOO, then user buys SPY within 30 days | The original SPY sell's loss deduction is disallowed. Record and flag. |
| Multiple lots of same symbol, some at loss, some at gain | Only harvest the losing lots. Gaining lots are untouched. |
| Replacement symbol also has losing lots | Harvest both in sequence, respecting the 30-day window between them |

---

## 12. Project Structure (New Files)

```
internal/domain/
├── entities/
│   ├── tax_lot_entities.go          # TaxLot, TaxLotSource
│   └── tax_harvest_entities.go      # TaxHarvestEvent, HarvestStatus
├── repositories/
│   ├── tax_lot_repository.go        # TaxLotRepository interface
│   └── tax_harvest_repository.go    # TaxHarvestRepository interface
└── services/
    ├── taxlot/
    │   └── service.go               # Lot creation, smart selling, advisory
    ├── taxoptimizer/
    │   └── service.go               # TLH scanning and execution
    └── taxreporter/
        └── service.go               # Year-end tax report generation

internal/infrastructure/repositories/
├── tax_lot_repo.go                  # PostgreSQL implementation
└── tax_harvest_repo.go              # PostgreSQL implementation

internal/workers/
├── tax_harvest_worker.go            # Weekly TLH scanner
└── tax_report_worker.go             # Annual report generator

internal/api/handlers/
└── tax_handler.go                   # API endpoints for tax data

migrations/
├── XXXX_create_tax_lots.up.sql
├── XXXX_create_tax_lots.down.sql
├── XXXX_create_tax_harvest_events.up.sql
├── XXXX_create_tax_harvest_events.down.sql
├── XXXX_create_wash_sale_windows.up.sql
├── XXXX_create_wash_sale_windows.down.sql
├── XXXX_create_tax_report_details.up.sql
└── XXXX_create_tax_report_details.down.sql
```

---

## 13. Dependency Injection Updates

**File:** `internal/infrastructure/di/` (or wherever DI is configured)

New dependencies to wire:

```go
// Repositories
taxLotRepo := repositories.NewTaxLotRepository(db)
taxHarvestRepo := repositories.NewTaxHarvestRepository(db)

// Services
taxLotService := taxlot.NewService(taxLotRepo, logger)
taxOptimizerService := taxoptimizer.NewService(
    taxLotRepo, taxHarvestRepo, marketDataService, alpacaOrderPlacer, taxLotService,
    taxoptimizer.DefaultConfig(), logger,
)
taxReporterService := taxreporter.NewService(taxLotRepo, taxHarvestRepo, marketDataService, logger)

// Inject into existing services
autoInvestService.SetTaxLotService(taxLotService)
roundupService.SetTaxLotService(taxLotService)
investingService.SetTaxLotService(taxLotService)

// Workers
taxHarvestWorker := workers.NewTaxHarvestWorker(taxOptimizerService, userRepo, logger)
taxReportWorker := workers.NewTaxReportWorker(taxReporterService, userRepo, logger)
```

---

## 14. Metrics & Observability

### Prometheus Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `rail_tax_lots_created_total` | Counter | Tax lots created (by source label) |
| `rail_tax_lots_closed_total` | Counter | Tax lots closed (by reason: sell, harvest) |
| `rail_tlh_scans_total` | Counter | TLH scan executions |
| `rail_tlh_candidates_found` | Gauge | Harvest candidates found in last scan |
| `rail_tlh_harvests_executed_total` | Counter | Successful harvests |
| `rail_tlh_harvests_failed_total` | Counter | Failed harvests |
| `rail_tlh_loss_harvested_dollars` | Counter | Total dollar losses harvested |
| `rail_wash_sale_blocks_total` | Counter | Harvests blocked by wash sale rule |
| `rail_smart_sell_long_term_pct` | Histogram | % of sell value from long-term lots |

### Alerts

| Alert | Condition | Severity |
|-------|-----------|----------|
| Wash sale violation | Any wash_sale_clear = false | Critical |
| TLH worker failure | Worker fails 2+ consecutive runs | High |
| Lot tracking gap | Fill event without corresponding lot creation | High |
| Harvest execution failure rate | > 5% of harvests fail | Medium |

---

## 15. Testing Strategy

### Unit Tests

| Test | Location | Coverage |
|------|----------|----------|
| `TaxLot.IsLongTerm()` with various dates | `entities/tax_lot_entities_test.go` | Entity logic |
| `TaxLot.UnrealizedGain()` gain and loss | `entities/tax_lot_entities_test.go` | Entity logic |
| `SelectLotsForSale` ordering (long-term first, highest basis) | `services/taxlot/service_test.go` | Core algorithm |
| `SelectLotsForSale` partial lot consumption | `services/taxlot/service_test.go` | Edge case |
| `ScanForHarvest` with mixed gain/loss lots | `services/taxoptimizer/service_test.go` | TLH logic |
| `ScanForHarvest` wash sale blocking | `services/taxoptimizer/service_test.go` | Compliance |
| Harvest pair mapping completeness | `services/taxoptimizer/service_test.go` | Config |

### Integration Tests

| Test | Description |
|------|-------------|
| Full autoinvest → lot creation flow | Deposit → split → autoinvest → fill → verify lot exists |
| Full sell → lot consumption flow | Create lots → sell order → verify lots decremented |
| TLH end-to-end | Create losing lots → run scanner → verify harvest event + new lot |
| Wash sale enforcement | Create recent trade → attempt harvest → verify blocked |
| Tax report generation | Create mixed lots → close some → generate report → verify totals |

---

## 16. Rollout Plan

### Phase 1: Silent Lot Tracking (Week 1-2)

- Deploy tax_lots migration
- Hook lot creation into autoinvest + roundup fills
- **No user-facing changes.** Just start recording.
- Monitor: `rail_tax_lots_created_total` should match fill events

### Phase 2: Smart Selling (Week 3)

- Deploy lot selection logic in investing service
- Add holding period advisory to sell responses
- **User-facing:** Advisory messages on sells near long-term threshold
- Monitor: `rail_smart_sell_long_term_pct`

### Phase 3: Tax-Loss Harvesting (Week 4-5)

- Deploy harvest events + wash sale tables
- Deploy TLH worker (initially: scan only, no execution)
- **Week 1:** Dry-run mode — log candidates but don't execute
- **Week 2:** Enable execution for internal test accounts
- **Week 3:** Gradual rollout (10% → 50% → 100%)
- Monitor: All TLH metrics + wash sale blocks

### Phase 4: Reporting & API (Week 6)

- Deploy tax report generation
- Deploy API endpoints
- **User-facing:** Tax summary visible in app
- Monitor: Report accuracy vs Alpaca 1099 data

---

## Related Documents

| Document | Description |
|----------|-------------|
| [Wealth Engine](../WEALTH_ENGINE.md) | Philosophy, wealth rules, feature overview |
| [System Design](system-design.md) | Overall Rail architecture |
| [PRD](../prd.md) | Product requirements |
