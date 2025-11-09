# Alpaca Integration Quick Reference

## Usage Examples

### 1. Creating a Brokerage Account

```go
// Initialize service
brokerageService := container.InitializeBrokerageOnboarding()

// Create account from KYC data
account, err := brokerageService.CreateBrokerageAccount(ctx, user, kycSubmission)
if err != nil {
    log.Error("Failed to create account", zap.Error(err))
    return err
}

log.Info("Account created", 
    zap.String("account_id", account.ID),
    zap.String("account_number", account.AccountNumber))
```

### 2. Instant Funding via Journal

```go
// Initialize service
fundingService := container.InitializeInstantFunding(firmAccountNumber)

// Fund user's brokerage account
amount := decimal.NewFromFloat(100.00)
err := fundingService.FundBrokerageAccount(ctx, userID, amount)
if err != nil {
    log.Error("Funding failed", zap.Error(err))
    return err
}

log.Info("Account funded", zap.String("amount", amount.String()))
```

### 3. Executing Investment Baskets

```go
// Initialize executor
basketExecutor := container.InitializeBasketExecutor()

// Get basket allocations
allocations := services.GetBasketAllocations("tech-growth")

// Execute basket
orders, err := basketExecutor.ExecuteBasket(
    ctx,
    alpacaAccountID,
    decimal.NewFromFloat(500.00),
    allocations,
)
if err != nil {
    log.Error("Basket execution failed", zap.Error(err))
    return err
}

log.Info("Basket executed", zap.Int("orders", len(orders)))
```

### 4. Listening to Real-Time Events

```go
// Access service
alpacaService := container.AlpacaService

// Define event handler
handler := func(event alpaca.SSEEvent) error {
    log.Info("Received event", 
        zap.String("type", event.Event),
        zap.ByteString("data", event.Data))
    
    // Process event based on type
    switch alpaca.SSEEventType(event.Event) {
    case alpaca.SSEEventTypeOrderFill:
        // Handle order fill
    case alpaca.SSEEventTypeOrderPartialFill:
        // Handle partial fill
    }
    
    return nil
}

// Listen to trade events with auto-reconnect
go alpacaService.ListenTradeEvents(ctx, handler)
```

### 5. Checking Account Balance

```go
alpacaService := container.AlpacaService

account, err := alpacaService.GetAccountBalance(ctx, accountID)
if err != nil {
    return err
}

log.Info("Account balance",
    zap.String("buying_power", account.BuyingPower.String()),
    zap.String("cash", account.Cash.String()),
    zap.String("portfolio_value", account.PortfolioValue.String()))
```

### 6. Listing Positions

```go
alpacaService := container.AlpacaService

positions, err := alpacaService.ListPositions(ctx, accountID)
if err != nil {
    return err
}

for _, pos := range positions {
    log.Info("Position",
        zap.String("symbol", pos.Symbol),
        zap.String("qty", pos.Qty.String()),
        zap.String("market_value", pos.MarketValue.String()),
        zap.String("unrealized_pl", pos.UnrealizedPL.String()))
}
```

### 7. Creating Individual Orders

```go
alpacaService := container.AlpacaService

// Market order with notional amount (fractional shares)
notional := decimal.NewFromFloat(100.00)
order, err := alpacaService.CreateOrder(ctx, accountID, &entities.AlpacaCreateOrderRequest{
    Symbol:        "AAPL",
    Notional:      &notional,
    Side:          entities.AlpacaOrderSideBuy,
    Type:          entities.AlpacaOrderTypeMarket,
    TimeInForce:   entities.AlpacaTimeInForceDay,
    ClientOrderID: uuid.New().String(),
})

// Limit order with quantity
qty := decimal.NewFromFloat(10.5)
limitPrice := decimal.NewFromFloat(150.00)
order, err := alpacaService.CreateOrder(ctx, accountID, &entities.AlpacaCreateOrderRequest{
    Symbol:      "GOOGL",
    Qty:         &qty,
    Side:        entities.AlpacaOrderSideBuy,
    Type:        entities.AlpacaOrderTypeLimit,
    LimitPrice:  &limitPrice,
    TimeInForce: entities.AlpacaTimeInForceGTC,
})
```

## Predefined Investment Baskets

### Tech Growth
```go
allocations := services.GetBasketAllocations("tech-growth")
// AAPL (20%), MSFT (20%), GOOGL (15%), NVDA (15%), TSLA (15%), META (15%)
```

