# STACK API Documentation

## Overview

The STACK API provides a comprehensive set of endpoints for managing a GenZ Web3 Multi-Chain Investment Platform. This API bridges traditional finance and Web3 through a hybrid model that enables instant wealth-building.

## Base URL

```
Development: http://localhost:8080/api/v1
Production: https://api.stackservice.com/api/v1
```

## Authentication

Most endpoints require authentication using JWT (JSON Web Tokens). Include the token in the Authorization header:

```
Authorization: Bearer <your-jwt-token>
```

### Obtaining Tokens

1. **Register**: `POST /auth/register` - Create a new account
2. **Verify**: `POST /auth/verify-code` - Verify email/phone with 6-digit code
3. **Login**: `POST /auth/login` - Authenticate and receive tokens

## Interactive Documentation

### Swagger UI

Access the interactive API documentation at:

```
http://localhost:8080/swagger/index.html
```

The Swagger UI provides:
- Complete API endpoint listing
- Request/response schemas
- Try-it-out functionality
- Authentication testing
- Example requests and responses

### Generating Documentation

To regenerate the Swagger documentation after code changes:

```bash
make swagger
```

Or manually:

```bash
swag init -g cmd/main.go -o docs/swagger --parseDependency --parseInternal
```

## API Endpoints Overview

### Authentication (`/auth`)

| Method | Endpoint | Description | Auth Required |
|--------|----------|-------------|---------------|
| POST | `/auth/register` | Register new user | No |
| POST | `/auth/verify-code` | Verify email/phone | No |
| POST | `/auth/resend-code` | Resend verification code | No |
| POST | `/auth/login` | User login | No |
| POST | `/auth/refresh` | Refresh access token | No |
| POST | `/auth/logout` | User logout | Yes |
| POST | `/auth/forgot-password` | Request password reset | No |
| POST | `/auth/reset-password` | Reset password | No |

### Onboarding (`/onboarding`)

| Method | Endpoint | Description | Auth Required |
|--------|----------|-------------|---------------|
| POST | `/onboarding/start` | Start onboarding process | No |
| GET | `/onboarding/status` | Get onboarding status | Yes |
| POST | `/onboarding/kyc/submit` | Submit KYC documents | Yes |
| GET | `/kyc/status` | Get KYC status | Yes |

### Wallets (`/wallet`, `/wallets`)

| Method | Endpoint | Description | Auth Required |
|--------|----------|-------------|---------------|
| GET | `/wallet/addresses` | Get deposit addresses | Yes |
| GET | `/wallet/status` | Get wallet status | Yes |
| POST | `/wallets/initiate` | Initiate wallet creation | Yes |
| POST | `/wallets/provision` | Provision wallets | Yes |
| GET | `/wallets/:chain/address` | Get wallet by chain | Yes |

### Funding (`/funding`)

| Method | Endpoint | Description | Auth Required |
|--------|----------|-------------|---------------|
| POST | `/funding/deposit/address` | Generate deposit address | Yes |
| GET | `/funding/confirmations` | Get deposit confirmations | Yes |
| POST | `/funding/virtual-account` | Create virtual account | Yes |
| GET | `/balances` | Get user balances | Yes |

### Investment Baskets (`/baskets`, `/curated`)

| Method | Endpoint | Description | Auth Required |
|--------|----------|-------------|---------------|
| GET | `/baskets` | List user baskets | Yes |
| POST | `/baskets` | Create custom basket | Yes |
| GET | `/baskets/:id` | Get basket details | Yes |
| POST | `/baskets/:id/invest` | Invest in basket | Yes |
| GET | `/curated/baskets` | List curated baskets | Yes |
| POST | `/curated/baskets/:id/invest` | Invest in curated basket | Yes |

### AI CFO (`/ai`)

| Method | Endpoint | Description | Auth Required |
|--------|----------|-------------|---------------|
| GET | `/ai/summary/latest` | Get latest AI summary | Yes |
| POST | `/ai/analyze` | Perform on-demand analysis | Yes |

