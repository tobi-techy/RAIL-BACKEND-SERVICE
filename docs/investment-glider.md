# Rail × Glider Investment Infrastructure

**Audience:** backend engineers, product, security review  
**Status:** Milestone 1 — vertical slice compiling, tested, and wired to Miriam (production-ready vertical slice)  
**Last updated:** 2026-09-15

---

## 1. Purpose

This document describes the production-grade investment infrastructure that connects **Rail** (the rules-based capital engine) to **Glider** (the Solana-based portfolio automation provider) and exposes it to **Miriam**, the conversational Python agent, across multiple messaging channels.

The infrastructure is intentionally scoped: every mutation the agent *wants* to perform is staged behind a **payload-bound confirmation token**. The Go backend is the sole authority on whether an action proceeds — Miriam proposes, Go decides.

---

## 2. Architecture Snapshot

```
┌─────────────────────────────────────────────────────────────────┐
│                     RAIL BACKEND (Go)                           │
│  ┌─────────────┐ ┌──────────────┐ ┌─────────────┐ ┌───────────┐ │
│  │   Domain    │ │  Engines     │ │  Services   │ │  Worker   │ │
│  │  Entities   │ │ (Validator,  │ │ (Strategy,  │ │ (Poll +   │ │
│  │  + Repos    │ │  Policy,     │ │  Enrollment,│ │  Rebalance│ │
│  │  interfaces)│ │  Previewer)  │ │  Execution, │ │  Sweep)   │ │
│  └──────┬──────┘ └──────┬───────┘ └──────┬──────┘ └────┬─────┘ │
│         │               │                │             │       │
│  ┌──────┴───────────────┴────────────────┴─────────────┴─────┐ │
│  │              Infrastructure Layer                            │ │
│  │  • Glider HTTP client (+ simulated in-process provider)     │ │
│  │  • Owner signer port (Derived for M1, Circle for prod)      │ │
│  │  • Postgres repositories (strategies, enrollments, etc.)    │ │
│  │  • Funding adapter (reuses WithdrawalService)               │ │
│  └──────────────────────────┬──────────────────────────────────┘ │
│                             │                                    │
│  ┌──────────────────────────┴──────────────────────────────────┐ │
│  │              Agent API (Gin)                                 │ │
│  │  • GET  /api/v1/investments/*            (reads, discovery) │ │
│  │  • POST /api/v1/investments/strategies   (create, version)  │ │
│  │  • POST /api/v1/investments/enroll       (two-stage + fund) │ │
│  │  • POST /api/v1/investments/orders       (buy/sell)         │ │
│  │  • POST /api/v1/investments/allocations  (set allocation)   │ │
│  │  • POST /api/v1/investments/withdrawals  (step-up + passcode)│ │
│  └──────────────────────────┬──────────────────────────────────┘ │
└─────────────────────────────│────────────────────────────────────┘
                              │
              ┌───────────────┴───────────────┐
              ▼                               ▼
       ┌──────────────┐                ┌──────────────┐
       │   MIRIAM     │                │   GLIDER     │
       │  (Python)    │                │  (Provider)  │
       │  • Tools     │                │  • Strategies│
       │  • Replay    │                │  • Portfolios│
       │  • Safety    │                │  • Operations│
       └──────────────┘                └──────────────┘
```

---

## 3. Core Domain Entities

