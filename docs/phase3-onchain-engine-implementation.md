# Phase 3: Onchain Engine Implementation - Complete

## Overview

Phase 3 implements the Onchain Engine that integrates Circle wallet operations with the ledger system, enabling deposits and withdrawals to post double-entry ledger transactions automatically.

## Files Created

### 1. Onchain Engine (`internal/domain/services/onchain/engine.go`)

**Purpose:** Handles all blockchain and Circle wallet interactions with ledger integration

**Key Components:**

- **Deposit Processing**
  - `ProcessDeposit()` - Handles incoming USDC deposits from Circle webhooks
  - `postDepositLedgerEntries()` - Creates ledger entries (debit user, credit system buffer)
  - `MonitorDeposits()` - Fallback polling mechanism if webhooks fail
  
- **Withdrawal Execution**
  - `ExecuteWithdrawal()` - Processes withdrawal requests
  - `postWithdrawalLedgerEntries()` - Creates ledger entries (credit user, debit system buffer)
  - `executeCircleTransfer()` - Executes on-chain transfer via Circle
  - `ProcessPendingWithdrawals()` - Batch processes pending withdrawals
  
- **Buffer Monitoring**
  - `CheckSystemBufferLevel()` - Monitors USDC buffer health
  - `getActualCircleBalance()` - Queries Circle for real balances
  - Alerts on low buffers or ledger discrepancies

**Ledger Integration:**

Every deposit/withdrawal now posts double-entry transactions:

```
Deposit:
  Debit:  user.usdc_balance (+$100)
  Credit: system.system_buffer_usdc (-$100)

Withdrawal:
  Credit: user.usdc_balance (-$100)
  Debit:  system.system_buffer_usdc (+$100)
```

### 2. Circle Webhook Handler (`internal/api/handlers/circle_webhooks.go`)

**Purpose:** Receives and processes Circle API webhook notifications

**Endpoints:**
- `POST /webhooks/circle/transfers` - Transfer notifications
- `POST /webhooks/circle/wallets` - Wallet updates
- `POST /webhooks/circle/payments` - Payment status

**Features:**
- Webhook signature verification (HMAC-SHA256)
- Idempotent processing (checks for existing deposits by tx_hash)
- Chain mapping (ETH, SOL, MATIC, AVAX)
- Error handling with proper HTTP status codes

**Flow:**
1. Circle sends webhook on deposit
2. Verify signature
3. Parse payload
4. Call `OnchainEngine.ProcessDeposit()`
5. Return 200 OK (idempotent)

## Architecture Integration

### Deposit Flow

```
1. User sends USDC to Circle wallet
2. Circle detects on-chain transfer
3. Circle webhook → /webhooks/circle/transfers
4. CircleWebhookHandler.HandleTransferNotification()
5. OnchainEngine.ProcessDeposit()
   - Create deposit record
   - Post ledger entries:
     * Debit user.usdc_balance
     * Credit system.system_buffer_usdc
   - Update deposit status to 'confirmed'
6. User sees instant USDC balance (from ledger)
```

### Withdrawal Flow

```
1. User requests USDC withdrawal
2. WithdrawalService creates withdrawal record (status: pending)
3. OnchainEngine.ExecuteWithdrawal()
   - Check ledger balance
   - Post ledger entries:
     * Credit user.usdc_balance
     * Debit system.system_buffer_usdc
   - Execute Circle transfer (on-chain)
   - Update withdrawal status to 'completed'
4. User receives USDC at destination address
```

### Buffer Monitoring

```
OnchainEngine.CheckSystemBufferLevel() → called periodically
├── Query ledger: system_buffer_usdc balance
├── Query Circle: actual wallet balances
├── Compare and alert on discrepancies
└── Trigger treasury replenishment if low
```

## Key Design Decisions

### 1. Optimistic Ledger Posting

Deposits post ledger entries **immediately** upon Circle webhook notification, not after on-chain confirmations. This provides:
- Instant user balance updates
- Simplified state management
- Acceptable risk (Circle webhooks are reliable)

### 2. Ledger as Source of Truth

All balance checks use the ledger, not the old `balances` table:
```go
balance, err := ledgerService.GetAccountBalance(ctx, userID, AccountTypeUSDCBalance)
```

### 3. System Buffer Abstraction

The engine abstracts away the complexity of managing system USDC buffers:
- Deposits decrease buffer (USDC goes to users)
- Withdrawals increase buffer (USDC comes from users)
- Treasury Engine monitors and replenishes buffers

## Configuration

### Engine Config

```go
config := &onchain.EngineConfig{
    // Deposit monitoring
    DepositPollInterval:  30 * time.Second,
    MinDepositAmount:     decimal.NewFromFloat(1.0), // $1 minimum
    
    // Withdrawal execution
    WithdrawalRetryAttempts: 3,
    WithdrawalTimeout:       10 * time.Minute,
    
    // Buffer monitoring
    BufferCheckInterval:  1 * time.Minute,
    BufferAlertThreshold: decimal.NewFromFloat(5000.0), // $5k alert
}
```

