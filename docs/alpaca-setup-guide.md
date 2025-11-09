# Alpaca Integration Setup Guide

## Quick Start (5 Minutes)

### Step 1: Get Alpaca Sandbox Credentials

1. Go to https://broker-app.alpaca.markets/
2. Sign up for a free sandbox account
3. Navigate to **API/Devs > API Keys**
4. Click **Generate New Key**
5. Copy your `API_KEY` and `API_SECRET`

### Step 2: Set Environment Variables

Create or update your `.env` file:

```bash
# Alpaca Sandbox Credentials
export ALPACA_API_KEY="your-sandbox-api-key-here"
export ALPACA_SECRET_KEY="your-sandbox-secret-key-here"
```

Or set them directly in your shell:

```bash
export ALPACA_API_KEY="PK..."
export ALPACA_SECRET_KEY="..."
```

### Step 3: Verify Configuration

The configuration is already added to `configs/config.yaml`:

```yaml
alpaca:
  api_key: "${ALPACA_API_KEY}"
  secret_key: "${ALPACA_SECRET_KEY}"
  base_url: "https://broker-api.sandbox.alpaca.markets"
  data_base_url: "https://data.alpaca.markets"
  environment: "sandbox"
  timeout: 30
```

### Step 4: Test the Integration

```bash
# Start the service
go run cmd/main.go

# Test assets endpoint
curl http://localhost:8080/api/v1/assets?limit=5

# Test asset search
curl http://localhost:8080/api/v1/assets/search?q=AAPL
```

## Sandbox Features

### Pre-funded Firm Account
- Each sandbox account comes with **$50,000** pre-funded
- Use for journaling funds to user accounts
- Perfect for testing instant funding

### Sandbox Behavior
- All prices and execution times match production
- Market hours are simulated
- No real money involved
- ACH transfers simulate 10-30 minute delay

### Available Endpoints

#### Account Management
```bash
# Create account
POST /v1/accounts

# Get account
GET /v1/accounts/{account_id}

# List accounts
GET /v1/accounts
```

#### Funding
```bash
# Instant funding (recommended for STACK)
POST /v1/instant_funding

# Journal entry (alternative)
POST /v1/journals

# ACH relationship (future use)
POST /v1/accounts/{account_id}/ach_relationships
```

#### Trading
```bash
# Create order
POST /v1/trading/accounts/{account_id}/orders

# Get order
GET /v1/trading/accounts/{account_id}/orders/{order_id}

# List orders
GET /v1/trading/accounts/{account_id}/orders
```

#### Assets
```bash
# List assets
GET /v1/assets

# Get asset
GET /v1/assets/{symbol_or_id}
```

## Testing the Funding Flow

### 1. Create a Test User Account

```bash
curl -X POST http://localhost:8080/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{
    "email": "test@example.com",
    "password": "SecurePass123!",
    "given_name": "John",
    "family_name": "Doe"
  }'
```

### 2. Simulate Stablecoin Deposit

```bash
# This would normally come from Circle webhook
# For testing, you can trigger the funding flow directly
```

### 3. Check Buying Power

```bash
curl http://localhost:8080/api/v1/balance \
  -H "Authorization: Bearer YOUR_JWT_TOKEN"
```

### 4. Place a Test Order

```bash
curl -X POST http://localhost:8080/api/v1/baskets/tech-growth/invest \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "amount": 100.00
  }'
```

## Monitoring & Debugging

### Check Logs

```bash
# Watch for Alpaca API calls
tail -f logs/app.log | grep "Alpaca"

# Check funding orchestrator
tail -f logs/app.log | grep "AlpacaFundingOrchestrator"
```

### Common Issues

#### 1. Authentication Failed
```
Error: API error: status 401
```
**Solution**: Verify your API key and secret are correct

#### 2. Account Not Found
```
Error: API error: status 404
```
**Solution**: Ensure you've created an Alpaca account first

#### 3. Insufficient Buying Power
```
Error: insufficient buying power
```
**Solution**: Fund the account via instant funding or journal entry

