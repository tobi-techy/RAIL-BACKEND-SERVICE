# Implementation Plan: Ledger + Treasury + Asynchronous Net Settlement Architecture
## Problem Statement
The current stack_service implements a basic stablecoin-to-USD conversion flow where users deposit USDC, it's converted to USD (via Due off-ramp), and then funded to Alpaca for trading. However, this creates friction with per-transaction conversions and lacks efficient buffer management. We need to implement a sophisticated financial architecture with:
1. **Ledger Service** - Core source of truth for all balances and transactions
2. **Treasury Engine** - Manages USDC/fiat buffers and batched net settlement conversions
3. **Onchain Engine** - Handles all Circle wallet interactions and blockchain monitoring
4. **Reconciliation Service** - Ensures system integrity across all financial boundaries
## Current State Overview
### Existing Components
**Funding Flow:**
* Circle Developer-Controlled Wallets for USDC deposits
* Due API integration for USDC → USD off-ramp (per deposit)
* Alpaca brokerage integration with instant funding via journals
* `deposits` table tracks: `pending` → `confirmed` → `off_ramp_initiated` → `off_ramp_completed` → `broker_funded`
* `balances` table maintains `buying_power` and `pending_deposits` per user
* Asynchronous processing via funding webhook workers and SQS
**Withdrawal Flow:**
* Alpaca journal to debit USD from user account
* Due API on-ramp USD → USDC
* Circle wallet transfers USDC to user's destination address
* `withdrawals` table with status tracking
**Key Services:**
* `internal/domain/services/funding/service.go` - Deposit handling
* `internal/domain/services/withdrawal_service.go` - Withdrawal orchestration
* `internal/domain/services/balance_service.go` - Balance updates
* `internal/workers/funding_webhook/` - Async deposit processing with Alpaca funding
* `internal/infrastructure/circle/client.go` - Circle API integration
* `internal/adapters/alpaca/` - Alpaca brokerage adapter with journal operations
* `internal/adapters/due/` - Due off-ramp/on-ramp integration
**Database Schema:**
* `deposits` (id, user_id, chain, tx_hash, token, amount, status, off_ramp_tx_id, alpaca_funding_tx_id)
* `balances` (user_id, buying_power, pending_deposits, currency)
* `withdrawals` (id, user_id, alpaca_account_id, amount, destination_chain, destination_address, status)
* `transactions` table exists but appears limited (idempotency tracking only)
**Limitations:**
1. No double-entry ledger - balance updates are direct modifications
2. Per-transaction USDC→USD conversion creates latency and cost overhead
3. No buffer management for USDC or fiat operational accounts
4. No batched net settlement mechanism
5. Limited treasury optimization opportunities
6. Reconciliation is manual/ad-hoc
### Circle API Capabilities
Circle Developer-Controlled Wallets support:
* Wallet creation and management
* On-chain transfers and batch operations
* Balance queries across supported blockchains (Ethereum, Solana, Polygon, etc.)
* **Note:** Circle does NOT provide native USDC↔USD conversion. We must use third-party providers (Due, ZeroHash, Coinbase) for conversions.
## Proposed Architecture
### 1. Ledger Service (Core Financial Engine)
**Purpose:** Single source of truth for all financial state using double-entry bookkeeping.
**Components:**
* `internal/domain/services/ledger/service.go` - Core ledger operations
* `internal/domain/services/ledger/entries.go` - Entry creation and validation
* `internal/domain/entities/ledger_entities.go` - Account and entry models
* `internal/infrastructure/repositories/ledger_repository.go` - Persistence layer
**Database Tables:**
```SQL
-- Ledger accounts (user USDC, user fiat exposure, system buffers)
CREATE TABLE ledger_accounts (
  id UUID PRIMARY KEY,
  user_id UUID REFERENCES users(id),
  account_type VARCHAR(50) NOT NULL, -- usdc_balance, fiat_exposure, pending_investment, system_buffer_usdc, system_buffer_fiat, broker_operational
  currency VARCHAR(10) NOT NULL, -- USDC, USD
  balance DECIMAL(36,18) NOT NULL DEFAULT 0,
  created_at TIMESTAMP,
  updated_at TIMESTAMP
);
-- Double-entry ledger entries (immutable)
CREATE TABLE ledger_entries (
  id UUID PRIMARY KEY,
  transaction_id UUID NOT NULL,
  account_id UUID REFERENCES ledger_accounts(id),
  entry_type VARCHAR(10) NOT NULL CHECK (entry_type IN ('debit', 'credit')),
  amount DECIMAL(36,18) NOT NULL CHECK (amount >= 0),
  currency VARCHAR(10) NOT NULL,
  description TEXT,
  metadata JSONB,
  created_at TIMESTAMP NOT NULL
);
-- Transaction groups (each transaction has exactly 2 entries: debit + credit)
CREATE TABLE ledger_transactions (
  id UUID PRIMARY KEY,
  user_id UUID REFERENCES users(id),
  transaction_type VARCHAR(50) NOT NULL, -- deposit, withdrawal, investment, conversion, internal_transfer
  reference_id UUID, -- Link to deposits, withdrawals, orders, etc.
  reference_type VARCHAR(50),
  status VARCHAR(20) NOT NULL DEFAULT 'pending', -- pending, completed, reversed
  idempotency_key VARCHAR(100) UNIQUE NOT NULL,
  created_at TIMESTAMP NOT NULL,
  completed_at TIMESTAMP
);
```
**Key Operations:**
* `CreateTransaction(ctx, entries []LedgerEntry) error` - Atomic double-entry creation
* `GetAccountBalance(ctx, userID, accountType) decimal.Decimal`
* `GetUserBalances(ctx, userID) (*UserBalances, error)` - All account types
* `ReserveForInvestment(ctx, userID, amount) error` - Lock funds for pending trades
* `ReleaseReservation(ctx, userID, amount) error` - Unlock on trade completion/cancellation
* `ReverseTransaction(ctx, transactionID) error` - Compensating entries
**Balance Representation:**
Each user has multiple ledger accounts:
* `usdc_balance` - Available USDC (instant withdrawal capable)
* `fiat_exposure` - Buying power at Alpaca (from converted USDC)
* `pending_investment` - Reserved funds for in-flight trades
System-level accounts:
* `system_buffer_usdc` - On-chain USDC reserve for instant withdrawals
* `system_buffer_fiat` - Operational USD at Due/conversion provider
* `broker_operational` - Pre-funded cash at Alpaca
### 2. Onchain Engine
**Purpose:** All blockchain and Circle wallet interactions.
**Components:**
* `internal/domain/services/onchain/engine.go` - Main orchestrator
* `internal/domain/services/onchain/deposit_monitor.go` - Chain event monitoring
* `internal/domain/services/onchain/transfer_executor.go` - Outbound transfers
* Extend existing `internal/infrastructure/circle/client.go`
**Key Responsibilities:**
1. **Deposit Detection:**
    * Monitor Circle wallet addresses for incoming USDC
    * Fire events to ledger: Credit `usdc_balance`, Debit `system_buffer_usdc`
    * Update `deposits` table status
