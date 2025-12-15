# Due API Sandbox Testing

## Setup

### Environment Variables

```bash
# .env file
DUE_API_KEY=your_sandbox_api_key
DUE_ACCOUNT_ID=your_sandbox_account_id
DUE_BASE_URL=https://api.sandbox.due.network
WEBHOOK_SECRET=your_webhook_secret
```

### Run Integration Tests

```bash
# All tests
go test -tags=integration ./test/integration/...

# Specific test
go test -tags=integration ./test/integration/ -run TestIntegration_AccountFlow

# Verbose
go test -tags=integration -v ./test/integration/...
```

## Manual Testing

### Account Creation
```bash
curl -X POST https://api.sandbox.due.network/v1/accounts \
  -H "Authorization: Bearer $DUE_API_KEY" \
  -H "Due-Account-Id: $DUE_ACCOUNT_ID" \
  -H "Content-Type: application/json" \
  -d '{"type":"individual","name":"Test","email":"test@example.com","country":"US"}'
```

### Wallet Balance
```bash
curl -X GET https://api.sandbox.due.network/v1/wallets/{walletId}/balance \
  -H "Authorization: Bearer $DUE_API_KEY" \
  -H "Due-Account-Id: $DUE_ACCOUNT_ID"
```

### Virtual Account
```bash
curl -X POST https://api.sandbox.due.network/v1/virtual_accounts \
  -H "Authorization: Bearer $DUE_API_KEY" \
  -H "Due-Account-Id: $DUE_ACCOUNT_ID" \
  -H "Content-Type: application/json" \
  -d '{
    "destination":"recipient_123",
    "schemaIn":"bank_us",
    "currencyIn":"USD",
    "railOut":"ethereum",
    "currencyOut":"USDC",
    "reference":"test_001"
  }'
```

### FX Markets
```bash
curl -X GET https://api.sandbox.due.network/fx/markets \
  -H "Authorization: Bearer $DUE_API_KEY"
```

## Webhook Testing

```bash
# Setup ngrok
ngrok http 8080

# Create webhook
curl -X POST https://api.sandbox.due.network/v1/webhook_endpoints \
  -H "Authorization: Bearer $DUE_API_KEY" \
  -H "Due-Account-Id: $DUE_ACCOUNT_ID" \
  -H "Content-Type: application/json" \
  -d '{
    "url":"https://abc123.ngrok.io/webhooks/due",
    "events":["transfer.completed","transfer.failed"]
  }'
```