#### 4. Circuit Breaker Open
```
Error: circuit breaker is open
```
**Solution**: Wait 30 seconds for circuit breaker to reset, check Alpaca API status

### Health Checks

```bash
# Application health
curl http://localhost:8080/health

# Metrics (includes circuit breaker state)
curl http://localhost:8080/metrics
```

## Production Deployment

### Before Going Live

1. **Get Production Credentials**
   - Apply for production access at Alpaca
   - Complete compliance requirements
   - Get production API keys

2. **Update Configuration**
   ```yaml
   alpaca:
     api_key: "${ALPACA_API_KEY}"
     secret_key: "${ALPACA_SECRET_KEY}"
     base_url: "https://broker-api.alpaca.markets"  # Remove 'sandbox'
     environment: "production"
   ```

3. **Set Production Environment Variables**
   ```bash
   export ALPACA_API_KEY="production-key"
   export ALPACA_SECRET_KEY="production-secret"
   ```

4. **Test with Small Amounts**
   - Start with $10-$100 transactions
   - Verify end-to-end flow
   - Monitor for 24 hours

5. **Enable Monitoring**
   - Set up alerts for funding failures
   - Monitor circuit breaker state
   - Track success rates (target: >99%)

### Production Checklist

- [ ] Production API credentials obtained
- [ ] Configuration updated to production URLs
- [ ] Environment variables set in production
- [ ] Monitoring and alerting configured
- [ ] Runbook created for common issues
- [ ] Escalation procedures documented
- [ ] Compliance requirements met
- [ ] Small-scale testing completed
- [ ] Team trained on troubleshooting
- [ ] Rollback plan prepared

## API Reference

### Instant Funding Request

```json
{
  "account_no": "935142145",
  "source_account_no": "927721227",
  "amount": "1000.00"
}
```

### Instant Funding Response

```json
{
  "id": "750d8323-19f6-47d5-8e9a-a34ed4a6f2d2",
  "account_no": "935142145",
  "source_account_no": "927721227",
  "amount": "1000.00",
  "remaining_payable": "1000.00",
  "total_interest": "0.00",
  "status": "PENDING",
  "system_date": "2024-01-15",
  "deadline": "2024-01-16",
  "created_at": "2024-01-15T10:00:00Z"
}
```

### Journal Request

```json
{
  "from_account": "927721227",
  "to_account": "935142145",
  "entry_type": "JNLC",
  "amount": "100.00",
  "description": "Signup bonus"
}
```

### Order Request

```json
{
  "symbol": "AAPL",
  "qty": 0.5,
  "side": "buy",
  "type": "market",
  "time_in_force": "day"
}
```

## Support & Resources

### Alpaca Documentation
- Broker API Docs: https://docs.alpaca.markets/docs/getting-started-with-broker-api
- API Reference: https://docs.alpaca.markets/reference
- Postman Collection: https://www.postman.com/alpacamarkets/workspace/alpaca-public-workspace

### STACK Resources
- Integration Review: `docs/alpaca-integration-review.md`
- Story Documentation: `docs/stories/2-3-alpaca-account-funding.md`
- Code: `internal/adapters/alpaca/`

### Getting Help
- Alpaca Support: Via Intercom on Broker Dashboard
- Alpaca Slack: https://alpaca.markets/slack
- Alpaca Forum: https://forum.alpaca.markets/

## Troubleshooting

### Enable Debug Logging

Update `configs/config.yaml`:
```yaml
log_level: debug
```

This will log all Alpaca API requests and responses.

### Test Circuit Breaker

```bash
# Simulate failures to trigger circuit breaker
# Circuit opens after 5 consecutive failures
# Resets after 30 seconds
```

### Verify Credentials

```bash
# Test authentication
curl -u "API_KEY:API_SECRET" \
  https://broker-api.sandbox.alpaca.markets/v1/assets?limit=1
```

### Check Firm Account Balance

```bash
# Get your firm account details
curl -u "API_KEY:API_SECRET" \
  https://broker-api.sandbox.alpaca.markets/v1/accounts
```

---

**Ready to go!** 🚀

Your Alpaca integration is fully configured and ready for testing. Start with the sandbox environment and move to production when ready.