### Due Network (`/due`)

| Method | Endpoint | Description | Auth Required |
|--------|----------|-------------|---------------|
| POST | `/due/account` | Create Due account | Yes |
| GET | `/due/account` | Get Due account | Yes |
| GET | `/due/kyc-link` | Get KYC link | Yes |
| POST | `/due/link-wallet` | Link wallet | Yes |
| POST | `/due/virtual-account` | Create virtual account | Yes |
| POST | `/due/transfer` | Create transfer | Yes |
| GET | `/due/transfers` | List transfers | Yes |

### Alpaca Assets (`/assets`)

| Method | Endpoint | Description | Auth Required |
|--------|----------|-------------|---------------|
| GET | `/assets` | List all tradable assets | Yes |
| GET | `/assets/search` | Search assets | Yes |
| GET | `/assets/popular` | Get popular assets | Yes |
| GET | `/assets/:symbol_or_id` | Get asset details | Yes |

### Portfolio (`/portfolio`)

| Method | Endpoint | Description | Auth Required |
|--------|----------|-------------|---------------|
| GET | `/portfolio/overview` | Get portfolio overview | Yes |

### Admin (`/admin`)

| Method | Endpoint | Description | Auth Required |
|--------|----------|-------------|---------------|
| POST | `/admin/users` | Create admin user | Special |
| GET | `/admin/users` | List all users | Admin |
| GET | `/admin/users/:id` | Get user by ID | Admin |
| PUT | `/admin/users/:id/status` | Update user status | Admin |
| POST | `/admin/wallet/create` | Create wallets for user | Admin |
| GET | `/admin/wallets` | List all wallets | Admin |

### Health & Monitoring

| Method | Endpoint | Description | Auth Required |
|--------|----------|-------------|---------------|
| GET | `/health` | Application health | No |
| GET | `/ready` | Readiness check | No |
| GET | `/live` | Liveness check | No |
| GET | `/metrics` | Prometheus metrics | No |

## Request/Response Examples

### Register User

**Request:**
```bash
curl -X POST http://localhost:8080/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{
    "email": "user@example.com",
    "password": "SecurePass123!"
  }'
```

**Response:**
```json
{
  "message": "Verification code sent to user@example.com. Please verify your account.",
  "identifier": "user@example.com"
}
```

### Verify Code

**Request:**
```bash
curl -X POST http://localhost:8080/api/v1/auth/verify-code \
  -H "Content-Type: application/json" \
  -d '{
    "email": "user@example.com",
    "code": "123456"
  }'
```

**Response:**
```json
{
  "user": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "email": "user@example.com",
    "emailVerified": true,
    "onboardingStatus": "wallets_pending",
    "kycStatus": "pending",
    "createdAt": "2024-01-01T00:00:00Z"
  },
  "accessToken": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "refreshToken": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "expiresAt": "2024-01-08T00:00:00Z"
}
```

### Get Wallet Addresses

**Request:**
```bash
curl -X GET http://localhost:8080/api/v1/wallet/addresses \
  -H "Authorization: Bearer <your-token>"
```

**Response:**
```json
{
  "wallets": [
    {
      "chain": "SOL-DEVNET",
      "address": "9xQeWvG816bUx9EPjHmaT23yvVM2ZWbrrpZb9PusVFin",
      "status": "live"
    }
  ]
}
```

### Get Balances

**Request:**
```bash
curl -X GET http://localhost:8080/api/v1/balances \
  -H "Authorization: Bearer <your-token>"
```

**Response:**
```json
{
  "balances": [
    {
      "chain": "SOL-DEVNET",
      "stablecoin": "USDC",
      "amount": "1000.50",
      "usd_value": "1000.50"
    }
  ],
  "total_usd": "1000.50",
  "buying_power": "1000.50"
}
```

## Error Responses

All error responses follow a consistent format:

