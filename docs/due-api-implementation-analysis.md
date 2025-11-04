# Due API Off-Ramp Implementation Analysis

**Date**: 2025-11-03  
**Status**: Critical Issues Found - Requires Immediate Fixes

## Executive Summary

After reviewing the codebase against the official Due API documentation (https://due.readme.io/docs/stablecoin-to-fiat-transfers), several critical implementation gaps and architectural issues have been identified that prevent the proper functioning of the USDC-to-USD off-ramp flow.

## Acceptance Criteria Status

| # | Requirement | Status | Notes |
|---|-------------|---------|-------|
| 1 | Deposit Detection | ✅ **PASS** | Circle webhook handler properly detects confirmed USDC deposits |
| 2 | Due Transfer Initiation | ⚠️ **PARTIAL** | Logic exists but uses placeholder values; **critical missing: funding address implementation** |
| 3 | Virtual Account Crediting | ❌ **FAIL** | No mechanism to credit USD to user's virtual account after off-ramp completion |
| 4 | Status Tracking | ⚠️ **PARTIAL** | Migration adds required fields, but workflow incomplete |
| 5 | Error Handling | ✅ **PASS** | Exponential backoff and error handling implemented |
| 6 | Circuit Breaker | ✅ **PASS** | Circuit breaker protection implemented in Due API client |
| 7 | Audit Logging | ✅ **PASS** | Structured logging with correlation IDs implemented |

## Critical Issues

### 1. **Missing Funding Address Implementation (BLOCKER)**

**Severity**: 🔴 **CRITICAL**

**Issue**: The Due API documentation shows two methods for completing a transfer:
- **5.a.** Create Transfer Intent (requires client-side signing)
- **5.b.** Create Funding Address (simpler, recommended for USDC on EVM networks)

**Current State**: The code implements Transfer Intent creation but **never uses it**. The simpler and recommended Funding Address method (5.b) is **NOT implemented** at all.

**Impact**: Deposits will remain stuck in "off_ramp_initiated" status because no blockchain transaction is ever sent to Due.

**Location**: `internal/domain/services/funding/service.go:454-526`

**Required Fix**:
```go
// After creating the Due transfer (step 6 in InitiateDueTransfer)
transfer, err := s.dueAPI.CreateTransfer(ctx, transferReq)
if err != nil {
    return "", fmt.Errorf("failed to create transfer: %w", err)
}

// MISSING: Create funding address for user to send USDC
fundingAddr, err := s.dueAPI.CreateFundingAddress(ctx, transfer.ID)
if err != nil {
    return "", fmt.Errorf("failed to create funding address: %w", err)
}

// MISSING: Initiate blockchain transfer from Circle wallet to funding address
// This requires Circle API integration to send USDC from managed wallet to fundingAddr.Details.Address
err = s.circleAPI.TransferFromWallet(ctx, managedWalletID, fundingAddr.Details.Address, deposit.Amount)
if err != nil {
    return "", fmt.Errorf("failed to send funds to Due: %w", err)
}
```

### 2. **No Virtual Account Crediting After Off-Ramp Completion (BLOCKER)**

**Severity**: 🔴 **CRITICAL**

**Issue**: When a Due transfer completes successfully, the system has no mechanism to:
1. Detect completion (webhook handler is a stub)
2. Credit USD to user's virtual account or buying power
3. Update deposit status to "off_ramp_complete"

**Current State**:
- `DueTransferWebhook` handler exists but is not implemented (`processDueTransferWebhook` just logs)
- No repository method to query deposits by Due transfer reference
- No balance crediting logic after successful off-ramp

**Location**:
- `internal/api/handlers/funding_investing_handlers.go:775-790`
- `internal/infrastructure/repositories/deposit_repository.go` (missing method)

**Required Fix**:
1. Implement `GetByDueTransferRef` in DepositRepository
2. Complete webhook handler to:
   - Query deposit by transfer reference
   - Update status based on webhook status ("completed" → "off_ramp_complete")
   - Credit buying power to user's Alpaca account (next step in flow)
   - Handle failure cases

### 3. **Placeholder/Invalid Transfer Parameters (HIGH)**

**Severity**: 🟠 **HIGH**

**Issue**: The `InitiateDueTransfer` method uses placeholder values that will cause API errors:

```go
// Line 473: Placeholder recipient ID
recipientID := "recipient_" + virtualAccountID.String() // Invalid

// Line 477: Invalid sender address format
senderAddress := "0x" + deposit.UserID.String() // UUIDs are not Ethereum addresses

// Lines 428-432: Hardcoded virtual account parameters
destination := "wlt_" + userID.String() // Invalid wallet address
schemaIn := "bank_sepa" // Wrong - should be "evm" for crypto deposits
currencyIn := "EUR" // Wrong - should be "USDC"
railOut := "ethereum" // Should be "ach" or "sepa" for fiat
currencyOut := "USDC" // Should be "USD" for off-ramp
```

**Location**: 
- `internal/domain/services/funding/service.go:428-432, 473, 477`

**Required Fix**:
1. Obtain actual Circle wallet address for sender
2. Create proper Due recipient with bank account details
3. Fix virtual account creation parameters to match off-ramp use case

### 4. **Missing Circle to Due Integration (HIGH)**

**Severity**: 🟠 **HIGH**

**Issue**: The code creates a Due transfer but never actually sends the USDC from the Circle managed wallet to Due's funding address. The Circle API adapter is missing the transfer method.

**Location**: `internal/adapters/circle/` (missing method)

**Required Fix**: Implement `TransferFromWallet` method in Circle API adapter to send USDC to Due's funding address.

### 5. **Incorrect Virtual Account Creation Parameters (MEDIUM)**

**Severity**: 🟡 **MEDIUM**

**Issue**: The `CreateVirtualAccount` method creates virtual accounts with wrong parameters for the off-ramp use case.

**Current (Wrong)**:
- `schemaIn: "bank_sepa"` → expects bank deposits
- `currencyIn: "EUR"` → expects Euro deposits
- `railOut: "ethereum"` → sends crypto out
- `currencyOut: "USDC"` → sends USDC out

**Should Be (for off-ramp)**:
- `schemaIn: "evm"` → accepts blockchain deposits
- `currencyIn: "USDC"` → accepts USDC deposits  
- `railOut: "ach"` or `"sepa"` → sends fiat out
- `currencyOut: "USD"` or `"EUR"` → sends fiat currency out

**Location**: `internal/domain/services/funding/service.go:428-432`

## Implementation Gaps

### Missing Components

1. **Recipient Management**
   - No API endpoints to create/manage Due recipients
   - No database table for storing recipient information
   - No user flow to collect bank account details

2. **Quote Management**
   - Quote creation exists in API but not used in flow
   - No quote validation before transfer creation
   - No FX rate display to users

3. **Transfer Monitoring**
   - No polling mechanism for transfer status
   - No timeout handling for expired transfers
   - No reconciliation process for stuck transfers

4. **Balance Management**
   - Missing logic to credit buying power after successful off-ramp
   - Missing Alpaca account funding trigger
   - No handling of partial failures

## Architecture Issues

### 1. Synchronous Processing in Webhook Handler

**Issue**: The deposit webhook handler calls `InitiateDueTransfer` synchronously, which can timeout for slow API calls.

**Recommendation**: Use async job queue (SQS) for off-ramp processing.

### 2. Missing Saga Pattern Implementation

**Issue**: The off-ramp flow is a multi-step distributed transaction:
1. Circle deposit detection
2. Due transfer creation
3. Due funding address creation
4. Circle → Due blockchain transfer
5. Due processing and fiat conversion
6. Virtual account crediting
7. Alpaca account funding

Each step can fail independently, but there's no saga coordinator to manage state and compensating transactions.

**Recommendation**: Implement saga orchestrator as specified in project rules.

### 3. Lack of Idempotency Keys

**Issue**: Due API calls don't use idempotency keys, risking duplicate transfers on retries.

**Recommendation**: Add idempotency key support to Due API client.

## Correct Due API Flow (Per Documentation)

Based on https://due.readme.io/docs/stablecoin-to-fiat-transfers, the correct flow should be:

```
1. Get Available Channels
   GET /v1/channels
   → Verify ethereum/USDC → ach/USD is supported

2. Create Recipient (if not exists)
   POST /v1/recipients
   → Store recipient ID for user

3. Generate Quote
   POST /v1/transfers/quote
   → Get real-time pricing and fees

4. Create Transfer
   POST /v1/transfers
   → Returns transfer with status "awaiting_funds"

5. Create Funding Address
   POST /v1/transfers/{transfer_id}/funding_address
   → Returns temporary deposit address

6. Send Funds (via Circle API)
   → Transfer exact amount from Circle wallet to funding address
   → Must be single transaction from authorized sender

7. Monitor Transfer Status (via webhook or polling)
   → Due processes automatically when funds received
   → Updates to "processing" → "completed" or "failed"

8. Credit User Account
   → Update deposit status to "off_ramp_complete"
   → Credit USD to virtual account
   → Trigger Alpaca funding
```

## Recommended Fixes (Priority Order)

### Phase 1: Critical Blockers (Immediate)

1. **Implement Funding Address Flow**
   - Add `CreateFundingAddress` call after `CreateTransfer`
   - Implement Circle → Due blockchain transfer
   - Test end-to-end with sandbox environment

2. **Complete Webhook Handler**
   - Implement `GetByDueTransferRef` in repository
   - Add balance crediting logic
   - Add status tracking to "off_ramp_complete"

3. **Fix Parameter Placeholders**
   - Get real Circle wallet address for sender
   - Create proper Due recipients
   - Fix virtual account creation parameters

### Phase 2: Required for Production (Week 1)

4. **Implement Recipient Management**
   - Add recipient API endpoints
   - Add recipient database table
   - Add user bank account collection flow

5. **Add Transfer Monitoring**
   - Implement status polling background job
   - Add timeout handling
   - Add reconciliation process

6. **Implement Saga Orchestrator**
   - Create orchestrator for multi-step flow
   - Add compensating transactions
   - Add state machine for deposit statuses

### Phase 3: Enhancements (Week 2)

7. **Add Quote Display**
   - Show FX rates and fees to users
   - Add quote expiration handling

8. **Add Idempotency Keys**
   - Implement for all Due API calls
   - Prevent duplicate transfers

9. **Add Comprehensive Testing**
   - Integration tests with Due sandbox
   - Failure scenario tests
   - Load tests

## Testing Recommendations

### Unit Tests
- ✅ Due API client methods (already exists)
- ❌ FundingService with funding address flow
- ❌ Webhook handler completion logic

### Integration Tests
- ❌ End-to-end deposit → off-ramp → credit flow
- ❌ Circle → Due transfer integration
- ❌ Error scenarios and retries

### Sandbox Testing
1. Create test Due account
2. Link test Circle wallet
3. Process test USDC deposit
4. Verify USD credited correctly

## Conclusion

The current implementation has the foundation (circuit breaker, retry logic, logging) but is missing critical components to actually complete the off-ramp flow. The most critical gap is the lack of funding address implementation and balance crediting after successful off-ramp.

**Estimated Effort**:
- Phase 1 (Blockers): 3-5 days
- Phase 2 (Production Ready): 5-7 days  
- Phase 3 (Enhancements): 3-5 days
- **Total**: 11-17 days

**Risk**: High - Current implementation will fail silently in production, leaving user funds stuck in "off_ramp_initiated" status with no path to completion.

## Next Steps

1. Review this analysis with team
2. Prioritize Phase 1 critical fixes
3. Set up Due sandbox environment
4. Implement and test funding address flow
5. Complete webhook handler
6. End-to-end testing before production deployment