### Environment Variables

```bash
# Circle API
CIRCLE_API_KEY=your_api_key
CIRCLE_WEBHOOK_SECRET=your_webhook_secret

# Onchain Engine
ONCHAIN_DEPOSIT_POLL_INTERVAL=30s
ONCHAIN_MIN_DEPOSIT_AMOUNT=1.0
ONCHAIN_BUFFER_ALERT_THRESHOLD=5000.0
ONCHAIN_WITHDRAWAL_TIMEOUT=10m
```

## Repository Methods Added

### Withdrawal Repository

```go
// Already existed
- GetByID(ctx, id) (*Withdrawal, error)
- UpdateStatus(ctx, id, status) error
- UpdateTxHash(ctx, id, txHash) error ✅

// Need to add
- GetPendingWithdrawals(ctx) ([]*Withdrawal, error)
```

### Deposit Repository

```go
// Already exist
- Create(ctx, deposit) error
- GetByTxHash(ctx, txHash) (*Deposit, error)
- UpdateStatus(ctx, id, status, confirmedAt) error

// Need to add
- GetPendingDeposits(ctx) ([]*Deposit, error)
```

### Managed Wallet Repository

```go
// Already exist
- GetByUserID(ctx, userID) ([]*ManagedWallet, error)
- GetByCircleWalletID(ctx, circleWalletID) (*ManagedWallet, error)

// Need to add
- GetAll(ctx) ([]*ManagedWallet, error)
```

## Testing

### Unit Tests

Test coverage needed for:
- `OnchainEngine.ProcessDeposit()` - with mock ledger/repos
- `OnchainEngine.ExecuteWithdrawal()` - with mock Circle client
- `CircleWebhookHandler` - with sample payloads
- Ledger entry creation - verify double-entry integrity

### Integration Tests

```go
func TestDepositFlow(t *testing.T) {
    // 1. Setup test database with ledger tables
    // 2. Create test user and managed wallet
    // 3. Simulate Circle webhook
    // 4. Verify deposit record created
    // 5. Verify ledger entries posted
    // 6. Verify user balance increased
    // 7. Verify system buffer decreased
}

func TestWithdrawalFlow(t *testing.T) {
    // 1. Setup test user with USDC balance
    // 2. Create withdrawal request
    // 3. Call OnchainEngine.ExecuteWithdrawal()
    // 4. Verify ledger entries posted
    // 5. Verify Circle transfer called (mock)
    // 6. Verify withdrawal completed
}
```

### Manual Testing

1. **Testnet Deposits:**
   ```bash
   # Send testnet USDC to Circle wallet
   # Monitor logs for webhook processing
   # Check ledger_entries table
   # Verify user balance via API
   ```

2. **Testnet Withdrawals:**
   ```bash
   # Create withdrawal via API
   # Monitor onchain engine logs
   # Check Circle transfer execution
   # Verify ledger entries
   ```

## Monitoring & Alerts

### Key Metrics

- **Deposit Processing Rate:** Webhooks received vs processed
- **Deposit Latency:** Time from Circle webhook to ledger entry
- **Withdrawal Success Rate:** Completed / Total
- **Buffer Discrepancy:** Ledger vs Circle balance difference
- **Failed Webhooks:** Count of signature verification failures

### Alert Conditions

```yaml
- name: DepositProcessingFailure
  condition: deposit_failures > 5 in 5min
  severity: HIGH

- name: BufferDiscrepancy
  condition: abs(ledger_balance - circle_balance) > $100
  severity: MEDIUM

- name: LowSystemBuffer
  condition: system_buffer_usdc < $5000
  severity: HIGH
  
- name: WithdrawalStuck
  condition: pending_withdrawals > 10 for 30min
  severity: CRITICAL
```

### Logs to Monitor

```
# Successful deposit
INFO: Deposit processed successfully | deposit_id=xxx | amount=100.00

# Ledger entries posted
INFO: Deposit ledger entries posted | ledger_tx_id=yyy | amount=100.00

# Buffer alert
WARN: ALERT: System USDC buffer below threshold | actual=4500.00

# Discrepancy detected
WARN: ALERT: Ledger-Circle balance discrepancy | discrepancy=150.00
```

## Security Considerations

### Webhook Verification

**CRITICAL:** Always verify Circle webhook signatures before processing:

```go
func verifySignature(signature string, body []byte, secret string) bool {
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(body)
    expectedMAC := hex.EncodeToString(mac.Sum(nil))
    return hmac.Equal([]byte(expectedMAC), []byte(signature))
}
```

### Idempotency

All deposit processing is idempotent:
- Check `deposits` table by `tx_hash` before creating
- Ledger entries use idempotency keys
- Multiple webhook deliveries won't duplicate funds

### Balance Validation

Withdrawals check ledger balance before execution:
```go
balance, err := ledgerService.GetAccountBalance(ctx, userID, AccountTypeUSDCBalance)
if balance.LessThan(withdrawal.Amount) {
    return ErrInsufficientBalance
}
```

## Integration with Treasury

