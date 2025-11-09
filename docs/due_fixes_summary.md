# Due API Integration Fixes - Summary

## Changes Implemented

### 1. ✅ Fixed Endpoint Versioning

**Problem:** Inconsistent endpoint versioning with mixed `/v1/` prefixes causing confusion.

**Solution:**
- Standardized all endpoint calls to exclude `/v1` prefix
- Updated `doRequest()` to consistently add `/v1` prefix for all non-dev endpoints
- All methods now pass clean endpoint names (e.g., `"accounts"` instead of `"/v1/accounts"`)

**Files Modified:**
- `internal/adapters/due/client.go` - Updated all 20+ endpoint calls
- `internal/adapters/due/onramp.go` - Updated 5 endpoint calls

**Example:**
```go
// Before
c.doRequest(ctx, "POST", "/v1/accounts", req, &response)
c.doRequest(ctx, "POST", "/accounts", req, &response)  // Inconsistent

// After
c.doRequest(ctx, "POST", "accounts", req, &response)  // Consistent
```

### 2. ✅ Added Required Query Parameters

**Problem:** `ListVirtualAccounts()` was missing 5 required query parameters per Due API spec.

**Solution:**
- Updated `VirtualAccountFilters` struct with all required fields:
  - `Destination` (required)
  - `SchemaIn` (required)
  - `CurrencyIn` (required)
  - `RailOut` (required)
  - `CurrencyOut` (required)
  - `Reference` (optional)
- Added validation to enforce required parameters
- Returns clear error if required fields are missing

**Files Modified:**
- `internal/adapters/due/types.go` - Updated struct definition
- `internal/adapters/due/client.go` - Added validation logic

**Example:**
```go
// Before
filters := &VirtualAccountFilters{
    CurrencyIn: "USD",
    RailOut: "ach",
}

// After - enforces all required fields
filters := &VirtualAccountFilters{
    Destination: "recipient_123",
    SchemaIn: "bank_us",
    CurrencyIn: "USD",
    RailOut: "ach",
    CurrencyOut: "USD",
    Reference: "optional_ref",
}
```

### 3. ✅ Implemented Webhook Signature Verification

**Problem:** No webhook signature verification, creating security vulnerability.

**Solution:**
- Created new `pkg/webhook/verify.go` utility
- Implements HMAC-SHA256 signature verification
- Constant-time comparison to prevent timing attacks

**Files Created:**
- `pkg/webhook/verify.go`

**Usage:**
```go
import "github.com/stack-service/stack_service/pkg/webhook"

// In webhook handler
isValid := webhook.VerifySignature(
    requestBody,
    signatureHeader,
    webhookSecret,
)
if !isValid {
    return errors.New("invalid webhook signature")
}
```

### 4. ✅ Parse Error Responses Properly

**Problem:** API errors returned as generic strings, losing structured error information.

**Solution:**
- Updated `doRequest()` to parse Due's `ErrorResponse` struct
- Returns structured error with status code, message, code, and details
- Falls back to generic error if parsing fails
- Implements `error` interface for seamless error handling

**Files Modified:**
- `internal/adapters/due/client.go` - Updated error handling in `doRequest()`

**Example:**
```go
// Before
// Error: API error: status 400, body: {"statusCode":400,"message":"Invalid currency","code":"INVALID_CURRENCY"}

// After - structured error
err := client.CreateTransfer(ctx, req)
if err != nil {
    if dueErr, ok := err.(*ErrorResponse); ok {
        log.Error("Due API error",
            "status", dueErr.StatusCode,
            "message", dueErr.Message,
            "code", dueErr.Code,
            "details", dueErr.Details)
    }
}
```

## Testing Recommendations

### Unit Tests
```go
func TestVerifyWebhookSignature(t *testing.T) {
    payload := []byte(`{"type":"transfer.completed","data":{}}`)
    secret := "test_secret"
    
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(payload)
    validSig := hex.EncodeToString(mac.Sum(nil))
    
    assert.True(t, webhook.VerifySignature(payload, validSig, secret))
    assert.False(t, webhook.VerifySignature(payload, "invalid", secret))
}

func TestListVirtualAccountsValidation(t *testing.T) {
    client := NewClient(config, logger)
    
    // Should fail without required fields
    _, err := client.ListVirtualAccounts(ctx, nil)
    assert.Error(t, err)
    
    // Should fail with partial fields
    _, err = client.ListVirtualAccounts(ctx, &VirtualAccountFilters{
        CurrencyIn: "USD",
    })
    assert.Error(t, err)
    
    // Should succeed with all required fields
    _, err = client.ListVirtualAccounts(ctx, &VirtualAccountFilters{
        Destination: "recipient_123",
        SchemaIn: "bank_us",
        CurrencyIn: "USD",
        RailOut: "ach",
        CurrencyOut: "USD",
    })
    assert.NoError(t, err)
}
```

### Integration Tests
```bash
# Test endpoint versioning
curl -X POST https://api.due.network/v1/accounts \
  -H "Authorization: Bearer $API_KEY" \
  -H "Due-Account-Id: $ACCOUNT_ID" \
  -d '{"type":"individual","name":"Test","email":"test@example.com","country":"US"}'

# Test virtual accounts with required params
curl -X GET "https://api.due.network/v1/virtual_accounts?destination=recipient_123&schemaIn=bank_us&currencyIn=USD&railOut=ach&currencyOut=USD" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Due-Account-Id: $ACCOUNT_ID"
```

## Impact Assessment

### Breaking Changes
- `ListVirtualAccounts()` now requires all 5 parameters (previously optional)
- Existing code calling this method will need to be updated

### Non-Breaking Changes
- Endpoint versioning fix is transparent to callers
- Error response parsing enhances existing error handling
- Webhook verification is a new utility function

## Next Steps

1. **Update calling code** to provide required virtual account filters
2. **Add webhook signature verification** to webhook handlers
3. **Add error type checking** where Due API errors are handled
4. **Write unit tests** for all four fixes
5. **Test against Due sandbox** to verify fixes work correctly

## Files Changed Summary

```
internal/adapters/due/client.go    - 25 changes (versioning, validation, error parsing)
internal/adapters/due/onramp.go    - 5 changes (versioning)
internal/adapters/due/types.go     - 1 change (struct update)
pkg/webhook/verify.go              - New file (signature verification)
```

## Verification Checklist

- [x] All endpoints use consistent versioning
- [x] Required query parameters enforced
- [x] Webhook signature verification implemented
- [x] Error responses properly parsed
- [ ] Unit tests written
- [ ] Integration tests passed
- [ ] Documentation updated
- [ ] Code reviewed
