# Alpaca Implementation Guide for STACK

## Overview

This guide documents the complete implementation of Alpaca integration aligned with STACK's goals for smooth UX.

## Implementation Components

### 1. Brokerage Onboarding (`brokerage_onboarding.go`)

**Purpose**: Automatically create Alpaca brokerage accounts from STACK KYC data

**Flow**:
```
User completes KYC → STACK validates → Create Alpaca account → Link to user
```

**Key Features**:
- Maps STACK KYC fields to Alpaca format
- Handles contact, identity, disclosures, agreements
- Returns Alpaca account ID for linking

**Usage**:
```go
account, err := brokerageService.CreateBrokerageAccount(ctx, user, kycSubmission)
// Store account.ID and account.AccountNumber in virtual_accounts table
```

### 2. Instant Funding (`instant_funding.go`)

**Purpose**: Journal USD from STACK's firm account to user's Alpaca account instantly

**Flow**:
```
Stablecoin deposit → Due off-ramp → USD in virtual account → Journal to Alpaca → Buying power available
```

**Key Features**:
- Uses Alpaca journal entries (JNLC) for instant transfer
- Updates buying power in real-time
- No ACH delays (instant vs 10-30 minutes)

**Usage**:
```go
err := fundingService.FundBrokerageAccount(ctx, userID, usdAmount)
// User can invest immediately
```

### 3. Basket Executor (`basket_executor.go`)

**Purpose**: Execute batch orders for curated investment baskets with fractional shares

**Flow**:
```
User selects basket → Calculate allocations → Place notional orders → Track execution
```

**Key Features**:
- Predefined baskets: Tech Growth, Sustainability, Balanced ETF
- Fractional share support via notional orders
- Batch execution with error handling
- Dollar-based investing (no need to calculate shares)

**Baskets**:
- **Tech Growth**: AAPL, MSFT, GOOGL, NVDA, TSLA, META
- **Sustainability**: ICLN, TAN, TSLA, NEE, ENPH
- **Balanced ETF**: SPY, QQQ, VTI, AGG

**Usage**:
```go
orders, err := basketExecutor.ExecuteBasket(ctx, alpacaAccountID, amount, allocations)
// Returns array of order responses
```

### 4. SSE Event Listener (`sse_listener.go`)

**Purpose**: Real-time order and account updates via Server-Sent Events

**Flow**:
```
Alpaca SSE stream → Parse events → Update database → Notify user
```

**Key Features**:
- Listens to trade executions
- Auto-reconnects on disconnect
- Updates order status in real-time
- Graceful shutdown support

**Events Monitored**:
- Trade executions (filled, partially filled)
- Journal status updates
- Account changes

**Usage**:
```go
listener := NewSSEListener(baseURL, apiKey, apiSecret, orderRepo, positionRepo, logger)
listener.Start(ctx)
defer listener.Stop()
```

### 5. Investment Handlers (`investment_handlers.go`)

**Purpose**: HTTP endpoints for basket investment with smooth UX

**Endpoints**:

#### GET `/api/v1/investment/baskets`
Returns available investment baskets
```json
{
  "baskets": [
    {
      "id": "tech-growth",
      "name": "Tech Growth",
      "description": "High-growth technology stocks",
      "min_amount": "10.00",
      "assets": ["AAPL", "MSFT", "GOOGL", "NVDA", "TSLA", "META"]
    }
  ]
}
```

#### POST `/api/v1/investment/baskets/:basket_type/invest`
Invest in a basket
```json
{
  "amount": 100.00
}
```

Response:
```json
{
  "order_count": 6,
  "order_ids": ["order-id-1", "order-id-2", ...],
  "message": "Basket orders placed successfully"
}
```

## Complete User Flow

### Step 1: User Onboarding
```
1. User signs up → KYC verification
2. STACK creates Alpaca brokerage account
3. Account linked to user profile
```

