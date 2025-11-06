# Alpaca Account Funding Integration

## Overview

This document describes the implementation of Alpaca brokerage account funding for the STACK platform. The integration enables instant funding of user brokerage accounts after stablecoin off-ramp completion, providing immediate buying power for stock and ETF trading.

## Architecture

### Funding Flow

```
User Deposits USDC
    ↓
Circle Wallet (Managed)
    ↓
Due Off-Ramp (USDC → USD)
    ↓
Virtual Account (Due)
    ↓
Alpaca Instant Funding ← [THIS IMPLEMENTATION]
    ↓
Buying Power Available
```

### Key Components

1. **Alpaca Funding Adapter** (`internal/adapters/alpaca/funding.go`)
   - Handles instant funding transfers
   - Manages journal entries for fund movement
   - Retrieves account balances

2. **Funding Service** (`internal/domain/services/funding/service.go`)
   - Orchestrates broker funding after off-ramp completion
   - Updates deposit status tracking
   - Coordinates with Alpaca adapter

3. **Entities** (`internal/domain/entities/alpaca_entities.go`)
   - `AlpacaInstantFundingRequest/Response`
   - `AlpacaJournalRequest/Response`
   - Supporting types for fees, interests, and limits

## Alpaca Instant Funding

### What is Instant Funding?

Instant Funding allows broker partners to extend buying power to customer accounts immediately, without waiting for funds to settle. This enables customers to trade stocks instantly while the actual settlement happens on T+1.

### Key Features

- **Immediate Buying Power**: Users can trade immediately after deposit
- **T+1 Settlement**: Actual funds must be settled by 1 PM ET on T+1
- **Default Limits**: $1,000 per account, $100,000 total (configurable)
- **Interest Charges**: Late settlements incur FED UB + 8% interest
- **Auto-Cancellation**: Unreconciled transfers cancel at 8 PM ET on T+1

### Implementation

#### 1. Initiate Instant Funding

```go
// After off-ramp completion, initiate instant funding
req := &entities.AlpacaInstantFundingRequest{
    AccountNo:       alpacaAccountNumber,
    SourceAccountNo: "SI", // Source account for instant funding
    Amount:          usdAmount,
}

resp, err := alpacaAdapter.InitiateInstantFunding(ctx, req)
```

**API Endpoint**: `POST /v1/instant_funding`

**Response**:
```json
{
  "id": "fcc6d9fc-ce36-484a-bd86-a27b98c2d1ab",
  "account_no": "{ACCOUNT_NO}",
  "amount": "20",
  "status": "PENDING",
  "deadline": "2024-11-13",
  "remaining_payable": "20",
  "total_interest": "0"
}
```

#### 2. Check Funding Status

```go
status, err := alpacaAdapter.GetInstantFundingStatus(ctx, transferID)
```

**API Endpoint**: `GET /v1/instant_funding/{transfer_id}`

**Status Values**:
- `PENDING`: Transfer created, awaiting execution
- `EXECUTED`: Buying power extended to account
- `COMPLETED`: Settlement completed
- `CANCELED`: Transfer canceled
- `FAILED`: Transfer failed

#### 3. Monitor Account Balance

```go
account, err := alpacaAdapter.GetAccountBalance(ctx, accountID)
// account.BuyingPower now reflects instant funding
```

**API Endpoint**: `GET /v1/accounts/{account_id}`

**Response**:
```json
{
  "id": "{ACCOUNT_ID}",
  "buying_power": "100",
  "cash": "100",
  "status": "ACTIVE"
}
```

## Journals API

The Journals API enables cash pooling and internal fund transfers between accounts, useful for reconciliation and settlement.

### Use Cases

1. **Cash Pooling**: Bulk deposit to firm account, then journal to individual accounts
2. **Settlement**: Transfer funds from SI account to customer accounts
3. **Corrections**: Adjust balances for errors or refunds

### Implementation

```go
req := &entities.AlpacaJournalRequest{
    FromAccount:                  "RF", // Firm account
    ToAccount:                    customerAccountNo,
    EntryType:                    "JNLC", // Cash journal
    Amount:                       amount,
    Description:                  "Settlement for instant funding",
    TransmitterName:              customerName,
    TransmitterAccountNumber:     customerAccountNo,
    TransmitterFinancialInstitution: "STACK Platform",
}

resp, err := alpacaAdapter.CreateJournal(ctx, req)
```

**API Endpoint**: `POST /v1/journals`

**Entry Types**:
- `JNLC`: Cash journal
- `JNLS`: Securities journal

## Travel Rule Compliance

Alpaca requires Travel Rule compliance for ALL deposits, regardless of amount. The following information must be provided:

### Required Fields

| Field | Description |
|-------|-------------|
| `transmitter_name` / `originator_full_name` | Full name of customer |
| `transmitter_account_number` / `originator_bank_account_number` | Customer's account number |
| `transmitter_address` / `originator_street_address` | Street address |
| `originator_city` | City |
| `originator_state` | State (if applicable) |
| `originator_country` | Country |
| `transmitter_financial_institution` / `originator_bank_name` | Financial institution name |
| `other_identifying_information` | Bank reference number (recommended) |

### Implementation

Travel Rule information is included in:
1. **Instant Funding Settlement** (`POST /v1/instant_funding/settlements`)
2. **Journals API** (`POST /v1/journals`)

## Error Handling

### Circuit Breaker