### Sustainability
```go
allocations := services.GetBasketAllocations("sustainability")
// ICLN (30%), TAN (25%), TSLA (20%), NEE (15%), ENPH (10%)
```

### Balanced ETF
```go
allocations := services.GetBasketAllocations("balanced-etf")
// SPY (40%), QQQ (30%), VTI (20%), AGG (10%)
```

## Configuration

### Environment Variables

```bash
# Alpaca Configuration
ALPACA_API_KEY=your_api_key
ALPACA_API_SECRET=your_api_secret
ALPACA_ENVIRONMENT=sandbox  # or production
ALPACA_FIRM_ACCOUNT=your_firm_account_number
```

### Config File (configs/config.yaml)

```yaml
alpaca:
  api_key: "${ALPACA_API_KEY}"
  api_secret: "${ALPACA_API_SECRET}"
  environment: "${ALPACA_ENVIRONMENT}"
  base_url: ""  # Auto-selected based on environment
  data_base_url: "https://data.alpaca.markets"
  timeout: 30
  firm_account_number: "${ALPACA_FIRM_ACCOUNT}"
```

## API Endpoints

### Investment Routes

```
GET    /api/v1/investment/baskets              # List available baskets
POST   /api/v1/investment/baskets/:type/invest # Invest in basket
```

### Asset Routes

```
GET    /api/v1/assets                    # List all tradable assets
GET    /api/v1/assets/search             # Search assets
GET    /api/v1/assets/popular            # Get popular assets
GET    /api/v1/assets/:symbol_or_id      # Get asset details
```

## Error Handling

```go
// Check for specific Alpaca errors
if apiErr, ok := err.(*entities.AlpacaErrorResponse); ok {
    switch apiErr.Code {
    case http.StatusTooManyRequests:
        // Handle rate limit
    case http.StatusBadRequest:
        // Handle validation error
    case http.StatusUnauthorized:
        // Handle auth error
    default:
        // Handle other errors
    }
}
```

## Testing

### Unit Test Example

```go
func TestBasketExecutor(t *testing.T) {
    // Create mock service
    mockService := &MockAlpacaService{}
    
    // Initialize executor
    executor := services.NewBasketExecutor(mockService, logger)
    
    // Test execution
    orders, err := executor.ExecuteBasket(ctx, accountID, amount, allocations)
    
    assert.NoError(t, err)
    assert.Len(t, orders, len(allocations))
}
```

### Integration Test Example

```go
func TestAlpacaIntegration(t *testing.T) {
    // Skip if not in integration test mode
    if testing.Short() {
        t.Skip("Skipping integration test")
    }
    
    // Initialize real service
    config := alpaca.Config{
        APIKey:      os.Getenv("ALPACA_API_KEY"),
        APISecret:   os.Getenv("ALPACA_API_SECRET"),
        Environment: "sandbox",
    }
    
    client := alpaca.NewClient(config, logger)
    service := alpaca.NewService(client, logger)
    
    // Test account creation
    account, err := service.CreateAccount(ctx, req)
    assert.NoError(t, err)
    assert.NotEmpty(t, account.ID)
}
```

## Best Practices

1. **Always use context with timeout**
   ```go
   ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
   defer cancel()
   ```

2. **Handle errors appropriately**
   ```go
   if err != nil {
       log.Error("Operation failed", zap.Error(err))
       return fmt.Errorf("alpaca operation: %w", err)
   }
   ```

3. **Use decimal for money**
   ```go
   amount := decimal.NewFromFloat(100.00)  // Good
   // amount := 100.00  // Bad - never use float64 for money
   ```

4. **Log important operations**
   ```go
   log.Info("Creating order",
       zap.String("symbol", symbol),
       zap.String("amount", amount.String()))
   ```

5. **Use client order IDs for tracking**
   ```go
   ClientOrderID: uuid.New().String()
   ```

## Troubleshooting

### Issue: Rate Limit Exceeded
**Solution**: Circuit breaker will automatically handle this. Wait for backoff period.

### Issue: Account Not Found
**Solution**: Ensure account was created successfully and ID is correct.

### Issue: Insufficient Buying Power
**Solution**: Check account balance before placing orders.

### Issue: SSE Connection Drops
**Solution**: Use `ListenWithReconnect` for automatic reconnection.

## Support

- **Documentation**: `/docs/ALPACA_INTEGRATION_VERIFICATION.md`
- **Refactoring Guide**: `/docs/ALPACA_REFACTORING_SUMMARY.md`
- **API Reference**: Swagger UI at `/swagger/index.html`

---

**Last Updated**: 2024
**Version**: 1.0