### Step 2: Deposit & Funding
```
1. User deposits USDC to Circle wallet
2. Due off-ramps USDC → USD
3. USD appears in virtual account
4. Instant funding journals USD to Alpaca
5. Buying power available immediately
```

### Step 3: Investment
```
1. User browses baskets
2. Selects "Tech Growth" basket
3. Enters $100 investment amount
4. System checks buying power
5. Executes 6 fractional orders
6. Real-time updates via SSE
7. Portfolio updated
```

### Step 4: Monitoring
```
1. SSE listener receives trade events
2. Order status updated in database
3. User sees real-time execution
4. Portfolio P&L calculated
5. AI CFO analyzes performance
```

## Configuration

Add to `configs/config.yaml`:
```yaml
alpaca:
  api_key: "${ALPACA_API_KEY}"
  secret_key: "${ALPACA_SECRET_KEY}"
  base_url: "https://broker-api.sandbox.alpaca.markets"
  data_base_url: "https://data.alpaca.markets"
  environment: "sandbox"
  timeout: 30
  firm_account_number: "927721227"  # Your firm account for journaling
```

## Database Schema Updates

### virtual_accounts table
```sql
ALTER TABLE virtual_accounts 
ADD COLUMN alpaca_account_id VARCHAR(255),
ADD COLUMN alpaca_account_number VARCHAR(50);
```

### balances table
```sql
ALTER TABLE balances
ADD COLUMN alpaca_account_id VARCHAR(255);
```

## Integration Checklist

- [x] Brokerage onboarding service
- [x] Instant funding via journaling
- [x] Basket executor with fractional shares
- [x] SSE event listener
- [x] Investment HTTP handlers
- [x] Route registration
- [ ] Add to DI container
- [ ] Database migrations
- [ ] Integration tests
- [ ] Frontend integration

## Testing

### Unit Tests
```bash
go test ./internal/domain/services/...
```

### Integration Test Flow
```bash
# 1. Create test user with KYC
# 2. Create Alpaca account
# 3. Fund with $100
# 4. Invest in basket
# 5. Verify orders placed
# 6. Check SSE events
```

### Manual Testing
```bash
# Get baskets
curl http://localhost:8080/api/v1/investment/baskets \
  -H "Authorization: Bearer $TOKEN"

# Invest in basket
curl -X POST http://localhost:8080/api/v1/investment/baskets/tech-growth/invest \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"amount": 100.00}'
```

## Performance Considerations

### Instant Funding
- Journal entries execute in <1 second
- No ACH delays
- Immediate buying power

### Basket Execution
- Parallel order placement (future optimization)
- Fractional shares eliminate rounding issues
- Dollar-based orders simplify UX

### Real-time Updates
- SSE provides sub-second latency
- Auto-reconnect ensures reliability
- Minimal server overhead

## Error Handling

### Insufficient Funds
```json
{
  "code": "INSUFFICIENT_FUNDS",
  "error": "Insufficient buying power"
}
```

### Basket Not Found
```json
{
  "code": "BASKET_NOT_FOUND",
  "error": "Invalid basket type"
}
```

### Execution Failure
```json
{
  "code": "EXECUTION_ERROR",
  "error": "Failed to execute basket"
}
```

## Monitoring

### Key Metrics
- Funding success rate (target: >99%)
- Average funding time (target: <2s)
- Order execution rate
- SSE connection uptime

### Alerts
- Funding failures
- SSE disconnections
- Order rejections
- Circuit breaker trips

## Next Steps

1. **Add to DI Container**: Wire up services in `internal/infrastructure/di/container.go`
2. **Database Migrations**: Add Alpaca account fields
3. **Frontend Integration**: Build basket selection UI
4. **Testing**: Comprehensive integration tests
5. **Monitoring**: Set up dashboards and alerts

## Support

- Alpaca Docs: https://docs.alpaca.markets/docs/getting-started-with-broker-api
- STACK Integration Review: `docs/alpaca-integration-review.md`
- Setup Guide: `docs/alpaca-setup-guide.md`
