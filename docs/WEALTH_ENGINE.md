# Rail Wealth Engine

> **Money should start working the moment it arrives. And it should never stop working — not for taxes, not for fees, not for bad timing.**

---

## Table of Contents

- [Philosophy](#philosophy)
- [The Problem We're Solving](#the-problem-were-solving)
- [Wealth Rules](#wealth-rules)
- [Feature Overview](#feature-overview)
  - [1. Tax-Lot Tracking](#1-tax-lot-tracking)
  - [2. Holding Period Awareness](#2-holding-period-awareness)
  - [3. Tax-Loss Harvesting](#3-tax-loss-harvesting)
  - [4. Smart Sell Ordering](#4-smart-sell-ordering)
  - [5. Round-Up Lot Optimization](#5-round-up-lot-optimization)
  - [6. Income-Aware Gain Realization](#6-income-aware-gain-realization)
  - [7. Borrow Against Portfolio](#7-borrow-against-portfolio)
  - [8. Charitable Stock Donation](#8-charitable-stock-donation)
- [How the Rich Structure Wealth](#how-the-rich-structure-wealth)
- [Implementation Roadmap](#implementation-roadmap)
- [Success Metrics](#success-metrics)

---

## Philosophy

Rail's 70/30 split already embodies the #1 wealth-building behavior: **pay yourself first**. Most people spend first and save what's left. Rail inverts this by default.

But getting money into investments is only half the equation. The wealthy don't just invest — they **structure**. They minimize taxes, optimize holding periods, harvest losses, and borrow against assets instead of selling them. These strategies used to require a $500/hr financial advisor. Rail automates them for everyone.

### Core Beliefs

1. **Automation beats discipline.** Users shouldn't need willpower to build wealth.
2. **Tax optimization is free money.** Every dollar saved in taxes compounds forever.
3. **Time in market beats timing the market.** The system should discourage premature selling.
4. **Complexity should be invisible.** The user sees "your money is working." The system handles the rest.

---

## The Problem We're Solving

The average retail investor loses 1-2% annually to avoidable taxes. Over a 30-year investing horizon, that compounds into a massive wealth gap:

| Scenario | $10K invested, 8% annual return, 30 years |
|----------|-------------------------------------------|
| No tax optimization | ~$100,627 |
| With tax optimization (~1.5% saved annually) | ~$132,677 |
| **Difference** | **~$32,050 (32% more wealth)** |

The wealthy have always had access to these strategies through private wealth managers. Rail democratizes them through automation.

---

## Wealth Rules

These are the automated rules Rail enforces on behalf of every user. No configuration. No decisions. Just better outcomes.

### Rule 1: The 70/30 Split (Existing)

Every deposit is split 70% spending / 30% investing before the user can touch it. This is the "pay yourself first" rule — automated and non-negotiable.

**Status:** ✅ Implemented (`allocation/service.go`)

### Rule 2: Never Sell Short-Term If You Can Avoid It

Long-term capital gains (assets held > 1 year) are taxed at roughly half the short-term rate. When a user sells, Rail should always sell the oldest, long-term lots first.

**Status:** 🔲 To be implemented (Tax-Lot Tracking + Smart Sell Ordering)

### Rule 3: Harvest Losses Automatically

If a position is underwater, sell it to realize the loss (tax deduction), then immediately buy a similar-but-not-identical asset to maintain market exposure. The user's portfolio barely changes, but their tax bill shrinks.

**Status:** 🔲 To be implemented (Tax-Loss Harvesting)

### Rule 4: Every Purchase Creates a Record

Every share bought — whether from auto-invest, round-ups, or DRIP — gets tracked as an individual tax lot with its purchase date and cost basis. This is the foundation for all tax optimization.

**Status:** 🔲 To be implemented (Tax-Lot Tracking)

### Rule 5: Small Lots Are Harvesting Fuel

Round-up purchases create many small lots at different prices. At any given time, some will be underwater. These are perfect candidates for tax-loss harvesting — micro-losses that add up across hundreds of transactions.

**Status:** 🔲 To be implemented (Round-Up Lot Optimization)

### Rule 6: Realize Gains When Taxes Are Low

If a user's deposit patterns suggest lower income (fewer/smaller deposits), proactively realize some gains while they're in a lower tax bracket.

**Status:** 🔲 Future phase (Income-Aware Gain Realization)

### Rule 7: Don't Sell — Borrow

Once a user's portfolio reaches a threshold, offer a credit line backed by their investments. Spending from a credit line isn't a taxable event. Selling is.

**Status:** 🔲 Future phase (Borrow Against Portfolio)

---

## Feature Overview

### 1. Tax-Lot Tracking

**What it does:** Records every share purchase as an individual "lot" with its cost basis, acquisition date, and source (auto-invest, round-up, DRIP).

**Why it matters:** Without lot-level tracking, you can't do tax-loss harvesting, smart selling, or accurate tax reporting. This is the foundation.

**How it works in Rail:**
- When `autoinvest/service.go` receives a fill from Alpaca → create a `TaxLot` with source `autoinvest`
- When `roundup/service.go` receives a fill → create a `TaxLot` with source `roundup`
- When DRIP reinvests dividends → create a `TaxLot` with source `drip`
- Each lot tracks: symbol, quantity, cost basis (price per share), acquired date, remaining quantity

**User experience:** Invisible. No UI. The system just records everything.

**Priority:** 🔴 Critical — blocks all other wealth features

---

### 2. Holding Period Awareness

**What it does:** Tracks how long each lot has been held and uses this information to minimize taxes on sells.

**Why it matters:** The difference between short-term and long-term capital gains tax is significant:

| Holding Period | Tax Rate (typical) |
|---------------|-------------------|
| < 1 year (short-term) | Ordinary income rate (up to 37%) |
| > 1 year (long-term) | 0%, 15%, or 20% depending on income |

**How it works in Rail:**
- Each `TaxLot` has an `acquired_at` timestamp
- `IsLongTerm()` method: returns true if held > 365 days
- `DaysUntilLongTerm()` method: returns days remaining until the lot qualifies
- When a user initiates a sell, the system checks if any lots are within 30 days of becoming long-term and includes an advisory in the response

**User experience:** If a user tries to withdraw from invest and some lots are close to long-term:
> "Waiting 12 more days would save you ~$47 in taxes on this sale."

**Priority:** 🔴 Critical — ships with tax-lot tracking

---

### 3. Tax-Loss Harvesting

**What it does:** Automatically sells losing positions to realize tax deductions, then immediately buys a correlated replacement asset to maintain market exposure.

**Why it matters:** The IRS allows you to deduct investment losses against gains (and up to $3,000 against ordinary income per year). The key insight: you can sell a losing position, claim the loss, and buy a *similar but not identical* asset to stay invested.

**How it works in Rail:**

```
Weekly scan (all users with invest balance > $500)
    │
    ├─ For each user's open tax lots:
    │   ├─ Get current market price
    │   ├─ Calculate unrealized gain/loss
    │   ├─ If loss > threshold ($10):
    │   │   ├─ Find replacement ETF (SPY↔VOO, QQQ↔VGT, BND↔AGG)
    │   │   ├─ Check wash sale window (no buy/sell of replacement in last 30 days)
    │   │   ├─ If clear: sell losing lot → buy replacement
    │   │   └─ Create new TaxLot for replacement
    │   └─ If gain: skip (we want to keep winners)
    │
    └─ Record harvest event for tax reporting
```

**Wash Sale Rule Compliance:**
The IRS wash sale rule prohibits claiming a loss if you buy a "substantially identical" security within 30 days before or after the sale. Rail handles this by:
1. Using different-provider ETFs that track similar indices (SPY vs VOO — both S&P 500, different issuers)
2. Tracking a 30-day window per symbol per user
3. Never harvesting if the replacement was recently traded

**Harvest pairs:**

| Losing Position | Replacement | Both Track |
|----------------|-------------|------------|
| SPY (SPDR) | VOO (Vanguard) | S&P 500 |
| VOO (Vanguard) | SPY (SPDR) | S&P 500 |
| QQQ (Invesco) | VGT (Vanguard) | Tech/Growth |
| VGT (Vanguard) | QQQ (Invesco) | Tech/Growth |
| BND (Vanguard) | AGG (iShares) | Total Bond Market |
| AGG (iShares) | BND (Vanguard) | Total Bond Market |

**User experience:** Completely invisible. User's portfolio exposure stays the same. Their tax bill shrinks.

**Priority:** 🟡 High — biggest tax impact, requires tax-lot tracking first

---

### 4. Smart Sell Ordering

**What it does:** When a user sells (withdrawal from invest), the system selects which lots to sell to minimize tax impact.

**Why it matters:** Default brokerage behavior is FIFO (first in, first out). This is often the worst tax outcome. Smart ordering can save significant taxes.

**Sell priority order:**
1. **Long-term lots with highest cost basis** — smallest gain, lowest tax rate
2. **Long-term lots with lower cost basis** — bigger gain but still low tax rate
3. **Short-term lots with highest cost basis** — smallest gain
4. **Short-term lots with lower cost basis** — last resort (biggest tax hit)

**How it works in Rail:**
- Intercept sell orders in `investing/service.go` `CreateOrder`
- Query all open `TaxLot` records for the symbol being sold
- Sort by: long-term first → then by highest cost basis (least taxable gain)
- Sell lots in that order until the target amount is reached
- Close or partially close the selected lots

**User experience:** Invisible. The system just sells the smartest lots automatically.

**Priority:** 🟡 High — immediate value on every sell

---

### 5. Round-Up Lot Optimization

**What it does:** Leverages the many small lots created by round-ups as tax-loss harvesting fuel.

**Why it matters:** Round-ups create dozens of tiny purchases at different prices throughout the month. At any given time, many of these will be underwater (bought at a local high). These micro-losses are perfect harvesting candidates because:
- There are many of them (high frequency)
- They're small enough that selling doesn't meaningfully change portfolio allocation
- The losses add up across hundreds of transactions per year

**How it works in Rail:**
- Round-up lots are tagged with source `roundup` in the tax lots table
- The tax-loss harvesting scanner prioritizes round-up lots for harvesting (smaller lots = less portfolio disruption)
- Harvested round-up lots are replaced with the same dollar amount in the replacement ETF

**User experience:** Invisible. Round-ups silently become a tax optimization tool.

**Priority:** 🟢 Medium — enhances tax-loss harvesting, no additional infrastructure

---

### 6. Income-Aware Gain Realization

**What it does:** Proactively realizes capital gains during low-income periods when the user would pay less tax.

**Why it matters:** Capital gains tax rates depend on total annual income. If a user's income drops (job change, gap year, student), they may be in the 0% long-term capital gains bracket. Rail can "fill up" that bracket by selling winners and immediately rebuying — resetting the cost basis higher.

**How it works in Rail:**
- Monitor deposit patterns as a proxy for income (fewer/smaller deposits = likely lower income)
- If deposits drop significantly (>50% reduction over 3+ months), flag the user for gain realization
- Calculate how much gain can be realized in the 0% bracket
- Sell and immediately rebuy to reset cost basis
- New lots start with a higher cost basis = less tax on future sells

**User experience:** Optional notification: "Your income looks lower this year. Rail moved some gains into a lower tax bracket, saving you ~$X."

**Priority:** 🔵 Future — requires income inference model

---

### 7. Borrow Against Portfolio

**What it does:** Offers a credit line backed by the user's investment portfolio, allowing spending without selling (and without triggering capital gains).

**Why it matters:** This is literally how billionaires fund their lifestyle. Instead of selling $100K of stock (triggering ~$15K in taxes), they borrow $100K against it at 5-7% interest. The math works because:
- Investments grow at ~8-10% annually
- Loan interest is 5-7%
- No capital gains tax triggered
- Net result: more wealth preserved

**How it works in Rail:**
- Once invest balance exceeds threshold (e.g., $5,000), offer a credit line at 50-70% LTV
- User spends from credit line instead of selling
- Interest accrues but is far less than capital gains tax would be
- If portfolio drops below maintenance margin, auto-repay from spending balance

**User experience:** "You have $3,500 available to spend without selling your investments."

**Priority:** 🔵 Future — requires lending partner integration

---

### 8. Charitable Stock Donation

**What it does:** When a user wants to donate, transfers the most appreciated stock directly instead of selling → donating cash.

**Why it matters:** Donating appreciated stock provides a double tax benefit:
1. Deduct the full market value of the stock (not just what you paid)
2. Never pay capital gains on the appreciation

Example: Stock bought at $50, now worth $100.
- Sell then donate cash: Pay ~$7.50 capital gains tax, donate $92.50, deduct $92.50
- Donate stock directly: Pay $0 tax, donate $100 of value, deduct $100

**How it works in Rail:**
- Identify the most appreciated lots (biggest gap between cost basis and current price)
- Transfer shares directly to the charity's brokerage account via Alpaca
- Close the tax lots without realizing a gain
- Generate donation receipt for tax filing

**User experience:** "Donate $100 of investments. Rail will pick the shares that save you the most in taxes."

**Priority:** 🔵 Future — requires charity brokerage integration

---

## How the Rich Structure Wealth

A summary of the strategies Rail automates, mapped to what wealthy individuals do manually:

| What the Rich Do | How They Do It | How Rail Automates It |
|-------------------|---------------|----------------------|
| Pay themselves first | Auto-transfer to investment accounts | 70/30 split on every deposit |
| Track every lot | Accountants maintain detailed records | Tax-lot tracking on every fill |
| Never sell short-term | Advisors flag holding periods | Smart sell ordering (long-term first) |
| Harvest losses | Tax advisors scan quarterly | Automated weekly TLH scanner |
| Borrow, don't sell | Portfolio-backed credit lines | Credit line against invest balance |
| Donate stock, not cash | Wealth managers coordinate transfers | Automated most-appreciated-lot selection |
| Realize gains in low brackets | CPAs plan year-end transactions | Income-aware gain realization |
| Reinvest everything | DRIP + dividend reinvestment | Auto-DRIP with lot tracking |

---

## Implementation Roadmap

### Phase 1: Foundation (Weeks 1-2)

| Task | Effort | Depends On |
|------|--------|-----------|
| Create `tax_lots` table + migration | 1 day | Nothing |
| Create `TaxLot` entity + repository interface | 1 day | Migration |
| Create `TaxLotRepository` implementation | 1 day | Entity |
| Hook lot creation into `autoinvest/service.go` on fill | 0.5 day | Repository |
| Hook lot creation into `roundup/service.go` on fill | 0.5 day | Repository |
| Hook lot creation into DRIP reinvestment | 0.5 day | Repository |
| Add `IsLongTerm()` / `DaysUntilLongTerm()` methods | 0.5 day | Entity |
| Add holding period advisory to sell response | 1 day | Entity + investing service |

**Deliverable:** Every share purchase creates a tax lot. Sell orders include holding period advisories.

### Phase 2: Smart Selling (Week 3)

| Task | Effort | Depends On |
|------|--------|-----------|
| Implement `SelectLotsForSale` (long-term first, highest cost basis) | 1 day | Phase 1 |
| Integrate into `investing/service.go` sell path | 1 day | SelectLotsForSale |
| Partial lot closing (sell part of a lot) | 0.5 day | Lot closing logic |
| Tax savings estimate calculation | 0.5 day | Lot selection |
| Unit tests for lot selection ordering | 1 day | All above |

**Deliverable:** Every sell order automatically picks the most tax-efficient lots.

### Phase 3: Tax-Loss Harvesting (Weeks 4-5)

| Task | Effort | Depends On |
|------|--------|-----------|
| Create `taxoptimizer` service package | 0.5 day | Phase 1 |
| Implement harvest pair mapping (SPY↔VOO, etc.) | 0.5 day | Service |
| Implement `ScanForHarvest` — find losing lots | 1 day | Service + market data |
| Implement wash sale window check (30-day lookback) | 1 day | TaxLot repo |
| Implement `ExecuteHarvest` — sell + buy replacement | 1 day | Order placer |
| Create `tax_harvest_events` table for audit trail | 0.5 day | Migration |
| Create scheduled worker (weekly scan) | 1 day | Service |
| Add minimum loss threshold ($10) and balance gate ($500) | 0.5 day | Worker |
| Integration tests with mock Alpaca | 1 day | All above |

**Deliverable:** Weekly automated tax-loss harvesting for all eligible users.

### Phase 4: Reporting & Visibility (Week 6)

| Task | Effort | Depends On |
|------|--------|-----------|
| Populate existing `TaxReport` entity from closed lots | 1 day | Phase 1-3 |
| Calculate short-term vs long-term gains from lot data | 0.5 day | TaxReport |
| Year-end tax summary generation worker | 1 day | Calculations |
| API endpoint: `GET /api/v1/investing/tax-summary` | 0.5 day | Worker |
| API endpoint: `GET /api/v1/investing/tax-lots` (admin/debug) | 0.5 day | Repository |

**Deliverable:** Users can see their tax savings. Year-end reports generated automatically.

### Future Phases

| Phase | Feature | Estimated Effort | Dependency |
|-------|---------|-----------------|------------|
| 5 | Income-aware gain realization | 2 weeks | Deposit pattern analysis |
| 6 | Borrow against portfolio | 4-6 weeks | Lending partner integration |
| 7 | Charitable stock donation | 2 weeks | Charity brokerage integration |

---

## Success Metrics

### Primary KPIs

| Metric | Target | Measurement |
|--------|--------|-------------|
| Tax losses harvested per user per year | > $200 | Sum of realized losses from TLH |
| % of sells using long-term lots | > 80% | Smart sell ordering effectiveness |
| Average tax savings per user per year | > $150 | Estimated from lot selection + TLH |
| Tax lot coverage | 100% | Every fill creates a lot |

### Secondary KPIs

| Metric | Description |
|--------|-------------|
| Harvest frequency | Average harvests per user per year |
| Wash sale violations | Should be 0 (compliance) |
| Lots near long-term threshold | Users advised to wait before selling |
| Round-up lots harvested | % of round-up lots used for TLH |

### Guardrails

| Guardrail | Threshold | Action |
|-----------|-----------|--------|
| Wash sale violation | 0 tolerance | Block harvest, alert engineering |
| Harvest execution failure | > 5% failure rate | Pause worker, investigate |
| Lot tracking gap | Any fill without a lot | Alert, backfill |
| Tax report accuracy | Must match Alpaca 1099 | Reconciliation check |

---

## Positioning

> "Rail doesn't just invest your money — it structures your wealth the way the rich do. Automated tax optimization, smart holding periods, and loss harvesting that used to require a $500/hr financial advisor. You do nothing. Rail handles it."

The 70/30 split gets people in the door. **Tax optimization is what makes them wealthy over 10 years.** That's the real product.

---

## Related Documents

| Document | Description |
|----------|-------------|
| [Tax Optimization System Design](architecture/TAX_OPTIMIZATION_SYSTEM_DESIGN.md) | Technical implementation spec — entities, services, migrations, workers, API |
| [System Design](architecture/system-design.md) | Overall Rail architecture |
| [PRD](prd.md) | Product requirements |
| [Rail Brief](Rail-Brief.md) | Product philosophy and vision |