2. **Withdrawal Execution:**
    * Receive withdrawal requests from Withdrawal Service
    * Execute USDC transfer from `system_buffer_usdc` to user address
    * Post ledger entry: Debit `usdc_balance`, Credit `system_buffer_usdc`
    * Handle stuck transactions with retry logic
3. **Buffer Management:**
    * Track `system_buffer_usdc` ledger account
    * Signal treasury when buffer falls below threshold
**Integration Pattern:**
* Webhook-based for Circle deposit notifications
* Event-driven communication with Ledger and Treasury
### 3. Treasury Engine (Critical Component)
**Purpose:** Minimize conversion friction via batching, buffering, and net settlement.
**Components:**
* `internal/domain/services/treasury/engine.go` - Main orchestrator
* `internal/domain/services/treasury/conversion_scheduler.go` - Batch job scheduler
* `internal/domain/services/treasury/buffer_monitor.go` - Threshold monitoring
* `internal/domain/entities/conversion_entities.go` - Conversion job models
* `internal/infrastructure/repositories/conversion_repository.go`
**Database Tables:**
```SQL
CREATE TABLE conversion_jobs (
  id UUID PRIMARY KEY,
  job_type VARCHAR(20) NOT NULL, -- usdc_to_usd, usd_to_usdc
  amount DECIMAL(36,18) NOT NULL,
  source_account VARCHAR(50) NOT NULL,
  destination_account VARCHAR(50) NOT NULL,
  provider VARCHAR(50) NOT NULL, -- due, zerohash, coinbase
  status VARCHAR(20) NOT NULL, -- pending, processing, completed, failed
  provider_tx_id VARCHAR(200),
  provider_response JSONB,
  error_message TEXT,
  retry_count INT DEFAULT 0,
  created_at TIMESTAMP,
  started_at TIMESTAMP,
  completed_at TIMESTAMP
);
CREATE TABLE buffer_thresholds (
  buffer_account VARCHAR(50) PRIMARY KEY,
  min_threshold DECIMAL(36,18) NOT NULL,
  target_threshold DECIMAL(36,18) NOT NULL,
  max_threshold DECIMAL(36,18) NOT NULL,
  updated_at TIMESTAMP
);
```
**Buffer Strategy:**
1. **USDC On-chain Buffer (`system_buffer_usdc`):**
    * Size: 1-3% of total AUM or $10k minimum
    * Purpose: Instant user withdrawals without waiting for conversions
    * Replenishment: When < min_threshold, trigger USD→USDC conversion