The Alpaca client implements a circuit breaker pattern to handle API failures gracefully:

```go
circuitBreaker := gobreaker.NewCircuitBreaker(gobreaker.Settings{
    Name:        "AlpacaBrokerAPI",
    MaxRequests: 5,
    Interval:    10 * time.Second,
    Timeout:     30 * time.Second,
    ReadyToTrip: func(counts gobreaker.Counts) bool {
        return counts.ConsecutiveFailures > 5
    },
})
```

### Retry Logic

Exponential backoff with jitter for transient errors:

```go
// Base: 1s, Max: 16s, Max Retries: 3
backoff := baseBackoff * 2^(attempt-1) + jitter
```

### Retryable Errors

- HTTP 429 (Rate Limit)
- HTTP 5xx (Server Errors)
- Network timeouts
- Connection errors

### Non-Retryable Errors

- HTTP 4xx (Client Errors, except 429)
- Validation errors
- Authentication failures

## Database Schema

### Deposit Status Progression

```
pending_confirmation
    ↓
confirmed
    ↓
off_ramp_initiated
    ↓
off_ramp_completed
    ↓
broker_funded ← [NEW STATUS]
```

### Deposit Table Fields

```sql
CREATE TABLE deposits (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    virtual_account_id UUID,
    amount DECIMAL(20, 8) NOT NULL,
    status VARCHAR(50) NOT NULL,
    tx_hash VARCHAR(255),
    chain VARCHAR(50),
    off_ramp_tx_id VARCHAR(255),
    off_ramp_initiated_at TIMESTAMP,
    off_ramp_completed_at TIMESTAMP,
    alpaca_funding_tx_id VARCHAR(255), -- Instant funding transfer ID
    alpaca_funded_at TIMESTAMP,        -- Timestamp when funded
    created_at TIMESTAMP NOT NULL
);
```

## Configuration

### Environment Variables

```bash
# Alpaca Broker API
ALPACA_API_KEY=your-alpaca-api-key
ALPACA_API_SECRET=your-alpaca-api-secret
ALPACA_BASE_URL=https://broker-api.sandbox.alpaca.markets
ALPACA_ENVIRONMENT=sandbox
```

### Sandbox vs Production

**Sandbox**:
- Base URL: `https://broker-api.sandbox.alpaca.markets`
- Test accounts and simulated trading
- No real money involved

**Production**:
- Base URL: `https://broker-api.alpaca.markets`
- Real accounts and live trading
- Requires production API keys

## Testing

### Unit Tests

```bash
go test ./internal/adapters/alpaca/...
go test ./internal/domain/services/funding/...
```

### Integration Tests

```bash
# With testcontainers for database
go test ./tests/integration/funding_test.go
```

### Manual Testing (Sandbox)

1. Create test Alpaca account
2. Simulate off-ramp completion
3. Trigger instant funding
4. Verify buying power increase
5. Check deposit status updates

## Monitoring & Metrics

### Key Metrics

- **Instant Funding Success Rate**: % of successful instant funding transfers
- **Average Funding Latency**: Time from off-ramp completion to buying power available
- **Settlement Success Rate**: % of transfers settled on time (by 1 PM T+1)
- **Late Settlement Count**: Number of transfers incurring interest charges
- **Circuit Breaker Trips**: Number of times circuit breaker opened

### Logging

All funding operations are logged with structured logging:

```go
logger.Info("Instant funding initiated",
    zap.String("transfer_id", resp.ID),
    zap.String("account_no", req.AccountNo),
    zap.String("amount", req.Amount.String()),
    zap.String("status", resp.Status))
```

## Security Considerations

1. **API Authentication**: Basic Auth with API key/secret
2. **TLS 1.2+**: All API calls use TLS 1.2 or higher
3. **Encrypted Storage**: Alpaca transaction IDs stored securely
4. **Audit Trail**: Complete funding history in deposits table
5. **Idempotency**: Duplicate funding requests prevented via deposit ID checks

## Limitations & Constraints

1. **Instant Funding Limits**:
   - Default: $1,000 per account
   - Total: $100,000 across all accounts
   - Configurable via Alpaca support

2. **Settlement Deadline**:
   - Must settle by 1 PM ET on T+1
   - Late settlements incur interest (FED UB + 8%)
   - Auto-cancel at 8 PM ET on T+1 if not settled

3. **Account Requirements**:
   - Account must be ACTIVE status
   - Account must not be blocked
   - Sufficient limits available

## Future Enhancements

1. **Settlement Automation**: Automatic settlement via wire/ACH on T+1
2. **Limit Management**: Dynamic limit adjustments based on user behavior
3. **Multi-Currency**: Support for non-USD currencies
4. **Batch Processing**: Bulk instant funding for multiple users
5. **Real-Time Notifications**: Push notifications for funding status changes

## References

- [Alpaca Broker API Documentation](https://docs.alpaca.markets/docs/getting-started-with-broker-api)
- [Instant Funding Guide](https://docs.alpaca.markets/docs/draft-instant-funding)
- [Journals API](https://docs.alpaca.markets/docs/funding-via-journals)
- [Travel Rule Requirements](https://www.fincen.gov/sites/default/files/advisory/advissu7.pdf)

## Support

For issues or questions:
- Alpaca Support: support@alpaca.markets
- Alpaca Slack: https://alpaca.markets/slack
- Internal: #stack-funding-team
