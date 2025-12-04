### 1. Dual Balance Systems (Critical)
There are two competing balance systems:
- Legacy Balance table with BuyingPower and PendingDeposits
- New LedgerAccount system with proper double-entry

The funding/service.go uses the legacy system while balance/enhanced_service.go uses the ledger. This creates inconsistency.

Fix needed: Migrate fully to ledger-based balances and deprecate the legacy Balance table.

### 2. Missing Deposit → Ledger Integration
ProcessChainDeposit in funding/service.go updates the legacy Balance table directly:
go
if err := s.balanceRepo.UpdateBuyingPower(ctx, wallet.UserID, usdAmount); err != nil {


It should create ledger entries instead:
go
// Should be:
ledgerService.RecordDeposit(ctx, userID, amount, depositID)


### 3. No Transaction History for Users
The GetFundingConfirmations only returns deposits. Users need a unified transaction history showing:
- Deposits
- Withdrawals
- Investments
- Conversions

### 4. Webhook Security Missing
go
// TODO: Verify webhook signature for security

Both ChainDepositWebhook and BrokerageFillWebhook have this TODO. Critical for production.

### 5. No Rate Limiting on Deposit Address Generation
CreateDepositAddress can be called repeatedly without limits, potentially creating many wallets.

### 6. Missing Deposit Status State Machine
Deposit statuses are strings without validation:
go
Status: "confirmed" // Should be an enum with valid transitions


### 7. No Pending Deposit Timeout
Deposits stuck in "pending" status have no automatic timeout/cleanup mechanism.

### 8. Balance Caching Missing
GetBalance calls Circle API on every request:
go
balanceResp, err := s.circleAPI.GetWalletBalances(ctx, wallet.CircleWalletID)

This should be cached with a short TTL (e.g., 30 seconds).

### 9. No Minimum Deposit Amount
No validation for minimum deposit amounts, which could lead to dust deposits that cost more in gas than they're worth.

### 10. Missing Audit Trail
Deposit/withdrawal operations don't create audit log entries for compliance.

### 11. Treasury Provider Selection
getAccountTypeFromID is a placeholder:
go
func getAccountTypeFromID(accountID uuid.UUID) entities.AccountType {
    // This is a placeholder - in practice, you'd query the ledger
    return entities.AccountTypeSystemBufferUSDC
}

### 12. No Withdrawal Limits
No daily/weekly withdrawal limits per user.

### 13. Missing Notifications
No email/push notifications for:
- Deposit confirmed
- Withdrawal completed
- Large balance changes


### 14. Recommended Deposit & Withdrawal Limits
The core philosophy should be to establish the lowest possible minimum to encourage micro-investing (round-ups) and the highest possible maximum for verified users to accommodate high salary deposits and crypto transfers.I. Deposit Limits (The On-Ramp)Limit TypeSuggested RangeRationale for RailMinimum Deposit$1.00 - $10.00 (or $0 if strictly via Round-ups)Must be extremely low to enable the core "Round-Up" and "micro-investing" features, catering to Gen Z's often limited starting capital.Tier 1 Max (Daily/Monthly)$5,000 daily / $25,000 monthlySuitable for users who have completed basic KYC/Identity Verification. This accommodates the full deposit of an average monthly salary into the Virtual EUR/USD accounts.Tier 2 Max (Monthly)$100,000 - $250,000+For high-volume users, institutional transfers, or high-net-worth clients. This requires Advanced Verification (Proof of Address, Proof of Funds).II. Withdrawal Limits (The Off-Ramp)Withdrawal limits should always be carefully monitored to prevent rapid fund outflow (bank run risk) and ensure compliance.Limit TypeSuggested RangeRationale for RailMinimum Withdrawal$5.00 - $20.00Must be low to allow users to exit small amounts gracefully, but high enough to cover underlying stablecoin network fees (gas or network processing costs) without draining the user's account entirely.Tier 1 Max (Daily)$2,500 - $10,000 dailySlightly lower than the daily deposit limit to encourage funds to remain on the platform, but sufficient to satisfy most withdrawal needs. Must align with KYC limits.Tier 2 Max (Monthly)$50,000 - $150,000For high-net-worth individuals. These limits ensure the platform retains control and visibility over large transactions for AML compliance.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━


## 🔧 Recommended Improvements

### High Priority
1. Unify balance systems - Migrate to ledger-only
2. Implement webhook signature verification
3. Add deposit → ledger integration
4. Implement balance caching

### Medium Priority
5. Add deposit status enum with state machine
6. Implement withdrawal limits
7. Add minimum deposit validation
8. Create unified transaction history endpoint
9. Add audit logging for all financial operations

### Lower Priority
10. Add deposit timeout mechanism
11. Implement notifications
12. Add rate limiting on deposit address creation
13. Fix treasury provider selection

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━


## Architecture Diagram (Current Flow)

User Deposit Flow:
┌─────────┐    ┌──────────┐    ┌─────────────┐    ┌────────────┐
│ Webhook │───▶│ Funding  │───▶│ Legacy      │    │ Ledger     │
│ (Chain) │    │ Service  │    │ Balance Tbl │    │ (unused)   │
└─────────┘    └──────────┘    └─────────────┘    └────────────┘

Should Be:
┌─────────┐    ┌──────────┐    ┌────────────┐    ┌────────────┐
│ Webhook │───▶│ Funding  │───▶│ Ledger     │───▶│ Treasury   │
│ (Chain) │    │ Service  │    │ Service    │    │ Engine     │
└─────────┘    └──────────┘    └────────────┘    └────────────┘


The foundation is solid, but the dual balance system needs resolution before production.