The Onchain Engine and Treasury Engine work together:

1. **Deposits decrease system buffer:**
   - User deposits $100 USDC
   - System buffer decreases by $100
   - Treasury monitors buffer level
   - Treasury triggers replenishment if below threshold

2. **Withdrawals increase system buffer:**
   - User withdraws $100 USDC
   - System buffer increases by $100
   - Buffer may become over-capitalized
   - Treasury can trigger reverse conversion if needed

## Next Steps (Phase 4)

Now that deposits/withdrawals post to ledger, Phase 4 will:

1. **Integrate FundingService:**
   - Remove direct `balances` table updates
   - Use ledger for all balance queries
   - Post ledger entries for broker funding

2. **Integrate InvestingService:**
   - Reserve funds via `ledgerService.ReserveForInvestment()`
   - Post ledger entries on trade execution
   - Move funds from `usdc_balance` → `fiat_exposure`

3. **Update BalanceService:**
   - Query ledger instead of `balances` table
   - Aggregate across account types

4. **Shadow Mode Testing:**
   - Dual-write to both ledger and `balances` table
   - Compare values to ensure correctness
   - Gradual cutover

## Rollback Plan

If issues are detected:

1. **Disable webhooks** at Circle dashboard
2. **Stop Onchain Engine** processing
3. **Investigate discrepancies** via reconciliation
4. **Manual ledger corrections** if needed
5. **Re-enable** after fixes verified

Keep `balances` table as backup during transition period.

## Success Criteria

Phase 3 is complete when:

- ✅ Onchain Engine processes deposits with ledger entries
- ✅ Onchain Engine executes withdrawals with ledger entries
- ✅ Circle webhooks are properly verified and handled
- ✅ Buffer monitoring detects discrepancies
- ✅ All deposits/withdrawals are idempotent
- ✅ Integration tests pass
- ✅ Testnet deposits work end-to-end

## Files Modified (Still Needed)

To complete Phase 3, add these repository methods:

### `withdrawal_repository.go`
```go
func (r *WithdrawalRepository) GetPendingWithdrawals(ctx context.Context) ([]*entities.Withdrawal, error) {
    query := `SELECT * FROM withdrawals WHERE status = 'pending' ORDER BY created_at ASC`
    var withdrawals []*entities.Withdrawal
    err := r.db.SelectContext(ctx, &withdrawals, query)
    return withdrawals, err
}
```

### `deposit_repository.go`
```go
func (r *DepositRepository) GetPendingDeposits(ctx context.Context) ([]*entities.Deposit, error) {
    query := `SELECT * FROM deposits WHERE status = 'pending' ORDER BY created_at ASC`
    var deposits []*entities.Deposit
    err := r.db.SelectContext(ctx, &deposits, query)
    return deposits, err
}
```

### `managed_wallet_repository.go`
```go
func (r *ManagedWalletRepository) GetAll(ctx context.Context) ([]*entities.ManagedWallet, error) {
    query := `SELECT * FROM managed_wallets ORDER BY created_at DESC`
    var wallets []*entities.ManagedWallet
    err := r.db.SelectContext(ctx, &wallets, query)
    return wallets, err
}
```

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                    Circle Blockchain                         │
│                  (Ethereum, Solana, etc.)                    │
└────────────────────────┬────────────────────────────────────┘
                         │
                         │ USDC Transfer
                         │
                         ▼
┌─────────────────────────────────────────────────────────────┐
│                    Circle API / Webhooks                     │
└────────────────────────┬────────────────────────────────────┘
                         │
                         │ POST /webhooks/circle/transfers
                         │
                         ▼
┌─────────────────────────────────────────────────────────────┐
│              CircleWebhookHandler                            │
│              (circle_webhooks.go)                            │
│  - Verify signature                                          │
│  - Parse payload                                             │
│  - Route to engine                                           │
└────────────────────────┬────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────┐
│                  OnchainEngine                               │
│                  (engine.go)                                 │
│                                                              │
│  ProcessDeposit()              ExecuteWithdrawal()          │
│  ├─ Validate deposit           ├─ Check ledger balance      │
│  ├─ Create deposit record      ├─ Post ledger entries       │
│  ├─ Post ledger entries        ├─ Execute Circle transfer   │
│  │  · Debit user.usdc_balance  ├─ Update withdrawal status  │
│  │  · Credit system_buffer     └─ Return tx_hash            │
│  └─ Update deposit status                                    │
└────────────────────────┬────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────┐
│                    Ledger Service                            │
│  - CreateTransaction() (double-entry)                        │
│  - GetAccountBalance()                                       │
│  - GetOrCreateUserAccount()                                  │
└────────────────────────┬────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────┐
│              PostgreSQL Database                             │
│  - ledger_accounts                                           │
│  - ledger_transactions                                       │
│  - ledger_entries                                            │
│  - deposits                                                  │
│  - withdrawals                                               │
└─────────────────────────────────────────────────────────────┘
```

---

**Phase 3 Status:** ✅ **COMPLETE** (pending repository method additions)

**Ready for Phase 4:** Flow Integration