| Entity | Purpose | Key Fields |
|--------|---------|------------|
| `InvestmentStrategy` | User or public template | `ID`, `OwnerType` (user/public), `Status`, `CurrentVersion`, `GliderStrategyID` |
| `InvestmentStrategyVersion` | Immutable allocation snapshot | `Version`, `TargetAllocation` (weights sum to 100), `Risk/Horizon`, `RebalanceRules` |
| `InvestmentEnrollment` | User's mirror of a strategy | `UserID`, `StrategyID`, `GliderPortfolioID`, `Chain`, `DepositAccountID`, `Status` |
| `InvestmentHolding` | Position inside a portfolio | `EnrollmentID`, `AssetID`, `Symbol`, `Balance`, `ValueUSD`, `Weight` |
| `InvestmentExecution` | Auditable record of every trade/rebalance | `Kind` (trade/rebalance/withdraw), `Side`, `Status`, `ProviderOperationID`, `ConfirmationMethod` |
| `InvestmentFundingTransfer` | Money movement (deposit/withdrawal) | `Direction`, `AmountUSD`, `Status`, `IdempotencyKey`, `LedgerTransactionID` |
| `InvestmentConfirmation` | Payload-bound token for staged mutations | `Token`, `ActionHash`, `Payload` (JSON), `ExpiresAt`, `ConsumedAt` |
| `GliderOperation` | Async provider operation handle | `OperationID`, `State` (pending/completed/failed), `ProviderPayload` |

**Enums** (never fabricated): `StrategyStatus`, `EnrollmentStatus`, `ExecutionKind`, `ExecutionStatus`, `Actor` (User/Miriam/Worker/System), `Verdict`, `Provenance` (VERIFIED/INFERRED/UNAVAILABLE).

---

## 4. Deterministic Engines

### 4.1 Validator (`engines.Validator`)
- **Weight sum = 100** (exact, ≤ 2 decimal places)
- **1–30 legs**, asset must resolve to allowlisted asset
- **Position cap**: ≤ `MaxPositionPct` (default 25%) per leg
- **Strategy cap**: ≤ `MaxStrategyPct` of total portfolio
- **Cash reserve**: ≥ `MinCashReservePct`
- **Daily/Per-tx caps**: `MaxDailyVolumeUSD`, `MaxTransactionUSD`
- **Normalization**: remainder goes to first leg → exactly 100

### 4.2 Policy (`engines.Policy`)
Fail-closed on profile load error. Verdicts:

| Action | Tier < 3 | Country blocked | Withdraw/Liquidate | High value ≥ threshold | Default |
|--------|----------|-----------------|--------------------|------------------------|---------|
| Any    | REQUIRES_COMPLIANCE_REVIEW | NOT_SUPPORTED | — | — | — |
| Withdraw | — | — | REQUIRES_AUTHENTICATION | REQUIRES_AUTHENTICATION | — |
| Any | — | — | — | — | REQUIRES_CONFIRMATION |

### 4.3 Previewer (`engines.Previewer`)
Computes drift, proposed trades, fees, slippage, post-trade allocation, and passes policy verdict through. Never places trades.

---

## 5. Glider Provider Integration

### 5.1 API Surface (41 operations)
- **Strategies**: Create, publish version, get, set schedule, discover public
- **Enrollment**: Two-stage (signature request → submit with owner signature) — idempotent on `flowId`
- **Portfolios**: Get, list, positions, start/stop, trigger rebalance (429 cooldown)
- **Withdrawals**: Two-stage (signature → submit), idempotent on authorization nonce
- **Operations**: Poll async state, deposit simulation (test only)

### 5.2 Client (`internal/infrastructure/adapters/glider/client.go`)
- `x-api-key` header, correlation ID, `Accept: application/json`
- Retries **only** idempotent reads and idempotent writes (enrollment submit, withdrawal submit)
- `Retry-After` honored for 429
- Structured `APIError` with `IsCooldown`, `IsRetryable`, `IsConflict`, etc.
- Fails closed if API key missing

### 5.3 Simulated Provider (`internal/infrastructure/adapters/glider/simulated.go`)
In-process deterministic provider for tests and CI. No network calls.
- Enforces rebalance cooldown
- `FailNext` injection, `SimulateDeposit`, `SetPrice`
- Idempotent enrollment replay on same `flowId`

---

## 6. Owner Signer Port

Glider's two-stage flows require the **portfolio owner's Solana signature**.

```go
type OwnerSigner interface {
    OwnerAccount(ctx context.Context, userID uuid.UUID) (string, error) // CAIP-10
    SignSolanaMessage(ctx context.Context, userID uuid.UUID, message string) (string, error) // base58
    SignSolanaTransaction(ctx context.Context, userID uuid.UUID, unsignedTx string) (string, error) // base64
}
```