```json
{
  "code": "ERROR_CODE",
  "message": "Human-readable error message",
  "details": {
    "field": "Additional context"
  }
}
```

### Common Error Codes

| Code | HTTP Status | Description |
|------|-------------|-------------|
| `INVALID_REQUEST` | 400 | Invalid request payload |
| `VALIDATION_ERROR` | 400 | Validation failed |
| `UNAUTHORIZED` | 401 | Authentication required |
| `INVALID_TOKEN` | 401 | Invalid or expired token |
| `FORBIDDEN` | 403 | Insufficient permissions |
| `NOT_FOUND` | 404 | Resource not found |
| `USER_EXISTS` | 409 | User already exists |
| `INTERNAL_ERROR` | 500 | Internal server error |

## Rate Limiting

API requests are rate-limited to prevent abuse:

- **Default**: 100 requests per minute per IP
- **Authenticated**: 1000 requests per minute per user

Rate limit headers are included in responses:

```
X-RateLimit-Limit: 100
X-RateLimit-Remaining: 95
X-RateLimit-Reset: 1640995200
```

## Webhooks

The API supports webhooks for external integrations:

### Chain Deposit Webhook

**Endpoint:** `POST /webhooks/chain-deposit`

Receives blockchain deposit notifications from Circle.

### Due Webhook

**Endpoint:** `POST /webhooks/due`

Receives events from Due Network for virtual account operations.

## Data Models

### User

```json
{
  "id": "uuid",
  "email": "string",
  "phone": "string (optional)",
  "emailVerified": "boolean",
  "phoneVerified": "boolean",
  "onboardingStatus": "enum",
  "kycStatus": "enum",
  "createdAt": "timestamp"
}
```

### Wallet

```json
{
  "id": "uuid",
  "userId": "uuid",
  "chain": "enum",
  "address": "string",
  "status": "enum",
  "createdAt": "timestamp"
}
```

### Balance

```json
{
  "chain": "enum",
  "stablecoin": "string",
  "amount": "decimal",
  "usd_value": "decimal"
}
```

## Enumerations

### OnboardingStatus

- `started` - User registered
- `email_verified` - Email verified
- `wallets_pending` - Waiting for wallet creation
- `wallets_created` - Wallets created
- `kyc_pending` - KYC submitted
- `kyc_approved` - KYC approved
- `completed` - Onboarding complete

### KYCStatus

- `pending` - KYC not started
- `submitted` - KYC documents submitted
- `under_review` - Under review
- `approved` - KYC approved
- `rejected` - KYC rejected

### WalletChain

- `SOL-DEVNET` - Solana Devnet
- `ETH-SEPOLIA` - Ethereum Sepolia (future)
- `MATIC-AMOY` - Polygon Amoy (future)

### WalletStatus

- `creating` - Wallet being created
- `live` - Wallet active
- `failed` - Wallet creation failed

## Best Practices

### Security

1. **Never expose tokens**: Store JWT tokens securely
2. **Use HTTPS**: Always use HTTPS in production
3. **Rotate tokens**: Refresh tokens before expiry
4. **Validate input**: Always validate user input

### Performance

1. **Pagination**: Use pagination for list endpoints
2. **Caching**: Cache responses when appropriate
3. **Batch requests**: Combine multiple operations
4. **Compression**: Enable gzip compression

### Error Handling

1. **Check status codes**: Always check HTTP status codes
2. **Parse error responses**: Extract error details
3. **Implement retries**: Retry failed requests with exponential backoff
4. **Log errors**: Log all errors for debugging

## Support

For API support:

- **Documentation**: https://docs.stackservice.com
- **Email**: support@stackservice.com
- **GitHub Issues**: https://github.com/stack-service/stack_service/issues

## Changelog

### Version 1.0.0 (Current)

- Initial API release
- Authentication and user management
- Multi-chain wallet support
- Funding and deposits
- Investment baskets
- AI CFO integration
- Due Network integration
- Alpaca brokerage integration

## License

MIT License - See LICENSE file for details
