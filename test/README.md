# STACK Service Test Suite

This directory contains comprehensive tests for the STACK service funding functionality, covering unit tests, integration tests, and end-to-end scenarios as specified in the user story requirements.

## Test Structure

### Unit Tests (`/internal/domain/services/funding/service_test.go`)

Tests the core business logic of the funding service with mocked dependencies:

- **Deposit Address Creation**: Tests address generation for new users and retrieval for existing users
- **Balance Management**: Tests balance retrieval and zero balance handling for new users
- **Webhook Processing**: Tests deposit validation, duplicate detection, and balance updates
- **Error Handling**: Tests various error scenarios and edge cases
- **Idempotency**: Ensures duplicate webhook processing is handled correctly

#### Key Test Scenarios:
- ✅ Existing wallet address retrieval
- ✅ New wallet address generation
- ✅ Balance retrieval (existing and new users)
- ✅ Successful deposit processing
- ✅ Duplicate deposit detection (idempotency)
- ✅ Invalid deposit validation
- ✅ Funding confirmations retrieval

### Integration Tests (`/test/integration/funding_test.go`)

Tests the complete HTTP request/response flow with mocked services:

#### Funding Endpoints:
- `POST /funding/deposit/address` - Deposit address creation
- `GET /funding/confirmations` - Funding confirmations with pagination
- `GET /balances` - User balance retrieval

#### Webhook Endpoints:
- `POST /webhooks/chain-deposit` - Chain deposit webhook processing

#### Test Coverage:
- ✅ Successful requests with proper authentication
- ✅ Invalid input validation
- ✅ Authentication and authorization
- ✅ Pagination support
- ✅ Webhook security validation
- ✅ Duplicate transaction handling

## Running Tests

### Prerequisites

Install test dependencies:
```bash
go mod download
go install github.com/stretchr/testify
```

### Unit Tests

Run all unit tests:
```bash
go test ./internal/domain/services/funding/... -v
```

Run specific test:
```bash
go test ./internal/domain/services/funding/... -run TestCreateDepositAddress_Success -v
```

Run with coverage:
```bash
go test ./internal/domain/services/funding/... -cover -coverprofile=coverage.out
go tool cover -html=coverage.out
```

### Integration Tests

Run integration tests:
```bash
go test ./test/integration/... -v
```

### All Tests

Run complete test suite:
```bash
go test ./... -v
```

## Test Scenarios Covered

### Story 2.0 Acceptance Criteria ✅

| Area | Criteria | Test Coverage |
|------|----------|---------------|
| **Deposit Address** | User can request deposit address for EVM or SOL | ✅ Unit & Integration tests |
| | Endpoint: POST /funding/deposit/address | ✅ Integration tests |
| | Returns QR + chain/network metadata | ✅ Response format tests |
| | Address generation is idempotent per user/chain | ✅ Unit tests (existing wallet) |
| **On-Chain Detection** | Inbound webhook processes transactions | ✅ Webhook integration tests |
| | Confirms stablecoin transfer via validation | ✅ Unit tests (ValidateDeposit) |
| | Applies chain-specific confirmations | ✅ Webhook processing tests |
| **Validation** | Only whitelisted tokens (USDC) accepted | ✅ Entity validation tests |
| | Wrong token triggers warning entry | ✅ Invalid deposit tests |
| **Credit to Buying Power** | After confirmed, creates deposit record | ✅ Unit tests (ProcessChainDeposit) |
| | Credits balances.buying_power | ✅ Balance update tests |
| | /balances endpoint reflects update | ✅ Integration tests |
| **Funding History** | /funding/confirmations lists events | ✅ Integration tests |
| | Status: Pending/Confirmed/Credited | ✅ Response format tests |
| **Reliability** | Transient errors retry with backoff | ✅ Retry logic (implemented) |
| | Duplicate events ignored (idempotency) | ✅ Duplicate detection tests |

### Security Testing ✅

- **Webhook Signature Verification**: HMAC-SHA256 validation
- **Authentication**: JWT token validation
- **Input Validation**: Payload structure and field validation
- **Rate Limiting**: Implemented in middleware
- **CORS Protection**: Configured in middleware

### Edge Cases ✅

- **Duplicate Webhooks**: Multiple identical transaction hashes
- **Invalid Tokens**: Unsupported stablecoins
- **Missing Fields**: Incomplete webhook payloads
- **Authentication Failures**: Missing or invalid JWT tokens
- **Network Errors**: Connection timeouts and retries
- **Large Payloads**: Webhook payload size limits

## Performance Testing

For load testing, you can use tools like:

```bash
# Install vegeta for load testing
go install github.com/tsenart/vegeta@latest

# Load test deposit address endpoint
echo "POST http://localhost:8080/api/v1/funding/deposit/address" | \
  vegeta attack -rate=10 -duration=30s -header="Authorization: Bearer token" \
  -header="Content-Type: application/json" \
  -body='{"chain":"Solana"}' | \
  vegeta report
```

## Test Environment Variables

Set these environment variables for testing:

```bash
export TEST_DB_URL="postgres://test_user:test_pass@localhost:5432/stack_test"
export TEST_WEBHOOK_SECRET="test-webhook-secret"
export TEST_JWT_SECRET="test-jwt-secret"
export LOG_LEVEL="debug"
```

## Continuous Integration

The test suite is designed to run in CI/CD pipelines. Example GitHub Actions workflow:

```yaml
name: Test Suite
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:13
        env:
          POSTGRES_PASSWORD: test_pass
          POSTGRES_DB: stack_test
    steps:
      - uses: actions/checkout@v2
      - uses: actions/setup-go@v2
        with:
          go-version: 1.21
      - name: Run tests
        run: |
          go test ./... -v -cover
          go test ./test/integration/... -v
```

## Troubleshooting

### Common Issues

1. **Database Connection Errors**: Ensure PostgreSQL is running and accessible
2. **Mock Failures**: Check that all mock expectations are properly set
3. **Authentication Errors**: Verify JWT token format and secret
4. **Webhook Signature Failures**: Ensure HMAC secret matches between client and server

### Debug Mode

Run tests with debug logging:
```bash
LOG_LEVEL=debug go test ./... -v
```

## Contributing

When adding new tests:

1. Follow the existing test patterns and naming conventions
2. Use table-driven tests for multiple similar scenarios
3. Mock external dependencies appropriately
4. Include both positive and negative test cases
5. Test edge cases and error conditions
6. Update this README with new test scenarios

## Test Data

Test fixtures and sample data are located in:
- `testdata/` - Static test files
- Mock implementations in individual test files
- Database seeds for integration tests (when applicable)