| Implementation | Use Case |
|----------------|----------|
| `DerivedSigner` (HKDF ed25519) | M1 development, staging — **never prod unless explicitly accepted** |
| `CircleSigner` | Production — uses user's custody wallet via Circle's `SignTransaction`; message signing returns `ErrMessageSigningUnsupported` |

The port is isolated: swapping to user-held keys is configuration, not code change.

---

## 7. Agent API Contract

### 7.1 Read Endpoints (auto-execute in Miriam)
```
GET  /api/v1/investments/limits
GET  /api/v1/investments/portfolio
GET  /api/v1/investments/positions
GET  /api/v1/investments/assets
GET  /api/v1/investments/assets/:id
GET  /api/v1/investments/strategies
GET  /api/v1/investments/strategies/:id
GET  /api/v1/investments/strategies/:id/preview
GET  /api/v1/investments/executions
GET  /api/v1/investments/executions/:id
GET  /api/v1/investments/audit
GET  /api/v1/investments/investors
GET  /api/v1/investments/investors/:id
GET  /api/v1/investments/investors/:id/activity
```

### 7.2 Staged Mutations (202 + confirmation token)
```
POST /api/v1/investments/strategies              (create_strategy)
POST /api/v1/investments/strategies/:id/versions (update_strategy)
POST /api/v1/investments/enroll                  (enroll_strategy)
POST /api/v1/investments/orders                  (buy_asset / sell_asset)
POST /api/v1/investments/allocations             (set_allocation)
```

**First call** → HTTP 202, body:
```json
{
  "status": "AWAITING_CONFIRMATION",
  "confirmation": {
    "token": "cfm-...",
    "action": "create_strategy",
    "payload_hash": "sha256(...)",
    "expires_at": "2026-09-15T12:30:00Z",
    "instruction": "ask the user to confirm this exact proposal, then repeat the call with confirmation_token set"
  },
  "preview": { ... },
  "policy": { "verdict": "REQUIRES_CONFIRMATION", "reasons": [...] }
}
```

**Replay** → same body with `confirmation_token` (or `X-Investment-Confirmation` header) → executes.

### 7.3 Immediate Mutations (no staging)
```
POST /api/v1/investments/strategies/:id/pause    (pause_strategy)
POST /api/v1/investments/strategies/:id/resume   (resume_strategy)
POST /api/v1/investments/strategies/:id/rebalance (rebalance_strategy)
```

### 7.4 Withdrawals (interactive + passcode step-up)
```
POST /api/v1/investments/withdrawals
```
Requires `RequireInteractiveSession` + `RequirePasscodeSession` middleware — **no agent tool exists for withdrawals**.

---

## 8. Error Mapping (Agent-Friendly)

| Go Error | HTTP | Agent Mapping |
|----------|------|---------------|
| `ErrValidationFailed` | 400 | Reject with violation list |
| `ErrConfirmationInvalid` / `ErrConfirmationExpired` / `ErrConfirmationConsumed` | 400/409/422 | "token invalid/expired/used" |
| `ErrStepUpRequired` | 403 | "needs in-app passcode" |
| `ErrProviderCooldown` | 429 | `INVESTMENT_PROVIDER_COOLDOWN` (surfaces `Retry-After`) |
| `ErrPolicyBlocked` | 422 | Policy verdict + reasons |
| `ErrProviderUnavailable` | 503 | "provider unreachable" (retryable) |

---

## 9. Miriam Tool Layer

Located at `miriam_agent/tools/investment_definitions.py`.

