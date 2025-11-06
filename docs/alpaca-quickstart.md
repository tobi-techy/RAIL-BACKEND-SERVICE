# Alpaca Funding Quick Start Guide

## Setup (5 minutes)

### 1. Get Alpaca Sandbox Credentials

1. Sign up at [Alpaca Broker API Sandbox](https://broker-app.sandbox.alpaca.markets/)
2. Create a new application
3. Copy your API Key and Secret

### 2. Configure Environment

Update `.env`:

```bash
ALPACA_API_KEY=your-sandbox-api-key
ALPACA_API_SECRET=your-sandbox-api-secret
ALPACA_BASE_URL=https://broker-api.sandbox.alpaca.markets
ALPACA_ENVIRONMENT=sandbox
```

### 3. Create Test Account

```bash
curl -X POST https://broker-api.sandbox.alpaca.markets/v1/accounts \
  -u "API_KEY:API_SECRET" \
  -H "Content-Type: application/json" \
  -d '{
    "contact": {
      "email_address": "test@example.com",
      "phone_number": "+15555551234",
      "street_address": ["123 Main St"],
      "city": "San Francisco",
      "state": "CA",
      "postal_code": "94105"
    },
    "identity": {
      "given_name": "John",
      "family_name": "Doe",
      "date_of_birth": "1990-01-01",
      "tax_id": "123456789",
      "tax_id_type": "USA_SSN",
      "country_of_citizenship": "USA",
      "country_of_birth": "USA",
      "country_of_tax_residence": "USA",
      "funding_source": ["employment_income"]
    },
    "disclosures": {
      "is_control_person": false,
      "is_affiliated_exchange_or_finra": false,
      "is_politically_exposed": false,
      "immediate_family_exposed": false
    },
    "agreements": [
      {
        "agreement": "account",
        "signed_at": "2024-01-01T00:00:00Z",
        "ip_address": "127.0.0.1"
      }
    ]
  }'
```

Save the `account_number` from the response.

## Usage Examples

### Example 1: Initiate Instant Funding

```go
package main

import (
    "context"
    "log"
    
    "github.com/shopspring/decimal"
    "github.com/stack-service/stack_service/internal/adapters/alpaca"
    "github.com/stack-service/stack_service/internal/domain/entities"
    "go.uber.org/zap"
)

func main() {
    // Initialize Alpaca client
    config := alpaca.Config{
        APIKey:      "your-api-key",
        APISecret:   "your-api-secret",
        Environment: "sandbox",
    }
    
    logger, _ := zap.NewDevelopment()
    client := alpaca.NewClient(config, logger)
    adapter := alpaca.NewFundingAdapter(client, logger)
    
    // Create instant funding request
    req := &entities.AlpacaInstantFundingRequest{
        AccountNo:       "123456789", // Your test account number
        SourceAccountNo: "SI",
        Amount:          decimal.NewFromFloat(100.00),
    }
    
    // Initiate funding
    ctx := context.Background()
    resp, err := adapter.InitiateInstantFunding(ctx, req)
    if err != nil {
        log.Fatal(err)
    }
    
    log.Printf("Instant funding initiated: %s", resp.ID)
    log.Printf("Status: %s", resp.Status)
    log.Printf("Deadline: %s", resp.Deadline)
}
```

### Example 2: Check Account Balance

```go
// Get account balance
account, err := adapter.GetAccountBalance(ctx, "account-id")
if err != nil {
    log.Fatal(err)
}

log.Printf("Buying Power: %s", account.BuyingPower.String())
log.Printf("Cash: %s", account.Cash.String())
log.Printf("Status: %s", account.Status)
```

### Example 3: Create Journal Entry

```go
// Transfer funds between accounts
req := &entities.AlpacaJournalRequest{
    FromAccount: "RF", // Firm account
    ToAccount:   "123456789",
    EntryType:   "JNLC", // Cash journal
    Amount:      decimal.NewFromFloat(50.00),
    Description: "Test transfer",
}

resp, err := adapter.CreateJournal(ctx, req)
if err != nil {
    log.Fatal(err)
}

log.Printf("Journal created: %s", resp.ID)
log.Printf("Status: %s", resp.Status)
```

### Example 4: Complete Funding Flow

```go
package main

import (
    "context"
    "log"
    "time"
    
    "github.com/google/uuid"
    "github.com/shopspring/decimal"
    "github.com/stack-service/stack_service/internal/domain/services/funding"
)

func fundUserAccount(
    fundingService *funding.Service,
    depositID uuid.UUID,
    alpacaAccountID string,
    amount decimal.Decimal,
) error {
    ctx := context.Background()
    
    // Initiate broker funding
    err := fundingService.InitiateBrokerFunding(
        ctx,
        depositID,
        alpacaAccountID,
        amount,
    )
    
    if err != nil {
        return err
    }
    
    log.Printf("Broker funding completed for deposit %s", depositID)
    return nil
}
```

## Testing Checklist

### ✅ Sandbox Testing

- [ ] Create test Alpaca account
- [ ] Initiate instant funding ($100)
- [ ] Verify buying power increased
- [ ] Check instant funding status
- [ ] Create journal entry
- [ ] Verify journal executed
- [ ] Check account balance
- [ ] Test error handling (invalid account)
- [ ] Test retry logic (simulate timeout)

### ✅ Integration Testing

- [ ] Simulate complete deposit flow
- [ ] Verify deposit status updates
- [ ] Check audit trail completeness
- [ ] Test concurrent funding requests
- [ ] Verify idempotency
- [ ] Test circuit breaker behavior

## Common Issues & Solutions

### Issue 1: "Account not found"

**Solution**: Verify account ID is correct and account exists in sandbox.

```bash
curl -X GET https://broker-api.sandbox.alpaca.markets/v1/accounts/{account_id} \
  -u "API_KEY:API_SECRET"
```

### Issue 2: "Insufficient instant funding limit"

**Solution**: Check available limits:

```bash
curl -X GET https://broker-api.sandbox.alpaca.markets/v1/instant_funding/limits \
  -u "API_KEY:API_SECRET"
```

Default sandbox limit is $100,000 total.

### Issue 3: "Account not active"

**Solution**: Account must be in ACTIVE status. Check account status:

```go
account, _ := client.GetAccount(ctx, accountID)
log.Printf("Status: %s", account.Status)
```

If status is APPROVAL_PENDING, wait for approval or contact Alpaca support.

### Issue 4: Circuit breaker open

**Solution**: Circuit breaker opens after 5 consecutive failures. Wait 30 seconds for it to reset, or check Alpaca API status.

## Monitoring

### Key Metrics to Track

```go
// Success rate
successRate := float64(successfulFundings) / float64(totalFundings) * 100

// Average latency
avgLatency := totalLatencyMs / totalFundings

// Circuit breaker state
if circuitBreaker.State() == gobreaker.StateOpen {
    log.Warn("Circuit breaker is open!")
}
```

### Logs to Monitor

```bash
# Successful funding
grep "Instant funding initiated" logs/app.log

# Failed funding
grep "Failed to initiate instant funding" logs/app.log

# Circuit breaker trips
grep "Circuit breaker state changed" logs/app.log
```

## Production Checklist

Before going to production:

- [ ] Replace sandbox credentials with production keys
- [ ] Update base URL to production endpoint
- [ ] Implement settlement automation (T+1)
- [ ] Set up monitoring and alerting
- [ ] Configure proper instant funding limits
- [ ] Implement webhook handling
- [ ] Add comprehensive unit tests
- [ ] Run load testing
- [ ] Document runbook for operations team
- [ ] Set up error notification system

## Resources

- [Alpaca Broker API Docs](https://docs.alpaca.markets/docs/getting-started-with-broker-api)
- [Instant Funding Guide](https://docs.alpaca.markets/docs/draft-instant-funding)
- [API Reference](https://docs.alpaca.markets/reference)
- [Alpaca Slack Community](https://alpaca.markets/slack)

## Support

- **Alpaca Support**: support@alpaca.markets
- **Sandbox Issues**: Use Alpaca Slack #broker-api channel
- **Internal**: #stack-funding-team

## Next Steps

1. ✅ Complete sandbox testing
2. ✅ Add unit tests
3. ✅ Implement webhook handling
4. ✅ Set up monitoring
5. ✅ Production deployment

Happy coding! 🚀