2. **Fiat Operational Buffer (`system_buffer_fiat`):**
    * Size: 1-2 days of avg conversion demand
    * Purpose: Staging area for conversion flows
    * Management: Balance between conversion provider and Alpaca funding needs
3. **Broker Cash Buffer (`broker_operational`):**
    * Size: 1 day of avg trade volume
    * Purpose: Instant trade execution at Alpaca
    * Replenishment: When < min_threshold, trigger USDC→USD→Alpaca funding
**Treasury Operations:**
**Conversion Scheduler (Cron/Event-driven):**
```go
func (t *TreasuryEngine) RunNetSettlementCycle(ctx context.Context) error {
    // 1. Calculate net demand
    netUSDCToUSD := t.calculateNetUSDCToUSD(ctx) // deposits - withdrawals
    netUSDToUSDC := t.calculateNetUSDToUSDC(ctx) // withdrawal demand
    
    // 2. Check buffer levels
    buffers := t.getBufferLevels(ctx)
    
    // 3. Determine conversion jobs
    jobs := []
    if buffers.BrokerCash < thresholds.MinBrokerCash {
        jobs.append(ConversionJob{Type: USDC_TO_USD, Amount: netUSDCToUSD})
    }
    if buffers.OnchainUSDC < thresholds.MinOnchainUSDC {
        jobs.append(ConversionJob{Type: USD_TO_USDC, Amount: netUSDToUSDC})
    }
    
    // 4. Execute batched conversions
    for job := range jobs {
        t.executeConversion(ctx, job)
    }
}
```
**Conversion Execution:**
1. Create `conversion_jobs` record
2. Call conversion provider API (Due, ZeroHash)
3. Monitor completion via webhook or polling
4. Post ledger entries on completion:
    * USDC→USD: Debit `system_buffer_usdc`, Credit `system_buffer_fiat`
    * USD→USDC: Debit `system_buffer_fiat`, Credit `system_buffer_usdc`
5. Trigger downstream actions (Alpaca funding if needed)
**Multi-Provider Fallback:**
```go
type ConversionProvider interface {
    ConvertUSDCToUSD(ctx, amount) (txID string, err error)
    ConvertUSDToUSDC(ctx, amount) (txID string, err error)
    GetConversionStatus(ctx, txID) (status, err)
}
providers := []ConversionProvider{dueClient, zeroHashClient, coinbaseClient}
```
**Scheduler Configuration:**
* Interval: Every 5 minutes (configurable)
* Trigger: On buffer threshold breach
* Batching window: Aggregate all conversions within interval
### 4. Reconciliation Service
**Purpose:** Ensure ledger matches reality across all external systems.
**Components:**
* `internal/domain/services/reconciliation/service.go`
* `internal/domain/services/reconciliation/checks.go`
* `internal/domain/entities/reconciliation_entities.go`
**Reconciliation Checks (Hourly + Daily):**
1. **Ledger Internal Consistency:**
    * Sum of all debits = Sum of all credits
    * No orphaned entries (entries without matching transaction)
    * All transactions have exactly 2 entries
2. **Circle Balance Reconciliation:**
```warp-runnable-command
ledger.system_buffer_usdc == Circle.getTotalBalance(wallets)
```
3. **Alpaca Balance Reconciliation:**
```warp-runnable-command
SUM(ledger.fiat_exposure) == Alpaca.getTotalBuyingPower(accounts)
```
4. **Deposit Reconciliation:**
```warp-runnable-command
SUM(ledger entries for deposits) == SUM(deposits.amount WHERE status='broker_funded')
```
5. **Conversion Job Reconciliation:**
    * All `completed` conversion jobs have corresponding ledger entries
    * Provider balance matches expected after conversions