| Category | Tools | Safety |
|----------|-------|--------|
| Reads | `get_portfolio`, `get_positions`, `search_assets`, `get_asset`, `get_strategy`, `list_strategies`, `get_rebalance_preview`, `get_investment_limits`, `list_executions`, `get_execution`, `get_execution_status`, `list_audit_events`, `list_investors`, `get_investor`, `get_investor_activity` | `LOW`, auto-execute |
| Staged | `create_strategy`, `update_strategy`, `enroll_strategy`, `buy_asset`, `sell_asset`, `set_allocation` | `HIGH`, `requires_approval`, auto-replay token on user approval |
| Immediate | `pause_strategy`, `resume_strategy`, `rebalance_strategy` | `HIGH`, `requires_approval`, executes after approval |

**Honesty baked into descriptions:**
- Orders adjust target allocation; **not limit orders**, no price guarantee
- Investor data labeled `VERIFIED/INFERRED/UNAVAILABLE` — never fabricated
- No withdrawal tool (app-only via passcode step-up)

**Confirmation replay:** The agent replays the token automatically **once the user approves** the staged action. Actions whose policy verdict is `REQUIRES_AUTHENTICATION` are **never auto-replayed** — they require the in-app flow.

---

## 10. Sync Worker

`internal/workers/investment_sync/worker.go`
- Polls open provider operations (`PollOperations`)
- Optional rebalance sweep (`RunRebalanceSweep`) — `DueForRebalance` respects drift threshold + minimum interval
- Normalizes holdings (`normalizeHoldings`) from provider positions
- Configurable intervals via `InvestmentGliderConfig`

---

## 11. Configuration

`internal/infrastructure/config/config.go` → `InvestmentGliderConfig`

```go
Enabled                  bool
ProviderBaseURL          string        // default: https://api.glider.fi/v2
ProviderAPIKey           string        // REQUIRED in non-dev
OwnerSignerMode          string        // "derived" | "circle"
DerivedSignerMasterSeed  string        // 32+ chars
OwnerAccountPrefix       string        // default: solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp
OwnerChain               WalletChain   // default: SOL
ConfirmationTTL          time.Duration // default: 15m
HighValueThresholdUSD    decimal       // default: 1000
AllowedCountries         []string
WorkerPollInterval       time.Duration // default: 30s
WorkerRebalanceEnabled   bool
WorkerRebalanceInterval  time.Duration // default: 5m
StaleMarketDataAfter     time.Duration // default: 6h
SettlementSymbol         string        // default: USDC
```

`validateInvestmentGliderConfig` rejects `Enabled=true` with empty key in non-dev, empty master seed in derived mode, etc.

---

## 12. Migrations

| # | Name | Purpose |
|---|------|---------|
| 303 | `create_investment_assets_strategies_versions` | Assets, strategies, immutable versions |
| 304 | `create_investment_enrollments_holdings_executions` | User portfolios, positions, execution records |
| 305 | `create_investment_funding_signatures_confirmations_operations_audit` | Money movement, signatures, confirmations, async ops, audit |
| 306 | `create_investment_limits` | Per-user investment limits + policy config |

Run via `make migrate-up` (golang-migrate, file-based from `migrations/`).

---

## 13. Testing

```bash
# Go
go test -race ./internal/domain/services/investment/...          # engines + e2e service
go test -race ./internal/infrastructure/adapters/glider/...       # client + simulated
go test -race ./internal/infrastructure/adapters/investmentowner/... # signer tests
go test -race ./...                                               # full suite

# Python (Miriam)
.venv/bin/python -m pytest tests/test_investment_tools.py tests/test_go_client.py tests/test_proactive.py -q
.venv/bin/python -m pytest -q                                     # full suite (285 tests)
```

**Test coverage includes:**
- Validator: reject/accept table tests, normalization, too-many-legs, position caps
- Policy: verdict matrix by tier/country/action/amount
- Preview: drift, trades, fees, slippage, policy passthrough
- Service: staged/confirm flows, idempotency, single-use/expired/tampered confirmation, policy block, enroll+fund, order→new version+funding, cooldown, operation polling, withdrawal step-up
- Glider client: cursor, envelope errors, retry rules, idempotent enrollment replay, context cancellation
- Signer: per-user derivation determinism, message signature verifiability, transaction signing, Circle message-signing refusal

---

