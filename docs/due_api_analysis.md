# Due API Integration Analysis

## Executive Summary

After scanning the Due API documentation (https://due.readme.io) and analyzing the current codebase implementation, this document identifies gaps, missing features, and improvements needed before comprehensive endpoint testing.

## Due API Overview

Due provides modern payment infrastructure with the following core APIs:

1. **Accounts API** - Account management and KYC/KYB workflows
2. **Transfers API** - Cross-border payments and asset transfers
3. **Virtual Accounts API** - Programmable collection infrastructure
4. **Wallets API** - Wallet balance and transaction management
5. **Recipients API** - Recipient management for transfers
6. **FX API** - Real-time exchange rates and quotes
7. **Vaults API** - MPC wallet creation and signing
8. **Webhooks API** - Event notifications
9. **Sandbox API** - Testing pay-ins

## Current Implementation Status

### ✅ Implemented Features

1. **Account Management**
   - `CreateAccount()` - Create Due accounts
   - `GetAccount()` - Retrieve account details

2. **Wallet Management**
   - `LinkWallet()` - Link blockchain wallets
   - `ListWallets()` - List all wallets
   - `GetWallet()` - Get wallet by ID

3. **Recipients**
   - `CreateRecipient()` - Create recipients
   - `ListRecipients()` - List with pagination
   - `GetRecipient()` - Get by ID

4. **Virtual Accounts**
   - `CreateVirtualAccount()` - Create virtual accounts
   - `GetVirtualAccount()` - Get by reference
   - `ListVirtualAccounts()` - List with filters

5. **Transfers**
   - `CreateTransfer()` - Create transfers
   - `GetTransfer()` - Get transfer details
   - `ListTransfers()` - List with filters
   - `CreateQuote()` - Create transfer quotes

6. **Webhooks**
   - `CreateWebhookEndpoint()` - Create webhook endpoints
   - `ListWebhookEndpoints()` - List endpoints
   - `DeleteWebhookEndpoint()` - Delete endpoints

7. **KYC**
   - `GetKYCStatus()` - Get KYC status
   - `InitiateKYC()` - Initiate KYC process

8. **Terms of Service**
   - `AcceptTermsOfService()` - Accept ToS

9. **Channels**
   - `GetChannels()` - Get available payment channels

### ❌ Missing Critical Features

#### 1. **Account Wallets API** (Account-specific wallets)
```go
// Missing endpoints:
GET /v1/wallets?accountId={accountId}  // List wallets for specific account
```

#### 2. **Financial Institutions API**
```go
// Missing endpoints:
GET /v1/financial_institutions/{country2}/{schema}  // Get financial institutions
```

#### 3. **KYC Advanced Features**
```go
// Missing endpoints:
POST /v1/kyc/share/sumsub           // Share KYC data from Sumsub
POST /v1/kyc/session                // Create KYC/KYB access session
```

#### 4. **Blockchain Transfers API**
```go
// Missing endpoints:
GET /v1/token_transfers/{address}   // Get blockchain transfers by address
```

#### 5. **Transfer Intent API**
```go
// Missing endpoints:
POST /v1/transfers/{id}/transfer_intent        // Create transfer intent
POST /v1/transfer_intents/submit               // Submit transfer intent
```

#### 6. **Wallet Balance API**
```go
// Missing endpoints:
GET /v1/wallets/{walletId}/balance  // Get wallet balances
```

#### 7. **Usage API**
```go
// Missing endpoints:
GET /v1/usage                       // Get API usage statistics
```

#### 8. **TOS Advanced Features**
```go
// Missing endpoints:
GET /v1/tos/{token}                 // Get ToS details
```

#### 9. **Webhook Events API**
```go
// Missing endpoints:
GET /v1/webhook_events              // List webhook events
POST /v1/webhook_endpoints/{id}     // Update webhook endpoint
```

#### 10. **Vaults API** (Complete MPC wallet management)
```go
// Missing all Vault endpoints:
POST /v1/vaults/credentials/init    // Initialize credentials
POST /v1/vaults/credentials         // Create credentials
POST /v1/vaults/sign                // Sign transactions
POST /v1/vaults                     // Create vaults
GET /v1/vaults                      // List vaults
```

#### 11. **FX Markets API**
```go
// Missing endpoints:
GET /fx/markets                     // Get available FX markets
POST /fx/quote                      // Create FX quote
```

#### 12. **Sandbox API**
```go
// Missing endpoints:
POST /dev/payin                     // Simulate pay-in for testing
```

#### 13. **Recipient Management**
```go
// Missing endpoints:
DELETE /v1/recipients/{id}          // Delete recipient
```

### ⚠️ Implementation Issues

#### 1. **Inconsistent Endpoint Versioning**
```go
// Current implementation mixes versioned and unversioned endpoints
// Issue in doRequest():
if !strings.HasPrefix(endpoint, "/v1/") && !strings.HasPrefix(endpoint, "/dev/") {
    endpoint = "/v1" + endpoint
}

// Problem: Some methods pass "/v1/..." while others pass "/..."
// Solution: Standardize all endpoints to exclude "/v1" prefix
```

#### 2. **Missing Query Parameter Support**
```go
// ListVirtualAccounts is missing required query parameters:
// - destination (required)
// - schemaIn (required)
// - currencyIn (required)
// - railOut (required)
// - currencyOut (required)
// - reference (optional)

// Current implementation only supports optional filters
```

#### 3. **Missing Header: Due-Account-Id**
```go
// Many endpoints require Due-Account-Id header but implementation is inconsistent
// Only doRequestWithAccountID() sets it properly
// Regular doRequest() always uses c.config.AccountID

// Issue: Cannot make requests for different accounts
```

#### 4. **Incomplete Error Handling**
```go
// Current error handling:
if resp.StatusCode >= 400 {
    return fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(respBody))
}

// Missing:
// - Parse ErrorResponse struct
// - Handle specific error codes
// - Retry logic for specific errors
```

#### 5. **Missing Pagination Support**
```go
// ListTransfers, ListRecipients, etc. don't support:
// - next cursor for pagination
// - startDate/endDate filtering
// - includeEmpty parameter
```

#### 6. **Webhook Signature Verification**
```go
// Missing webhook signature verification
// Due sends webhook signatures that need to be verified
// No implementation of signature validation
```

#### 7. **Transfer Quote vs Transfer Creation**
```go
// Confusion between:
// - POST /v1/transfers/quote (create quote)
// - POST /v1/transfers (create transfer)

// Current CreateQuote uses wrong types (CreateQuoteRequest vs OnRampQuoteRequest)
```

#### 8. **Virtual Account Creation Missing Required Fields**
```go
// ListVirtualAccounts requires ALL these query params:
// - destination (required)
// - schemaIn (required)
// - currencyIn (required)
// - railOut (required)
// - currencyOut (required)

// Current implementation doesn't enforce this
```

## Recommended Improvements

### Priority 1: Critical Fixes

1. **Standardize Endpoint Paths**
```go
// Remove /v1 prefix from all method calls
// Let doRequest() handle versioning consistently
```

2. **Add Required Query Parameters**
```go
type ListVirtualAccountsRequest struct {
    Destination  string `json:"destination"`  // Required
    SchemaIn     string `json:"schemaIn"`     // Required
    CurrencyIn   string `json:"currencyIn"`   // Required
    RailOut      string `json:"railOut"`      // Required
    CurrencyOut  string `json:"currencyOut"`  // Required
    Reference    string `json:"reference,omitempty"`
}
```

3. **Implement Proper Error Handling**
```go
func parseErrorResponse(body []byte, statusCode int) error {
    var errResp ErrorResponse
    if err := json.Unmarshal(body, &errResp); err != nil {
        return fmt.Errorf("API error: status %d, body: %s", statusCode, string(body))
    }
    return &errResp
}
```

4. **Add Webhook Signature Verification**
```go
func VerifyWebhookSignature(payload []byte, signature string, secret string) bool {
    // Implement HMAC-SHA256 verification
}
```

### Priority 2: Missing Endpoints

1. **Wallet Balance API**
```go
func (c *Client) GetWalletBalance(ctx context.Context, walletID string) (*WalletBalanceResponse, error)
```

2. **Transfer Intent API**
```go
func (c *Client) CreateTransferIntent(ctx context.Context, transferID string, req *TransferIntentRequest) (*TransferIntentResponse, error)
func (c *Client) SubmitTransferIntent(ctx context.Context, req *SubmitTransferIntentRequest) (*TransferIntentResponse, error)
```

3. **Blockchain Transfers API**
```go
func (c *Client) GetBlockchainTransfers(ctx context.Context, address string) (*BlockchainTransfersResponse, error)
```

4. **Financial Institutions API**
```go
func (c *Client) GetFinancialInstitutions(ctx context.Context, country, schema string) (*FinancialInstitutionsResponse, error)
```

5. **Vaults API** (Complete implementation)
```go
func (c *Client) InitializeCredentials(ctx context.Context, req *InitCredentialsRequest) (*InitCredentialsResponse, error)
func (c *Client) CreateCredentials(ctx context.Context, req *CreateCredentialsRequest) (*CredentialsResponse, error)
func (c *Client) CreateVault(ctx context.Context, req *CreateVaultRequest) (*VaultResponse, error)
func (c *Client) SignTransaction(ctx context.Context, req *SignRequest) (*SignResponse, error)
```

6. **FX Markets API**
```go
func (c *Client) GetFXMarkets(ctx context.Context) (*FXMarketsResponse, error)
func (c *Client) CreateFXQuote(ctx context.Context, req *FXQuoteRequest) (*FXQuoteResponse, error)
```

7. **Sandbox API**
```go
func (c *Client) SimulatePayIn(ctx context.Context, req *SimulatePayInRequest) (*SimulatePayInResponse, error)
```

### Priority 3: Enhanced Features

1. **Pagination Helper**
```go
type PaginationParams struct {
    Next    string
    Limit   int
    Order   string // "asc" or "desc"
}

func (c *Client) ListWithPagination(ctx context.Context, endpoint string, params PaginationParams) (*PaginatedResponse, error)
```

2. **Retry Configuration**
```go
type RetryConfig struct {
    MaxAttempts      int
    InitialDelay     time.Duration
    MaxDelay         time.Duration
    RetryableStatuses []int // e.g., 429, 500, 502, 503, 504
}
```

3. **Request Context Enhancement**
```go
type RequestOptions struct {
    AccountID    string
    IdempotencyKey string
    Timeout      time.Duration
}
```

4. **Webhook Event Types**
```go
const (
    WebhookEventTransferCreated   = "transfer.created"
    WebhookEventTransferCompleted = "transfer.completed"
    WebhookEventTransferFailed    = "transfer.failed"
    WebhookEventAccountUpdated    = "account.updated"
    WebhookEventKYCCompleted      = "kyc.completed"
)
```

## Testing Strategy

### Phase 1: Unit Tests
- Test each client method with mocked HTTP responses
- Test error handling for various status codes
- Test request/response serialization

### Phase 2: Integration Tests
- Test against Due sandbox environment
- Test complete flows (account creation → wallet linking → transfer)
- Test webhook delivery and signature verification

### Phase 3: End-to-End Tests
- Test real-world scenarios
- Test error recovery
- Test concurrent requests
- Test rate limiting

## Test Coverage Checklist

### Accounts API
- [ ] Create individual account
- [ ] Create business account
- [ ] Get account by ID
- [ ] List accounts with pagination
- [ ] Get account categories

### Wallets API
- [ ] Link EVM wallet
- [ ] Link Solana wallet
- [ ] List wallets
- [ ] Get wallet by ID
- [ ] Get wallet balance

### Recipients API
- [ ] Create recipient (EVM)
- [ ] Create recipient (Solana)
- [ ] List recipients with pagination
- [ ] Get recipient by ID
- [ ] Delete recipient

### Virtual Accounts API
- [ ] Create virtual account (USD ACH)
- [ ] Create virtual account (EUR SEPA)
- [ ] Create virtual account (crypto)
- [ ] List virtual accounts with filters
- [ ] Get virtual account by reference

### Transfers API
- [ ] Create transfer quote
- [ ] Create transfer
- [ ] Get transfer details
- [ ] List transfers with filters
- [ ] Create funding address
- [ ] Create transfer intent
- [ ] Submit transfer intent

### Webhooks API
- [ ] Create webhook endpoint
- [ ] List webhook endpoints
- [ ] Update webhook endpoint
- [ ] Delete webhook endpoint
- [ ] List webhook events
- [ ] Verify webhook signature

### KYC API
- [ ] Get KYC status
- [ ] Initiate KYC
- [ ] Create KYC session
- [ ] Share KYC data

### Channels API
- [ ] Get available channels
- [ ] Filter channels by country
- [ ] Filter channels by currency

### FX API
- [ ] Get FX markets
- [ ] Create FX quote
- [ ] Get historical rates

### Vaults API
- [ ] Initialize credentials
- [ ] Create credentials
- [ ] Create vault
- [ ] Sign transaction
- [ ] List vaults

### Sandbox API
- [ ] Simulate pay-in
- [ ] Test webhook delivery

## Security Considerations

1. **API Key Management**
   - Store API keys in environment variables
   - Rotate keys regularly
   - Use different keys for sandbox/production

2. **Webhook Security**
   - Verify all webhook signatures
   - Use HTTPS endpoints only
   - Implement replay attack prevention

3. **Data Encryption**
   - Encrypt sensitive data at rest
   - Use TLS for all API calls
   - Never log API keys or secrets

4. **Rate Limiting**
   - Implement exponential backoff
   - Handle 429 responses gracefully
   - Monitor rate limit headers

## Next Steps

1. **Immediate Actions**
   - Fix endpoint versioning inconsistency
   - Add missing required query parameters
   - Implement proper error response parsing
   - Add webhook signature verification

2. **Short Term (1-2 weeks)**
   - Implement missing critical endpoints (Wallet Balance, Transfer Intent)
   - Add comprehensive unit tests
   - Set up sandbox testing environment
   - Document all endpoints with examples

3. **Medium Term (2-4 weeks)**
   - Implement Vaults API for MPC wallet management
   - Add FX Markets API
   - Implement Sandbox API for testing
   - Create integration test suite

4. **Long Term (1-2 months)**
   - Performance optimization
   - Advanced error recovery
   - Monitoring and alerting
   - Production deployment

## Conclusion

The current Due API integration has a solid foundation but requires significant enhancements before comprehensive testing. The most critical gaps are:

1. Missing required query parameters for virtual accounts
2. Inconsistent endpoint versioning
3. Missing webhook signature verification
4. Incomplete Vaults API (MPC wallets)
5. Missing wallet balance endpoint
6. No transfer intent support

Addressing these issues in priority order will ensure a robust, production-ready integration with the Due API.