**Exception Handling:**
* Create `reconciliation_exceptions` table with discrepancy details
* Alert operations team via monitoring
* Auto-correct small discrepancies (<$1)
* Require manual review for larger discrepancies
### 5. Modified Flows
**A) Enhanced Deposit Flow:**
```warp-runnable-command
1. User sends USDC to Circle wallet
2. OnchainEngine detects deposit → fires event
3. Ledger: 
   - Credit user.usdc_balance (+100 USDC)
   - Debit system.system_buffer_usdc (-100 USDC)
4. deposits table: status = 'confirmed'
5. User sees instant USDC balance
6. Treasury (async, batched):
   - IF system.broker_operational < threshold:
     - Schedule USDC→USD conversion (net batch)
     - Execute via Due/ZeroHash
     - Ledger: Debit system_buffer_usdc, Credit system_buffer_fiat
     - Fund Alpaca via journal
     - Ledger: Debit system_buffer_fiat, Credit broker_operational
```
**B) Enhanced Investment Flow:**
```warp-runnable-command
1. User places order (basket/stock)
2. OrderService checks ledger.usdc_balance >= order.amount
3. Ledger: Reserve funds
   - Credit user.usdc_balance (-100 USDC)
   - Debit user.pending_investment (+100 USDC)
4. BrokerAdapter submits order to Alpaca (using pre-funded broker_operational)
5. Alpaca executes trade
6. On fill webhook:
   - Ledger: 
     - Credit user.pending_investment (-100 USDC)
     - Debit user.fiat_exposure (+100 USD equivalent)
   - Update positions
7. Treasury (async): 
   - Convert net USDC→USD to replenish broker_operational
```
**C) Enhanced Withdrawal Flow:**
```warp-runnable-command
1. User requests USDC withdrawal
2. WithdrawalService checks ledger.usdc_balance >= amount
3. Ledger:
   - Credit user.usdc_balance (-100 USDC)
   - Debit system.system_buffer_usdc (+100 USDC)
4. OnchainEngine sends USDC immediately from buffer
5. withdrawals table: status = 'completed'
6. Treasury (async, batched):
   - IF system_buffer_usdc < threshold:
     - Schedule USD→USDC conversion
     - IF broker_operational has excess:
       - Journal from Alpaca to fiat buffer
       - Convert to USDC
       - Deposit to Circle wallet
```
## Implementation Phases
### Phase 1: Ledger Foundation (Week 1-2)
**Goal:** Establish double-entry ledger as single source of truth
**Tasks:**
1. Database schema creation (ledger_accounts, ledger_entries, ledger_transactions)
2. Implement Ledger Service with core operations
3. Create ledger repositories
4. Write comprehensive unit tests for double-entry logic
5. Implement transaction reversal mechanism
6. Create migration to populate initial ledger state from existing balances/deposits
**Deliverables:**
* All ledger tables created
* LedgerService with atomic transaction creation
* 100% test coverage for ledger operations
* Migration script tested on staging data
### Phase 2: Treasury Engine Core (Week 2-3)
**Goal:** Implement buffer management and conversion scheduling
**Tasks:**
1. Database schema for conversion_jobs and buffer_thresholds
2. Implement TreasuryEngine with buffer monitoring
3. Create ConversionScheduler with cron job
4. Implement multi-provider fallback for conversions (Due primary)
5. Add conversion webhook handlers
6. Integrate with existing Due adapter
7. Unit and integration tests for treasury operations
**Deliverables:**
* Treasury engine running with configurable thresholds
* Conversion jobs table tracking all conversions
* Scheduler running every 5 minutes
* Provider fallback mechanism tested
### Phase 3: Onchain Engine Enhancement (Week 3-4)
**Goal:** Integrate Circle wallet operations with ledger
**Tasks:**
1. Refactor deposit monitoring to post ledger entries
2. Implement withdrawal execution from system buffer
3. Add buffer-aware withdrawal logic
4. Create OnchainEngine service
5. Integrate with existing Circle client
6. Add retry logic for stuck transactions
7. Integration tests with testnet Circle wallets
**Deliverables:**
* OnchainEngine service operational
* All deposits/withdrawals post ledger entries
* Buffer tracking integrated
* Withdrawal instant execution from buffer
### Phase 4: Flow Integration (Week 4-5)
**Goal:** Migrate existing flows to use ledger and treasury
**Tasks:**
1. Modify FundingService to post ledger entries on deposit
2. Update WithdrawalService to check ledger balances
3. Modify InvestingService to reserve funds via ledger
4. Update BalanceService to query ledger accounts
5. Integrate treasury conversion triggers with deposit/withdrawal flows
6. Add buffer replenishment triggers
7. End-to-end integration tests
**Deliverables:**
* All services integrated with ledger
* Treasury conversions triggered by flow events
* E2E tests passing for deposit → invest → withdraw
### Phase 5: Reconciliation Service (Week 5-6)
**Goal:** Ensure system integrity and catch discrepancies
**Tasks:**
1. Implement ReconciliationService with all checks
2. Create reconciliation_exceptions table
3. Set up hourly and daily reconciliation jobs
4. Implement alerting for exceptions
5. Create manual review dashboard
6. Add metrics for reconciliation health
7. Integration tests with mocked external APIs
**Deliverables:**
* ReconciliationService running hourly
* Exception tracking and alerting
* Metrics dashboard showing reconciliation status
* Manual review procedures documented
### Phase 6: Monitoring & Operations (Week 6-7)
**Goal:** Production readiness with observability
**Tasks:**
1. Add OpenTelemetry tracing to all ledger/treasury operations
2. Create Prometheus metrics for buffer levels, conversion success rates
3. Build Grafana dashboards for treasury health
4. Configure CloudWatch alarms for buffer thresholds
5. Document runbooks for common issues
6. Conduct load testing for ledger performance
7. Disaster recovery procedures
**Deliverables:**
* Complete observability stack
* Production dashboards and alerts
* Runbooks for operations team
* Load test results showing system capacity
## Configuration
**Treasury Thresholds (Environment Variables):**
```yaml
treasury:
  buffers:
    usdc_onchain:
      min_threshold: "10000.00"  # $10k minimum
      target_threshold: "50000.00"  # Replenish to $50k
      max_threshold: "100000.00"  # Alert if exceeds
    fiat_operational:
      min_threshold: "20000.00"
      target_threshold: "100000.00"
    broker_cash:
      min_threshold: "50000.00"
      target_threshold: "200000.00"
  conversion:
    scheduler_interval: "5m"
    batch_window: "5m"
    providers:
      - name: "due"
        priority: 1
      - name: "zerohash"
        priority: 2
    retry_policy:
      max_attempts: 3
      backoff: "exponential"
```
## Migration Strategy
**Phase 1: Shadow Mode (Week 1-3)**
* Deploy ledger service
* Run in parallel with existing balance system
* Post ledger entries for all operations but don't use for decisions
* Compare ledger balances with existing balances table
* Fix discrepancies
**Phase 2: Validation Mode (Week 4-5)**
* Treasury engine active for conversions
* Continue using existing balance checks
* Log when ledger and balance diverge
* Monitor treasury conversion efficiency
**Phase 3: Cutover (Week 6)**
* Switch all balance queries to ledger
* Deprecate direct balance table updates
* Enable reconciliation service
* Monitor closely for issues
**Phase 4: Cleanup (Week 7+)**
* Keep balances table for backward compatibility (read-only)
* Remove direct balance update code
* Full production operations
## Risks & Mitigations
**Risk 1: Ledger Performance**
* *Impact:* High transaction volume could slow ledger operations
* *Mitigation:* Index optimization, read replicas, caching for balance queries
**Risk 2: Buffer Sizing**
* *Impact:* Under-sized buffers cause conversion delays; over-sized waste capital
* *Mitigation:* Start conservative, monitor utilization, adjust dynamically
**Risk 3: Conversion Provider Failures**
* *Impact:* Buffer depletion if primary provider unavailable
* *Mitigation:* Multi-provider fallback, alerting on provider health
**Risk 4: Reconciliation Mismatches**
* *Impact:* User funds at risk if ledger diverges from reality
* *Mitigation:* Automated hourly checks, circuit breaker on large discrepancies
**Risk 5: Migration Data Integrity**
* *Impact:* Incorrect initial ledger state causes incorrect balances
* *Mitigation:* Thorough testing on production snapshot, shadow mode validation
## Success Metrics
**Financial Metrics:**
* Conversion cost reduction: Target 50% fewer conversion transactions
* Capital efficiency: Buffer utilization 70-90% of target
* Treasury yield: Opportunity cost from idle buffers <0.1% AUM
**Operational Metrics:**
* Ledger transaction latency: p99 <100ms
* Conversion success rate: >99.5%
* Reconciliation discrepancies: <0.01% of transaction volume
* Buffer depletion events: <1 per month
**User Experience Metrics:**
* Withdrawal instant execution rate: >95% (from buffer)
* Deposit-to-investment latency: <30 seconds (unchanged)
* Investment execution time: <5 seconds (unchanged)
## References
* Circle Developer-Controlled Wallets Documentation
* Alpaca Journal API Documentation
* Due Off-ramp/On-ramp API Documentation
* Existing codebase: internal/domain/services/funding/, internal/adapters/
* Architecture constraints: WARP.md (Repository pattern, Circuit breakers, OpenTelemetry)