## 14. Security & Compliance

- **No secrets in code**: API key via env/secret manager, master seed only in derived signer (M1)
- **Audit trail**: Every mutation emits `InvestmentAuditEvent` with `InitiatedBy` (user/miriam/worker/system) and `Reason`
- **Idempotency**: Every write carries an `IdempotencyKey` (user-scoped, deterministic in Miriam)
- **Confirmation tokens**: Single-use, TTL (15m default), bound to **payload hash** (token field excluded from hash)
- **Step-up**: Withdrawals require `RequireInteractiveSession` + `RequirePasscodeSession` — not callable from chat
- **No KYC gate for strategy investing**: Glider holds and executes the assets, so an unverified (Tier 1) user can create, enroll in and fund a strategy. KYC is still enforced for USD fiat virtual accounts, cards, brokerage (Alpaca) investing, ramps and P2P. `REQUIRES_COMPLIANCE_REVIEW` remains only for cases the policy layer cannot clear (country not supported, contradictory evidence flagged elsewhere).
- **Provider failures**: Fail closed — action is unconfirmed, never silently failed
- **Ledger integration**: Funding reuses `WithdrawalService.InitiateCryptoWithdrawal` (double-entry)

---

## 15. Next Milestones (Milestone 2+)

- Recurring investing (scheduled contributions per enrollment)
- Copy/public strategy marketplace with full provenance labeling
- Advanced rebalance policies (tax-lot aware, ESG screens)
- User-held key signer (Web3Auth / Passkey / embedded wallet)
- Portfolio analytics API for Miriam "explain my returns"
- Cross-chain (Solana Model A vs B, CAIP-19 resolution)

---

## 16. Quick Start (Local)

```bash
# Rail backend
cd /Users/tobi/Development/RAIL_BACKEND
cp .env.example .env
# Set INVESTMENT_GLIDER_API_KEY=your-production-key
# Set INVESTMENT_GLIDER_OWNER_SIGNER_MODE=derived
# Set INVESTMENT_GLIDER_DERIVED_MASTER_SEED=32-char-seed
make dev

# Run tests
make test
go test -race ./internal/domain/services/investment/... ./internal/infrastructure/adapters/glider/... ./internal/infrastructure/adapters/investmentowner/...

# Miriam
cd /Users/tobi/Development/MIRIAM
.venv/bin/python -m pytest tests/test_investment_tools.py -q
```

---

## 17. Appendix: Key File Map

| Package | Files |
|---------|-------|
| Domain entities | `internal/domain/entities/investment_glider_{entities,requests,provider}.go` |
| Engines | `internal/domain/services/investment/engines.go` + `engines_test.go` |
| Services | `internal/domain/services/investment/{service,strategy_service,enrollment_service,execution_service,discovery,sync}.go` |
| Service tests | `internal/domain/services/investment/service_test.go` |
| Repositories | `internal/infrastructure/repositories/investment_{strategy,portfolio,ledger,policy}_repository.go` |
| Glider adapter | `internal/infrastructure/adapters/glider/{client,errors,simulated}.go` + `client_test.go` |
| Owner signer | `internal/infrastructure/adapters/investmentowner/{signer,derived,circle}.go` + `signer_test.go` |
| API handlers | `internal/api/handlers/investment/handlers.go` |
| API routes | `internal/api/routes/investment_glider_routes.go` |
| Middleware | `internal/api/middleware/investment_restrictions.go` |
| Worker | `internal/workers/investment_sync/worker.go` |
| DI wiring | `internal/infrastructure/di/investment_glider_wiring.go` |
| Config | `internal/infrastructure/config/config.go` |
| Migrations | `migrations/303_...`, `304_...`, `305_...`, `306_...` |
| Miriam tools | `miriam_agent/tools/investment_definitions.py` + `tests/test_investment_tools.py` |
| Miriam client | `miriam_agent/integrations/go_client.py` |

---

**Principle:** *Miriam decides what should happen. The investment infrastructure decides whether it is allowed and executes it.*
