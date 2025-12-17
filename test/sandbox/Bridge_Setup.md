# Bridge API Sandbox Setup and Testing

This document provides instructions for setting up and testing Bridge API integration in the RAIL backend service.

## Overview

Bridge API provides comprehensive financial infrastructure including:
- **Customer Management**: KYC verification and user profiles
- **Virtual Accounts**: USD bank accounts for fiat deposits/withdrawals
- **Wallets**: Multi-chain cryptocurrency wallets (ETH, MATIC, AVAX, SOL)
- **Cards**: Virtual and physical card management
- **Transfers**: Cross-chain and internal transfers

## Prerequisites

1. **Bridge Sandbox Account**
   - Sign up at [Bridge Developer Portal](https://bridge.xyz)
   - Create a sandbox account
   - Obtain API credentials

2. **Environment Variables**
   ```bash
   export BRIDGE_API_KEY="your-bridge-sandbox-api-key"
   export BRIDGE_BASE_URL="https://api.bridge.xyz"  # Sandbox URL
   export BRIDGE_ENVIRONMENT="sandbox"
   ```

## Configuration

Add the following to your `.env` file:

```bash
# Bridge API Configuration
BRIDGE_API_KEY=your-bridge-sandbox-api-key
BRIDGE_BASE_URL=https://api.bridge.xyz
BRIDGE_ENVIRONMENT=sandbox
BRIDGE_TIMEOUT=30
BRIDGE_MAX_RETRIES=3
BRIDGE_SUPPORTED_CHAINS=ETH,MATIC,AVAX,SOL
```

## Supported Blockchains

Bridge supports the following payment rails:

| Chain | Bridge Payment Rail | Status | Notes |
|-------|-------------------|---------|---------|
| Ethereum | `ethereum` | ✅ Supported | Mainnet and testnet |
| Polygon | `polygon` | ✅ Supported | Mainnet and Mumbai testnet |
| Avalanche | `avalanche_c_chain` | ✅ Supported | C-Chain mainnet |
| Solana | `solana` | ✅ Supported | Mainnet and devnet |
| Arbitrum | `arbitrum` | ✅ Supported | Mainnet |
| Base | `base` | ✅ Supported | Mainnet |
| Optimism | `optimism` | ✅ Supported | Mainnet |
| Stellar | `stellar` | ✅ Supported | Mainnet |
| Tron | `tron` | ✅ Supported | Mainnet |

## Testing

### 1. Connectivity Test

Run the Bridge connectivity test:

```bash
cd /Users/tobi/Development/RAIL_BACKEND
go run test_bridge_connectivity.go
```

Expected output:
```
Testing Bridge API health check...
✓ Bridge API health check passed

Testing customer creation...
✓ Customer created successfully:
  Customer ID: cust_123456789
  Email: test@example.com
  Status: active
  Wallet ID: wal_123456789
  Wallet Address: 0x1234567890123456789012345678901234567890
  Chain: ethereum

🎉 Bridge API integration test completed successfully!
```

### 2. Unit Tests

Run unit tests (no API key required):
```bash
go test ./test/unit/bridge_adapter_test.go -v
```

### 3. Integration Tests

Run integration tests (requires valid sandbox credentials):
```bash
# Set environment variables
export BRIDGE_API_KEY="your-sandbox-api-key"

# Run integration tests
go test -tags=integration ./test/integration/... -v

# Or run specific test
go test -tags=integration ./test/integration/bridge_integration_test.go -v -run TestBridgeIntegration_FullFlow
```

### 4. Webhook Testing

Bridge uses webhooks for real-time events. To test locally:

1. **Setup ngrok**:
   ```bash
   ngrok http 8080
   ```

2. **Configure webhook in Bridge dashboard**:
   - Set webhook URL to ngrok URL
   - Configure events: `customer.updated`, `wallet.created`, `transfer.completed`

3. **Test webhook handler**:
   ```bash
   # Send test webhook payload
   curl -X POST http://localhost:8080/api/v1/webhooks/bridge \
     -H "Content-Type: application/json" \
     -d '{
       "event": "customer.updated",
       "data": {
         "id": "cust_123456789",
         "status": "active"
       }
     }'
   ```

## Common Testing Scenarios

### Customer Creation Flow

```bash
# Test customer creation with wallet
curl -X POST https://api.bridge.xyz/customers \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "individual",
    "first_name": "Test",
    "last_name": "User",
    "email": "test@example.com",
    "phone": "+1234567890"
  }'
```

### KYC Verification

```bash
# Get KYC link
curl -X GET https://api.bridge.xyz/customers/{customer_id}/kyc \
  -H "Authorization: Bearer YOUR_API_KEY"

# Expected response:
{
  "url": "https://kyc.bridge.xyz/verify?token=abc123",
  "expires_at": "2024-01-01T00:00:00Z"
}
```

### Wallet Management

```bash
# Create wallet
curl -X POST https://api.bridge.xyz/customers/{customer_id}/wallets \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "chain": "ethereum",
    "currency": "usdc",
    "wallet_type": "user"
  }'

# Get wallet balance
curl -X GET https://api.bridge.xyz/customers/{customer_id}/wallets/{wallet_id}/balance \
  -H "Authorization: Bearer YOUR_API_KEY"
```

### Virtual Account Operations

```bash
# Create virtual account
curl -X POST https://api.bridge.xyz/customers/{customer_id}/virtual-accounts \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "checking",
    "currency": "usd"
  }'
```

## Error Handling

Bridge API returns standard HTTP status codes:

- `200 OK`: Success
- `400 Bad Request`: Invalid request data
- `401 Unauthorized`: Invalid API key
- `404 Not Found`: Resource not found
- `429 Too Many Requests`: Rate limit exceeded
- `500 Internal Server Error`: Bridge API error

Example error response:
```json
{
  "error": {
    "code": "INVALID_PARAMETER",
    "message": "Email is invalid",
    "details": {
      "field": "email",
      "value": "invalid-email"
    }
  }
}
```

## Rate Limits

Bridge sandbox has generous rate limits:
- **100 requests per minute** per API key
- **1000 requests per hour** per API key
- **10000 requests per day** per API key

## Security Best Practices

1. **API Key Management**:
   - Never commit API keys to version control
   - Use environment variables for configuration
   - Rotate API keys regularly

2. **Webhook Security**:
   - Verify webhook signatures using Bridge's webhook secret
   - Use HTTPS endpoints for production
   - Implement idempotency for webhook processing

3. **Data Protection**:
   - Encrypt sensitive data at rest
   - Use HTTPS for all API communications
   - Follow PCI DSS compliance for card operations

## Troubleshooting

### Common Issues

1. **Authentication Errors**:
   ```
   Error: 401 Unauthorized
   Solution: Verify API key is correct and active
   ```

2. **Customer Creation Fails**:
   ```
   Error: 400 Bad Request - Email already exists
   Solution: Use unique email addresses for testing
   ```

3. **Wallet Balance Returns Zero**:
   ```
   Expected: Non-zero balance
   Actual: 0
   Solution: Fund the wallet from another account or test with real funds
   ```

4. **KYC Link Expires**:
   ```
   Error: KYC link expired
   Solution: Generate new KYC link for each test
   ```

### Debug Mode

Enable debug logging:
```bash
export LOG_LEVEL=debug
go run test_bridge_connectivity.go
```

## Production Deployment

For production deployment:

1. **Update Configuration**:
   ```bash
   BRIDGE_ENVIRONMENT=production
   BRIDGE_BASE_URL=https://api.bridge.xyz
   ```

2. **Update Webhook URLs**:
   - Use production HTTPS URLs
   - Configure SSL certificates
   - Test webhook delivery

3. **Monitor and Alert**:
   - Set up monitoring for API response times
   - Configure alerts for webhook failures
   - Track success/failure rates

## Additional Resources

- [Bridge API Documentation](https://docs.bridge.xyz)
- [Bridge Developer Dashboard](https://dashboard.bridge.xyz)
- [Bridge Support](https://support.bridge.xyz)
- [Bridge Status Page](https://status.bridge.xyz)

## Migration from Due/Circle

If migrating from Due or Circle:

1. **Customer Data**: Map existing users to Bridge customers
2. **Wallet Migration**: Create Bridge wallets for existing addresses
3. **Virtual Account Setup**: Replace Due accounts with Bridge virtual accounts
4. **Webhook Updates**: Update webhook handlers for Bridge events
5. **Testing**: Thoroughly test with real user data in sandbox

## Support

For Bridge-related issues:

1. Check [Bridge Status Page](https://status.bridge.xyz)
2. Review [API Documentation](https://docs.bridge.xyz)
3. Contact [Bridge Support](https://support.bridge.xyz)
4. Check [RAIL Backend Issues](https://github.com/rail-service/rail_backend/issues)

---

**Note**: This setup guide is for the RAIL backend service Bridge integration. For general Bridge API usage, refer to the official Bridge documentation.