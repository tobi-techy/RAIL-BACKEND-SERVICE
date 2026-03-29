# Lulo Production-Ready Integration Architecture

## Phase 1: Foundation (Week 1-2) — Core Yield Deposit Flow

### 1.1 Database Schema

Migration: `migrations/xxx_add_yield_positions.sql`

```sql
CREATE TABLE yield_positions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider VARCHAR(50) NOT NULL DEFAULT 'lulo',
    external_position_id VARCHAR(255) UNIQUE,
    principal_amount NUMERIC(36,18) NOT NULL DEFAULT 0,
    current_balance NUMERIC(36,18) NOT NULL DEFAULT 0,
    accrued_yield NUMERIC(36,18) NOT NULL DEFAULT 0,
    apy NUMERIC(5,2),
    status VARCHAR(20) NOT NULL DEFAULT 'active', -- active, withdrawing, closed, failed
    deposited_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_synced_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT positive_amounts CHECK (principal_amount >= 0 AND current_balance >= 0)
);

CREATE INDEX idx_yield_positions_user ON yield_positions(user_id);
CREATE INDEX idx_yield_positions_provider ON yield_positions(provider, status);

ALTER TABLE ledger_transactions
ADD COLUMN yield_position_id UUID REFERENCES yield_positions(id) ON DELETE SET NULL;

CREATE TABLE yield_floats (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider VARCHAR(50) NOT NULL UNIQUE,
    total_float NUMERIC(36,18) NOT NULL DEFAULT 0,
    allocated_to_withdrawals NUMERIC(36,18) NOT NULL DEFAULT 0,
    available_float NUMERIC(36,18) NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### 1.2 Domain Entities

File: `internal/domain/entities/yield_entities.go`

- `YieldProvider` — enum: `lulo`, `kamino`, `solayer`
- `YieldPosition` — tracks user's position (ID, principal, current balance, accrued yield, APY, status, timestamps)
- `YieldStatus` — enum: `active`, `withdrawing`, `closed`, `failed`
- `YieldFloat` — tracks available liquidity for instant withdrawals per provider
- `YieldOperationRequest` — deposit/withdrawal request with validation

### 1.3 Repository Layer

File: `internal/infrastructure/repositories/yield_repository.go`

Methods:
- `CreatePosition(ctx, position)` — insert new yield position
- `GetPositionByUser(ctx, userID, provider)` — get active position (latest, status=active)
- `UpdatePositionBalance(ctx, positionID, currentBalance, accruedYield, apy)` — sync from Lulo
- `UpdatePositionStatus(ctx, positionID, status)` — lifecycle transitions
- `GetTotalActivePositions(ctx, provider)` — aggregate stats (total principal, count)
- `GetFloat(ctx, provider)` — get float record (auto-initialize if missing)
- `AllocateFloat(ctx, provider, amount)` — reserve float for instant withdrawal (atomic check)
- `execContext` — handles both transactional and non-transactional contexts

### 1.4 Lulo Client

File: `internal/infrastructure/adapters/lulo/client.go`

Interface:
- `Deposit(ctx, DepositRequest) → DepositResponse` — create yield position
- `GetPosition(ctx, positionID) → Position` — current state with yield
- `RequestWithdrawal(ctx, positionID, amount) → WithdrawalResponse`
- `GetCurrentAPY(ctx) → float64`

Types:
- `DepositRequest` — userID, amount, token (USDC), callbackURL
- `DepositResponse` — positionID, status, txHash, amount, APY
- `Position` — id, principal, accruedYield, totalBalance, APY, status, timestamps

HTTP client with 30s timeout, idempotency-key header on deposits.

### 1.5 Yield Service

File: `internal/domain/services/yield/service.go`

Interfaces consumed:
- `Repository` — yield position persistence
- `LuloClient` — Lulo API
- `LedgerIntegration` — `RecordYieldDeposit`, `RecordYieldWithdrawal`, `CreditStash`

Key methods:
- `DepositStash(ctx, userID, amount)` — skip if < $10 min, top-up existing or create new position
- `GetBalance(ctx, userID)` → (principal, yieldEarned, apy) — sync with Lulo (5min cache)
- `WithdrawStash(ctx, userID, amount)` — try float first, fallback to standard Lulo withdrawal

Internal:
- `createPosition` — Lulo API → DB → ledger (with refund on DB failure)
- `topUpPosition` — add to existing active position
- `syncPosition` — refresh from Lulo if cache > 5min, update DB
- `instantWithdrawal` — allocate float → credit stash → async replenish from Lulo
- `standardWithdrawal` — direct Lulo withdrawal

---

## Phase 2: Integration Points (Week 2-3)

### 2.1 Allocation Service (`allocation/service.go`)

- Add `yieldService YieldService` + `enableYield bool` to Service struct
- After stash transfer succeeds in `ProcessIncomingFunds`, async call `depositToYield`
- `depositToYield` — detached context (30s timeout), non-blocking, logs errors, stash stays in ledger on failure

### 2.2 Auto-Invest Service (`autoinvest/service.go`)

- Add `yieldService YieldService` to Service struct
- In `TriggerAutoInvestment`, check yield balance first
- If total (principal + yield) >= min investment amount, withdraw from Lulo before investing
- Fallback to available stash_balance if withdrawal fails

### 2.3 Ledger Enhancements (`ledger/service.go`)

New methods:
- `RecordYieldDeposit(ctx, userID, amount, positionID)` — debit yield_position, credit stash_balance
- `RecordYieldWithdrawal(ctx, userID, amount, positionID)` — reverse entries

New account type: `AccountTypeYieldPosition`
New transaction type: `TransactionTypeYieldDeposit`

---

## Phase 3: Workers & Background Jobs (Week 3)

### 3.1 Yield Sync Worker (`internal/workers/yield_sync/worker.go`)

- Runs every 5 minutes
- Syncs all active positions with Lulo API
- Updates accrued yield in DB
- Triggers notifications for significant yield earned

### 3.2 Deposit Cleanup Extension

- Find yield positions stuck in 'pending' > 1 hour
- Reconcile with Lulo — if no record, refund stash_balance

---

## Phase 4: API Layer (Week 3-4)

### 4.1 Handlers (`internal/api/handlers/yield_handlers.go`)

- `GET /yield/balance` → principal, yield_earned, apy, total
- `POST /yield/sync` → force manual sync with Lulo

### 4.2 Enhanced Balances Endpoint

```json
{
  "spending_balance": "70.00",
  "stash_balance": "30.00",
  "stash_yield_earned": "0.45",
  "stash_total": "30.45",
  "stash_apy": "8.5%",
  "mode_active": true
}
```

---

## Phase 5: Testing & Deployment

### Testing Pyramid
1. Unit tests — repository methods, Lulo client mocking
2. Integration tests — Lulo sandbox environment
3. E2E tests — full deposit → allocation → yield flow

### Deployment Phases
- Week 1: Shadow mode (log what would be deposited, no user-facing changes)
- Week 2: Beta (10% of users via feature flag)
- Week 3: Full rollout (monitor float levels, withdrawal latency)

---

## Risk Mitigation

| Risk | Mitigation |
|---|---|
| Lulo API downtime | Graceful fallback to stash_balance, retry queue |
| Failed deposits | Compensation: return funds to stash_balance |
| Gas spikes on Solana | Jito bundles, batched operations |
| Float depletion | Alert at 20% remaining, pause instant withdrawals |
| Yield calculation disputes | Audit trail via allocation_events + ledger_entries |

---

## Configuration (Environment Variables)

```
LULO_API_URL=https://api.lulo.fi/v1
LULO_API_KEY=xxx
YIELD_ENABLED=true
YIELD_MIN_DEPOSIT_USD=10.00
YIELD_FLOAT_SIZE_USD=50000
YIELD_SYNC_INTERVAL_MINUTES=5
```
