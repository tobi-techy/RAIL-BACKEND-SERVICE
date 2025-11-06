# Due API Implementation Guide

## Quick Reference

### Key Endpoints

| Operation | Method | Endpoint | Headers Required |
|-----------|--------|----------|------------------|
| Create Account | POST | `/v1/accounts` | `Authorization` |
| Get Account | GET | `/v1/accounts/{id}` | `Authorization` |
| Get KYC Status | GET | `/v1/kyc` | `Authorization`, `Due-Account-Id` |
| Initiate KYC | POST | `/v1/kyc` | `Authorization`, `Due-Account-Id` |
| Link Wallet | POST | `/v1/wallets` | `Authorization`, `Due-Account-Id` |
| Create Recipient | POST | `/v1/recipients` | `Authorization`, `Due-Account-Id` |
| Create Virtual Account | POST | `/v1/virtual_accounts` | `Authorization`, `Due-Account-Id` |
| Create Transfer | POST | `/v1/transfers` | `Authorization`, `Due-Account-Id` |

### KYC Status Flow

```
pending → passed (ready to transact)
        → resubmission_required (needs more docs)
        → failed (rejected)
```

### Account Status Flow

```
onboarding → active (KYC passed + ToS accepted)
           → inactive (disabled)
```

### Critical Requirements

- ✅ **KYC Status**: Must be `passed` before any transactions
- ✅ **ToS Status**: Must be `accepted` before any transactions
- ✅ **Account Status**: Must be `active` for full functionality
- ✅ **Wallet Linking**: Required before transfers
- ✅ **Recipient Creation**: Required before off-ramp transfers

### Webhook Events

| Event | Description | Use Case |
|-------|-------------|----------|
| `bp.kyc.status_changed` | KYC status updated | Update user account status |
| `transfer.status_changed` | Transfer status updated | Track payment progress |
| `virtual_account.deposit` | Funds received | Process incoming deposits |

### Environment URLs

| Environment | API Base URL | App Base URL |
|-------------|--------------|-------------|
| Sandbox | `https://api.sandbox.due.network` | `https://app.sandbox.due.network` |
| Production | `https://api.due.network` | `https://app.due.network` |

### Common Error Codes

| Code | Meaning | Action |
|------|---------|--------|
| 400 | Bad Request | Check request parameters |
| 401 | Unauthorized | Verify API key |
| 403 | Forbidden | Check KYC/ToS status |
| 422 | Validation Error | Fix field validation issues |
| 429 | Rate Limited | Implement backoff |

---

## Overview

This guide provides step-by-step requirements and implementation methods for integrating Due API to enable:
1. User account management with KYC/KYB verification
2. Linking Circle-generated wallets to Due accounts
3. Virtual account creation for fiat deposits
4. Off-ramping USDC to USD into virtual accounts
5. Real-time webhook notifications

---

## Architecture Flow

```
User Registration → Due Account Creation → KYC/KYB Verification → 
Circle Wallet Creation → Link Wallet to Due Account → 
Create Virtual Account (USD) → Deposit USDC → Auto Off-Ramp to USD
```

---

## Prerequisites

### API Credentials
- **Due API Key**: Required for all API calls
- **Circle API Key**: For wallet management
- **Base URLs**:
  - Sandbox: `https://api.sandbox.due.network`
  - Production: `https://api.due.network`

### Required Headers
```
Authorization: Bearer {DUE_API_KEY}
Due-Account-Id: {account_id}  // Required for most endpoints
Content-Type: application/json
Accept: application/json
```

---

## Step 1: User Account Creation

### Endpoint
```
POST /v1/accounts
```

### Purpose
Create a Due account for each user. This is the foundation entity that links all payment activities.

### Account Types
- `business` - For companies and legal entities
- `individual` - For natural persons (use this for Gen Z users)

### Required Fields
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | ✅ | `business` or `individual` |
| name | string | ✅ | Legal name of account holder |
| email | string | ✅ | Contact email address |
| country | string | ✅ | ISO-2 country code (e.g., `US`, `BR`, `GB`) |
| category | string | ❌ | Category from Due's allowed list |
| kycReturnUrl | string | ❌ | Optional URL to redirect user after KYC completion |

### Request Example
```bash
curl --request POST \
  --url https://api.due.network/v1/accounts \
  -H "Authorization: Bearer your_api_key" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "individual",
    "name": "Taylor Johnson",
    "email": "taylor@example.com",
    "country": "US",
    "category": "personal_finance",
    "kycReturnUrl": "https://yourapp.com/onboarding/kyc-complete"
  }'
```

### Response
```json
{
  "id": "acct_XyZpT7HhAJFq12345",
  "type": "individual",
  "name": "Taylor Johnson",
  "email": "taylor@example.com",
  "country": "US",
  "status": "onboarding",
  "statusLog": [
    {
      "status": "onboarding",
      "timestamp": "2025-08-10T09:21:06.000Z"
    }
  ],
  "kyc": {
    "status": "pending",
    "link": "/api/bp/redirect/kyc/sumsub/sumsub_ExAmPlE123456"
  },
  "tos": {
    "id": "ta_AbCdEf123456",
    "entityName": "Due Ltd",
    "status": "pending",
    "link": "/tos/ta_AbCdEf123456",
    "documentLinks": {
      "tos": "https://example.s3.amazonaws.com/documents/tos/TOS.pdf",
      "privacyPolicy": "https://example.s3.amazonaws.com/documents/tos/PP.pdf"
    }
  }
}
```

### KYC/KYB Process Overview

Due provides multiple KYC implementation methods:

#### Method 1: Standard Hosted KYC (Recommended for MVP)
Due handles the entire KYC flow through their hosted interface powered by Sumsub.

1. **Get KYC Link**: Append `kyc.link` from account response to base URL
   - Sandbox: `https://app.sandbox.due.network{kyc.link}`
   - Production: `https://app.due.network{kyc.link}`
   
2. **Redirect User**: Send user to complete identity verification
   - User uploads ID documents (passport, driver's license, etc.)
   - User takes selfie for liveness check
   - User provides proof of address (if required)
   
3. **Monitor Status**: Poll account status or use webhooks
   - Webhook event: `bp.kyc.status_changed`
   - Poll endpoint: `GET /v1/accounts/{account_id}`

#### Method 2: Programmatic KYC Initiation
Initiate KYC programmatically and get a session link.

**Endpoint**: `POST /v1/kyc`

**Headers**:
```
Authorization: Bearer {api_key}
Due-Account-Id: {account_id}
```

**Response**:
```json
{
  "id": "acct_XyZpT7HhAJFq12345",
  "type": "individual",
  "email": "taylor@example.com",
  "applicantId": "sumsub_applicant_id",
  "externalLink": "https://app.due.network/kyc/session/xyz",
  "status": "pending",
  "country": "US",
  "token": "session_token_xyz",
  "primaryEntity": {
    "name": "Due Ltd",
    "tosLink": "https://example.com/tos",
    "privacyPolicyLink": "https://example.com/privacy",
    "country2": "GB"
  }
}
```

#### Method 3: Share Existing Sumsub KYC
If you already have Sumsub integration, share existing applicant data with Due.

**Endpoint**: `POST /v1/kyc/share/sumsub`

**Headers**:
```
Authorization: Bearer {api_key}
Due-Account-Id: {account_id}
Content-Type: application/json
```

**Request Body**:
```json
{
  "shareToken": "sumsub_share_token_from_their_api"
}
```

**Steps**:
1. Ensure your Sumsub applicant is approved with valid ID and proof of address
2. Generate share token using [Sumsub API](https://docs.sumsub.com/reference/generate-share-token)
3. Call Due's endpoint with the share token

#### Method 4: US-Specific Verification
For US users, Due offers a streamlined verification process.

**Get US Verification Link**: `GET /v1/kyc/us-local`

**Headers**:
```
Authorization: Bearer {api_key}
Due-Account-Id: {account_id}
```

**Initiate US Verification**: `POST /v1/kyc/us-local`

**Submit US Verification**: `POST /v1/kyc/us-local/submit`

#### Method 5: Create KYC Access Session
Create a temporary access session for users to complete KYC.

**Endpoint**: `POST /v1/kyc/session`

**Headers**:
```
Authorization: Bearer {api_key}
Due-Account-Id: {account_id}
```

### KYC Status Values
- `pending` - Not yet reviewed or in progress
- `passed` - Approved (required for transactions)
- `resubmission_required` - Needs additional documentation
- `failed` - Verification failed

### Get Current KYC Status

**Endpoint**: `GET /v1/kyc`

**Headers**:
```
Authorization: Bearer {api_key}
Due-Account-Id: {account_id}
```

**Response**:
```json
{
  "status": "passed",
  "link": "/api/bp/redirect/kyc/sumsub/sumsub_ExAmPlE123456",
  "applicantId": "sumsub_applicant_id"
}
```

### Update KYC Profile

If you need to update KYC information after initial submission:

**Endpoint**: `POST /v1/kyc/update-profile`

**Headers**:
```
Authorization: Bearer {api_key}
Due-Account-Id: {account_id}
```

### Terms of Service

1. **Get ToS Link**: Append `tos.link` to base URL with optional redirect
   ```
   https://app.due.network/tos/ta_AbCdEf123456?redirect=https%3A%2F%2Fyourapp.com%2Fonboarding%2Fsuccess
   ```
   
2. **User Acceptance**: Required before transacting
   - User must accept Terms of Service
   - User must accept Privacy Policy
   - Both documents are provided in `tos.documentLinks`

3. **Check ToS Status**: `GET /v1/tos/{tos_token}`

### KYC Webhook Events

Subscribe to KYC status changes via webhooks:

**Event Type**: `bp.kyc.status_changed`

**Payload Example**:
```json
{
  "type": "bp.kyc.status_changed",
  "data": {
    "id": "acct_98765ABCDE",
    "type": "individual",
    "name": "Taylor Johnson",
    "email": "taylor@example.com",
    "country": "US",
    "category": "personal_finance",
    "status": "active",
    "kyc": {
      "status": "passed",
      "link": "/api/bp/redirect/kyc/sumsub/sumsub_ExAmPlE123456"
    },
    "tos": {
      "id": "ta_AbCdEf123456",
      "entityName": "Due Ltd",
      "status": "accepted",
      "link": "/tos/ta_AbCdEf123456",
      "documentLinks": {
        "tos": "https://example.com/tos.pdf",
        "privacyPolicy": "https://example.com/privacy.pdf"
      },
      "acceptedAt": "2025-08-10T09:22:13.000Z",
      "token": "token123"
    }
  }
}
```

**Setup Webhook**:
```bash
curl -X POST "https://api.due.network/v1/webhook_endpoints" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "url": "https://yourapp.com/webhooks/due",
    "events": ["bp.kyc.status_changed"],
    "description": "KYC status updates"
  }'
```

### Implementation Notes

1. **Store Critical IDs**:
   - `account.id` - Required for all API calls
   - `kyc.applicantId` - For tracking in Sumsub
   - `tos.id` - For ToS tracking

2. **Create Accounts Early**: 
   - Create Due account at the start of your onboarding flow
   - This allows users to complete KYC and ToS without delays

3. **Use Webhooks**: 
   - Subscribe to `bp.kyc.status_changed` for real-time updates
   - Don't rely solely on polling
   - Verify webhook authenticity

4. **Handle KYC States**:
   - `pending`: Show "Under Review" message
   - `passed`: Enable full platform features
   - `resubmission_required`: Prompt user to upload additional documents
   - `failed`: Show error and support contact

5. **Account Status Progression**:
   - `onboarding` → User created, KYC pending
   - `active` → KYC passed, ToS accepted, ready to transact
   - `inactive` → Account disabled

6. **Security Best Practices**:
   - Always use HTTPS for KYC redirects
   - Validate webhook signatures
   - Store KYC status in your database
   - Log all KYC state changes for audit trail

7. **User Experience**:
   - Use `kycReturnUrl` to redirect users back to your app
   - Show clear progress indicators during KYC
   - Provide estimated completion time (typically 5-10 minutes)
   - Send email notifications on status changes

8. **Testing in Sandbox**:
   - Sandbox KYC auto-approves after submission
   - Use test documents for identity verification
   - Test all KYC status transitions
   - Verify webhook delivery

9. **Compliance Requirements**:
   - Account must have `kyc.status: "passed"` before any transactions
   - ToS must be accepted (`tos.status: "accepted"`)
   - Store acceptance timestamps for regulatory compliance
   - Maintain audit logs of all KYC events

10. **Error Handling**:
    - Handle network timeouts gracefully
    - Retry failed webhook deliveries
    - Provide clear error messages to users
    - Log all API errors for debugging

---

## Step 2: Link Circle Wallet to Due Account

### Endpoint
```
POST /v1/wallets
```

### Purpose
Link a Circle-generated wallet address to the Due account for compliance monitoring and transaction tracking.

### Required Headers
```
Authorization: Bearer {DUE_API_KEY}
Due-Account-Id: {account_id}  // REQUIRED
Accept: application/json
Content-Type: application/json
```

### Request Body (EVM Chains)
```json
{
  "address": "evm:0x1b7b5e051497f526ed41d177Bef603d51320322D"
}
```

### Request Body (Starknet)
```json
{
  "address": "starknet:0x0322142e7b058c6a051d7a298e99ed9b8b5c6a5e3e4e689237a37b570287d2d3"
}
```

### Request Example
```bash
curl --request POST https://api.due.network/v1/wallets \
  --header "Authorization: Bearer $DUE_API_KEY" \
  --header "Accept: application/json" \
  --header "Content-Type: application/json" \
  --header "Due-Account-Id: acct_123" \
  --data '{
    "address": "evm:0x1b7b5e051497f526ed41d177Bef603d51320322D"
  }'
```

### Response
```json
{
  "id": "wlt_e1NNNZ9HQyd01M0R",
  "address": "evm:0x1b7b5e051497f526ed41d177Bef603d51320322D",
  "accountId": "acct_123",
  "isActive": true,
  "createdAt": "2024-03-15T10:30:00Z"
}
```

### List Linked Wallets
```bash
curl --request GET https://api.due.network/v1/wallets \
  --header "Authorization: Bearer $DUE_API_KEY" \
  --header "Accept: application/json" \
  --header "Due-Account-Id: acct_123"
```

### Typical Flow
1. **Create Circle Wallet**: Use Circle API to generate managed wallet
2. **Link to Due**: Call POST /v1/wallets with wallet address
3. **Verify**: Call GET /v1/wallets to confirm linking
4. **Use in Transfers**: Reference `wallet_id` in transfer operations

### Implementation Notes
- Link wallets before initiating any transfers
- Both external and Due-managed wallets must be linked
- Wallet linking enables compliance screening
- Store `wallet_id` for transfer operations

---

## Step 3: Create Virtual Account for USD Deposits

### Endpoint
```
POST /v1/virtual_accounts
```

### Purpose
Create a dedicated virtual account that accepts USDC deposits and automatically converts them to USD, settling into a specified destination (bank account or recipient).

### Use Case: USDC to USD Off-Ramp
Accept USDC deposits on blockchain networks and automatically convert to USD in a bank account.

### Required Parameters
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| destination | string | ✅ | Recipient ID or wallet ID for settlement |
| schemaIn | string | ✅ | Input method: `evm`, `tron`, `starknet` |
| currencyIn | string | ✅ | Input currency: `USDC`, `USDT` |
| railOut | string | ✅ | Settlement rail: `ach`, `sepa`, `swift` |
| currencyOut | string | ✅ | Output currency: `USD`, `EUR`, `GBP` |
| reference | string | ✅ | Your unique tracking reference |

### Request Example: USDC to USD (ACH)
```bash
curl -X POST https://api.due.network/v1/virtual_accounts \
  -H "Authorization: Bearer your_api_key" \
  -H "Due-Account-Id: acct_XyZpT7HhAJFq12345" \
  -H "Content-Type: application/json" \
  -d '{
    "destination": "{recipient_id}",
    "schemaIn": "evm",
    "currencyIn": "USDC",
    "railOut": "ach",
    "currencyOut": "USD",
    "reference": "user_taylor_usdc_offramp"
  }'
```

### Response
```json
{
  "ownerId": "acct_XyZpT7HhAJFq12345",
  "destinationId": "{recipient_id}",
  "schemaIn": "evm",
  "currencyIn": "USDC",
  "railOut": "ach",
  "currencyOut": "USD",
  "nonce": "user_taylor_usdc_offramp",
  "details": {
    "address": "0x742d35Cc6665C6c175E8c7CaB7CeEf5634123456",
    "network": "ethereum",
    "memo": "VA_12345"
  },
  "isActive": true,
  "createdAt": "2024-03-15T10:30:00Z"
}
```

### Virtual Account Types

#### 1. Fiat-to-Crypto On-Ramps
Accept fiat deposits, deliver stablecoins to crypto addresses.

**EUR to USDC Example:**
```bash
curl -X POST https://api.due.network/v1/virtual_accounts \
  -H "Authorization: Bearer your_api_key" \
  -H "Due-Account-Id: your_account_id" \
  -H "Content-Type: application/json" \
  -d '{
    "destination": "wlt_e1NNNZ9HQyd01M0R",
    "schemaIn": "bank_sepa",
    "currencyIn": "EUR",
    "railOut": "ethereum",
    "currencyOut": "USDC",
    "reference": "customer_alice_eur_onramp"
  }'
```

Response includes IBAN for EUR deposits:
```json
{
  "details": {
    "IBAN": "DE89370400440532013000",
    "bankName": "Due Payments Europe",
    "beneficiaryName": "Due Payments Europe"
  }
}
```

#### 2. Crypto Liquidation (Off-Ramp)
Accept crypto deposits, settle to fiat accounts.

**USDC to USD Example:**
```bash
curl -X POST https://api.due.network/v1/virtual_accounts \
  -H "Authorization: Bearer your_api_key" \
  -H "Due-Account-Id: your_account_id" \
  -H "Content-Type: application/json" \
  -d '{
    "destination": "{recipient_id}",
    "schemaIn": "evm",
    "currencyIn": "USDC",
    "railOut": "ach",
    "currencyOut": "USD",
    "reference": "crypto_profits_liquidation"
  }'
```

#### 3. Cross-Network Bridging
Accept crypto on one network, deliver on another.

**USDT Tron to USDC Arbitrum:**
```bash
curl -X POST https://api.due.network/v1/virtual_accounts \
  -H "Authorization: Bearer your_api_key" \
  -H "Due-Account-Id: your_account_id" \
  -H "Content-Type: application/json" \
  -d '{
    "destination": "wlt_e1NNNZ9HQyd01M0R",
    "schemaIn": "tron",
    "currencyIn": "USDT",
    "railOut": "arbitrum",
    "currencyOut": "USDC",
    "reference": "customer_tron_arbitrum_bridge"
  }'
```

### List Virtual Accounts
```bash
curl -H "Authorization: Bearer your_api_key" \
  -H "Due-Account-Id: your_account_id" \
  "https://api.due.network/v1/virtual_accounts?currencyIn=USDC&railOut=ach"
```

### Get Virtual Account by Reference
```bash
curl -H "Authorization: Bearer your_api_key" \
  -H "Due-Account-Id: your_account_id" \
  https://api.due.network/v1/virtual_accounts/user_taylor_usdc_offramp
```

### Discover Available Configurations
```bash
# Get available static deposit methods
curl -H "Authorization: Bearer your_api_key" \
  -H "Due-Account-Id: your_account_id" \
  https://api.due.network/v1/channels | jq '.[] | select(.type == "static_deposit")'

# Get available settlement methods
curl -H "Authorization: Bearer your_api_key" \
  -H "Due-Account-Id: your_account_id" \
  https://api.due.network/v1/channels | jq '.[] | select(.type == "withdrawal")'
```

### How Virtual Accounts Work
1. **Create**: Specify input method and output destination
2. **Receive Details**: Get dedicated address/account details
3. **Share with User**: User deposits to the unique address
4. **Auto-Convert**: System converts and delivers to settlement destination
5. **Monitor**: Use webhooks to track deposit events

### Implementation Notes
- Virtual accounts are persistent - create once, use multiple times
- Each virtual account is unique to a customer/use case
- Automatic conversion happens on deposit
- No manual reconciliation needed
- Reference field is stored as unique identifier (nonce)

---

## Step 4: Create Recipient for USD Settlement

### Endpoint
```
POST /v1/recipients
```

### Purpose
Create a recipient (bank account) where USD will be settled after USDC off-ramp.

### Required Fields
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| name | string | ✅ | Legal name or business name |
| details | object | ✅ | Schema-specific payment details |
| isExternal | boolean | ✅ | true for third-party, false for own account |

### US Bank Account Example
```bash
curl -X POST https://api.due.network/v1/recipients \
  -H "Authorization: Bearer your_api_key" \
  -H "Due-Account-Id: your_account_id" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Taylor Johnson",
    "details": {
      "schema": "bank_us",
      "bankName": "JPMorgan Chase Bank",
      "accountName": "Taylor Johnson",
      "accountNumber": "123456789",
      "routingNumber": "021000021",
      "beneficiaryAddress": {
        "street_line_1": "123 Main Street",
        "city": "New York",
        "postal_code": "10001",
        "country": "USA",
        "state": "NY"
      }
    },
    "isExternal": false
  }'
```

### Response
```json
{
  "id": "recipient_1234567890abcdef",
  "label": "Taylor Johnson",
  "details": {
    "schema": "bank_us",
    "bankName": "JPMorgan Chase Bank",
    "accountName": "Taylor Johnson",
    "accountNumber": "123456789",
    "routingNumber": "021000021",
    "beneficiaryAddress": {
      "street_line_1": "123 Main Street",
      "city": "New York",
      "postal_code": "10001",
      "country": "USA",
      "state": "NY"
    }
  },
  "isExternal": false,
  "isActive": true
}
```

### Other Recipient Types

#### SEPA (Europe)
```json
{
  "name": "Marie Dubois",
  "details": {
    "schema": "bank_sepa",
    "accountType": "individual",
    "firstName": "Marie",
    "lastName": "Dubois",
    "IBAN": "FR1420041010050500013M02606"
  },
  "isExternal": true
}
```

#### SWIFT (International)
```json
{
  "name": "Singapore Tech Pte Ltd",
  "details": {
    "schema": "bank_swift",
    "accountType": "business",
    "companyName": "Singapore Tech Pte Ltd",
    "bankName": "DBS Bank Ltd",
    "swiftCode": "DBSSSGSG",
    "accountNumber": "1234567890",
    "currency": "SGD"
  },
  "isExternal": true
}
```

### List Recipients
```bash
curl -H "Authorization: Bearer your_api_key" \
  -H "Due-Account-Id: your_account_id" \
  https://api.due.network/v1/recipients
```

### Implementation Notes
- Create recipient before creating virtual account
- Store `recipient_id` for virtual account destination
- Recipients cannot be modified (delete and recreate)
- Recipients with pending payments cannot be deleted
- Automatic deduplication based on account details

---

## Step 5: Initiate Direct Transfer (Alternative Method)

### Endpoint
```
POST /v1/transfers
```

### Purpose
Initiate a one-time transfer from a wallet to a recipient (alternative to virtual accounts).

### Required Fields
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| quote | string | ✅ | Quote ID from quote endpoint |
| sender | string | ✅ | Wallet ID (linked wallet) |
| recipient | string | ✅ | Recipient ID |
| memo | string | ❌ | Optional memo/reference |

### Flow
1. **Create Quote**: Get exchange rate and fees
2. **Create Transfer**: Execute the transfer
3. **Monitor Status**: Track transfer completion

### Create Quote
```bash
curl -X POST https://api.due.network/v1/transfers/quote \
  -H "Authorization: Bearer your_api_key" \
  -H "Due-Account-Id: your_account_id" \
  -H "Content-Type: application/json" \
  -d '{
    "sender": "wlt_e1NNNZ9HQyd01M0R",
    "recipient": "recipient_1234567890abcdef",
    "amount": "1000",
    "currency": "USDC"
  }'
```

### Create Transfer
```bash
curl --request POST \
  --url https://api.due.network/v1/transfers \
  --header "Authorization: Bearer your_api_key" \
  --header "Due-Account-Id: your_account_id" \
  --header "Content-Type: application/json" \
  --data '{
    "quote": "quote_abc123",
    "sender": "wlt_e1NNNZ9HQyd01M0R",
    "recipient": "recipient_1234567890abcdef",
    "memo": "Monthly withdrawal"
  }'
```

### List Transfers
```bash
curl --request GET \
  --url "https://api.due.network/v1/transfers?limit=50&order=desc" \
  --header "Authorization: Bearer your_api_key" \
  --header "Due-Account-Id: your_account_id"
```

---

## Complete Implementation Flow

### For STACK Platform: USDC Off-Ramp to USD

```
1. User Signs Up
   ↓
2. Create Due Account (POST /v1/accounts)
   - type: "individual"
   - Store account_id
   ↓
3. Complete KYC
   - Redirect to kyc.link
   - Wait for status: "passed"
   ↓
4. Accept Terms of Service
   - Redirect to tos.link
   ↓
5. Create Circle Wallet (Circle API)
   - Generate managed wallet
   - Store wallet address
   ↓
6. Link Wallet to Due (POST /v1/wallets)
   - address: "evm:0x..."
   - Store wallet_id
   ↓
7. Create USD Recipient (POST /v1/recipients)
   - User's bank account details
   - Store recipient_id
   ↓
8. Create Virtual Account (POST /v1/virtual_accounts)
   - destination: recipient_id
   - schemaIn: "evm"
   - currencyIn: "USDC"
   - railOut: "ach"
   - currencyOut: "USD"
   - Store virtual account address
   ↓
9. User Deposits USDC
   - Send USDC to virtual account address
   - Due automatically converts to USD
   - USD settles to recipient bank account
   ↓
10. Monitor via Webhooks
    - transfer.created
    - transfer.completed
    - virtual_account.deposit
```

---

## Webhook Events

### Setup Webhook Endpoint
```
POST /v1/webhook-endpoints
```

### Key Events
- `account.kyc.updated` - KYC status changed
- `account.tos.accepted` - Terms accepted
- `virtual_account.deposit` - Funds received
- `transfer.created` - Transfer initiated
- `transfer.completed` - Transfer settled
- `transfer.failed` - Transfer failed

### Webhook Payload Example
```json
{
  "event": "virtual_account.deposit",
  "data": {
    "virtualAccountId": "va_123",
    "reference": "user_taylor_usdc_offramp",
    "amountIn": "1000.00",
    "currencyIn": "USDC",
    "amountOut": "999.50",
    "currencyOut": "USD",
    "status": "completed",
    "timestamp": "2024-03-15T10:30:00Z"
  }
}
```

---

## Error Handling

### Common Error Codes
- `400` - Bad Request (invalid parameters)
- `401` - Unauthorized (invalid API key)
- `403` - Forbidden (account not verified)
- `404` - Not Found (resource doesn't exist)
- `422` - Validation Error (field validation failed)
- `429` - Rate Limit Exceeded
- `500` - Internal Server Error

### Validation Error Example
```json
{
  "statusCode": 422,
  "message": "Validation error",
  "code": "err_validation",
  "details": {
    "routingNumber": "required",
    "beneficiaryAddress.street_line_1": "required"
  }
}
```

---

## Security Best Practices

1. **API Key Management**
   - Store API keys in environment variables
   - Never commit keys to version control
   - Rotate keys periodically
   - Use different keys for sandbox/production

2. **Webhook Security**
   - Verify webhook signatures
   - Use HTTPS endpoints only
   - Implement idempotency checks
   - Log all webhook events

3. **Data Protection**
   - Encrypt sensitive data at rest
   - Use TLS for all API calls
   - Implement rate limiting
   - Log all API interactions

4. **Compliance**
   - Ensure KYC completion before transactions
   - Monitor transaction limits
   - Implement AML screening
   - Maintain audit trails

---

## Testing in Sandbox

### Sandbox Environment
- Base URL: `https://api.sandbox.due.network`
- App URL: `https://app.sandbox.due.network`

### Test KYC
- Sandbox KYC auto-approves after submission
- Use test data for identity verification

### Test Deposits
```
POST /dev/payin
```
Simulate deposits to virtual accounts for testing.

---

## Rate Limits

- **Default**: 100 requests per minute per API key
- **Burst**: 20 requests per second
- **Headers**: Check `X-RateLimit-*` headers in responses

---

## Support Resources

- **Documentation**: https://due.readme.io/docs
- **API Reference**: https://due.readme.io/reference
- **Support Email**: demo@due.network
- **Status Page**: Check for service status

---

## KYC Implementation Example (Go)

### Complete KYC Flow Implementation

```go
package due

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DueClient handles Due API interactions
type DueClient struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewDueClient creates a new Due API client
func NewDueClient(apiKey string, sandbox bool) *DueClient {
	baseURL := "https://api.due.network"
	if sandbox {
		baseURL = "https://api.sandbox.due.network"
	}

	return &DueClient{
		apiKey:  apiKey,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Account represents a Due account
type Account struct {
	ID         string        `json:"id"`
	Type       string        `json:"type"`
	Name       string        `json:"name"`
	Email      string        `json:"email"`
	Country    string        `json:"country"`
	Category   string        `json:"category,omitempty"`
	Status     string        `json:"status"`
	StatusLog  []StatusLog   `json:"statusLog,omitempty"`
	KYC        KYCInfo       `json:"kyc"`
	ToS        ToSInfo       `json:"tos"`
}

type StatusLog struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

type KYCInfo struct {
	Status      string `json:"status"`
	Link        string `json:"link"`
	ApplicantID string `json:"applicantId,omitempty"`
}

type ToSInfo struct {
	ID            string            `json:"id"`
	EntityName    string            `json:"entityName,omitempty"`
	Status        string            `json:"status"`
	Link          string            `json:"link"`
	DocumentLinks map[string]string `json:"documentLinks,omitempty"`
	AcceptedAt    *time.Time        `json:"acceptedAt,omitempty"`
}

// CreateAccountRequest represents account creation request
type CreateAccountRequest struct {
	Type         string `json:"type"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	Country      string `json:"country"`
	Category     string `json:"category,omitempty"`
	KYCReturnURL string `json:"kycReturnUrl,omitempty"`
}

// CreateAccount creates a new Due account
func (c *DueClient) CreateAccount(ctx context.Context, req CreateAccountRequest) (*Account, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/accounts", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var account Account
	if err := json.NewDecoder(resp.Body).Decode(&account); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &account, nil
}

// GetAccount retrieves account details
func (c *DueClient) GetAccount(ctx context.Context, accountID string) (*Account, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/v1/accounts/"+accountID, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var account Account
	if err := json.NewDecoder(resp.Body).Decode(&account); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &account, nil
}

// GetKYCStatus retrieves current KYC status
func (c *DueClient) GetKYCStatus(ctx context.Context, accountID string) (*KYCInfo, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/v1/kyc", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Due-Account-Id", accountID)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var kycInfo KYCInfo
	if err := json.NewDecoder(resp.Body).Decode(&kycInfo); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &kycInfo, nil
}

// InitiateKYC initiates KYC process programmatically
func (c *DueClient) InitiateKYC(ctx context.Context, accountID string) (*KYCInfo, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/kyc", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Due-Account-Id", accountID)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var kycInfo KYCInfo
	if err := json.NewDecoder(resp.Body).Decode(&kycInfo); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &kycInfo, nil
}

// GetKYCLink generates the full KYC URL for user redirection
func (c *DueClient) GetKYCLink(kycLink string) string {
	appBaseURL := "https://app.due.network"
	if c.baseURL == "https://api.sandbox.due.network" {
		appBaseURL = "https://app.sandbox.due.network"
	}
	return appBaseURL + kycLink
}

// GetToSLink generates the full ToS URL for user redirection
func (c *DueClient) GetToSLink(tosLink, redirectURL string) string {
	appBaseURL := "https://app.due.network"
	if c.baseURL == "https://api.sandbox.due.network" {
		appBaseURL = "https://app.sandbox.due.network"
	}
	
	url := appBaseURL + tosLink
	if redirectURL != "" {
		url += "?redirect=" + redirectURL
	}
	return url
}

// WebhookEvent represents a Due webhook event
type WebhookEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// HandleKYCWebhook processes KYC status change webhooks
func HandleKYCWebhook(payload []byte) (*Account, error) {
	var event WebhookEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, fmt.Errorf("unmarshal webhook: %w", err)
	}

	if event.Type != "bp.kyc.status_changed" {
		return nil, fmt.Errorf("unexpected event type: %s", event.Type)
	}

	var account Account
	if err := json.Unmarshal(event.Data, &account); err != nil {
		return nil, fmt.Errorf("unmarshal account data: %w", err)
	}

	return &account, nil
}
```

### Usage Example

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
)

func main() {
	// Initialize Due client
	client := NewDueClient("your_api_key", true) // true for sandbox

	ctx := context.Background()

	// Step 1: Create account
	account, err := client.CreateAccount(ctx, CreateAccountRequest{
		Type:         "individual",
		Name:         "Taylor Johnson",
		Email:        "taylor@example.com",
		Country:      "US",
		Category:     "personal_finance",
		KYCReturnURL: "https://yourapp.com/onboarding/kyc-complete",
	})
	if err != nil {
		log.Fatalf("Failed to create account: %v", err)
	}

	fmt.Printf("Account created: %s\n", account.ID)
	fmt.Printf("KYC Status: %s\n", account.KYC.Status)

	// Step 2: Generate KYC link for user
	kycURL := client.GetKYCLink(account.KYC.Link)
	fmt.Printf("Redirect user to: %s\n", kycURL)

	// Step 3: Generate ToS link
	redirectURL := url.QueryEscape("https://yourapp.com/onboarding/success")
	tosURL := client.GetToSLink(account.ToS.Link, redirectURL)
	fmt.Printf("ToS URL: %s\n", tosURL)

	// Step 4: Check KYC status (polling - use webhooks in production)
	kycInfo, err := client.GetKYCStatus(ctx, account.ID)
	if err != nil {
		log.Fatalf("Failed to get KYC status: %v", err)
	}
	fmt.Printf("Current KYC Status: %s\n", kycInfo.Status)

	// Step 5: Get full account details
	updatedAccount, err := client.GetAccount(ctx, account.ID)
	if err != nil {
		log.Fatalf("Failed to get account: %v", err)
	}
	fmt.Printf("Account Status: %s\n", updatedAccount.Status)
	fmt.Printf("KYC Status: %s\n", updatedAccount.KYC.Status)
	fmt.Printf("ToS Status: %s\n", updatedAccount.ToS.Status)
}
```

### Webhook Handler Example

```go
package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
)

func handleDueWebhook(w http.ResponseWriter, r *http.Request) {
	// Read webhook payload
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse webhook event
	var event WebhookEvent
	if err := json.Unmarshal(body, &event); err != nil {
		log.Printf("Failed to parse webhook: %v", err)
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	// Handle different event types
	switch event.Type {
	case "bp.kyc.status_changed":
		account, err := HandleKYCWebhook(body)
		if err != nil {
			log.Printf("Failed to handle KYC webhook: %v", err)
			http.Error(w, "Processing error", http.StatusInternalServerError)
			return
		}

		log.Printf("KYC status changed for account %s: %s", account.ID, account.KYC.Status)

		// Update your database
		if err := updateAccountKYCStatus(account.ID, account.KYC.Status); err != nil {
			log.Printf("Failed to update database: %v", err)
			// Don't return error - acknowledge webhook receipt
		}

		// Send notification to user
		if account.KYC.Status == "passed" {
			sendKYCApprovedEmail(account.Email, account.Name)
		} else if account.KYC.Status == "failed" {
			sendKYCRejectedEmail(account.Email, account.Name)
		}

	default:
		log.Printf("Unhandled webhook event type: %s", event.Type)
	}

	// Always acknowledge webhook receipt
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"received"}`))
}

func updateAccountKYCStatus(accountID, status string) error {
	// Update your database
	// db.Exec("UPDATE accounts SET kyc_status = $1, updated_at = NOW() WHERE due_account_id = $2", status, accountID)
	return nil
}

func sendKYCApprovedEmail(email, name string) {
	// Send email notification
	log.Printf("Sending KYC approved email to %s", email)
}

func sendKYCRejectedEmail(email, name string) {
	// Send email notification
	log.Printf("Sending KYC rejected email to %s", email)
}
```

### Database Schema Example

```sql
CREATE TABLE due_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    due_account_id VARCHAR(255) NOT NULL UNIQUE,
    account_type VARCHAR(50) NOT NULL, -- 'individual' or 'business'
    account_status VARCHAR(50) NOT NULL, -- 'onboarding', 'active', 'inactive'
    
    -- KYC Information
    kyc_status VARCHAR(50) NOT NULL, -- 'pending', 'passed', 'resubmission_required', 'failed'
    kyc_link TEXT,
    kyc_applicant_id VARCHAR(255),
    kyc_completed_at TIMESTAMP,
    
    -- ToS Information
    tos_id VARCHAR(255),
    tos_status VARCHAR(50), -- 'pending', 'accepted'
    tos_accepted_at TIMESTAMP,
    
    -- Metadata
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    
    INDEX idx_user_id (user_id),
    INDEX idx_due_account_id (due_account_id),
    INDEX idx_kyc_status (kyc_status)
);

CREATE TABLE due_kyc_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    due_account_id VARCHAR(255) NOT NULL,
    event_type VARCHAR(100) NOT NULL,
    old_status VARCHAR(50),
    new_status VARCHAR(50) NOT NULL,
    event_data JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    
    INDEX idx_due_account_id (due_account_id),
    INDEX idx_created_at (created_at)
);
```

---

## Troubleshooting Guide

### KYC Issues

#### Problem: KYC Status Stuck on "pending"

**Possible Causes**:
- User hasn't completed KYC flow
- Documents under review (can take 5-30 minutes)
- Sandbox environment delay

**Solutions**:
1. Check if user actually visited KYC link
2. In sandbox, KYC should auto-approve after submission
3. Poll `/v1/accounts/{id}` to check current status
4. Check webhook logs for missed events
5. Contact Due support if stuck > 1 hour in production

#### Problem: KYC Status "resubmission_required"

**Possible Causes**:
- Poor quality document images
- Document expired or invalid
- Name mismatch between account and documents
- Missing proof of address

**Solutions**:
1. Redirect user back to KYC link to resubmit
2. Provide clear instructions on document requirements
3. Ensure documents are:
   - Clear and readable
   - Not expired
   - Match the name on the account
4. Check email for specific rejection reasons

#### Problem: KYC Status "failed"

**Possible Causes**:
- User on sanctions list
- Country not supported
- Fraudulent documents detected
- Multiple failed attempts

**Solutions**:
1. Contact Due support for specific reason
2. User may need to contact support directly
3. Cannot retry without Due approval
4. Consider alternative verification methods

### API Integration Issues

#### Problem: 401 Unauthorized Error

**Possible Causes**:
- Invalid API key
- API key not in Authorization header
- Wrong environment (sandbox vs production)

**Solutions**:
```bash
# Verify API key format
curl -H "Authorization: Bearer YOUR_API_KEY" \
  https://api.sandbox.due.network/v1/accounts

# Check response for authentication errors
```

#### Problem: 403 Forbidden Error

**Possible Causes**:
- KYC not passed
- ToS not accepted
- Account not active
- Missing `Due-Account-Id` header

**Solutions**:
1. Check account status: `GET /v1/accounts/{id}`
2. Verify KYC status is "passed"
3. Verify ToS status is "accepted"
4. Ensure `Due-Account-Id` header is set for operations

#### Problem: 422 Validation Error

**Example Error**:
```json
{
  "statusCode": 422,
  "message": "Validation error",
  "code": "err_validation",
  "details": {
    "country": "must be ISO-2 format",
    "email": "invalid email format"
  }
}
```

**Solutions**:
1. Check all required fields are present
2. Verify field formats (ISO-2 country codes, valid emails)
3. Review API documentation for field requirements
4. Check `details` object for specific field errors

### Webhook Issues

#### Problem: Webhooks Not Received

**Possible Causes**:
- Webhook endpoint not publicly accessible
- Firewall blocking Due's IPs
- Endpoint returning non-2xx status
- SSL certificate issues

**Solutions**:
1. Test endpoint accessibility:
   ```bash
   curl -X POST https://yourapp.com/webhooks/due \
     -H "Content-Type: application/json" \
     -d '{"type":"test","data":{}}'
   ```
2. Ensure endpoint returns 200 OK quickly (< 5 seconds)
3. Use valid SSL certificate (not self-signed)
4. Check webhook endpoint logs
5. Verify webhook is registered: `GET /v1/webhook_endpoints`

#### Problem: Duplicate Webhook Events

**Possible Causes**:
- Webhook endpoint timeout causing retries
- Network issues
- Due's retry mechanism

**Solutions**:
1. Implement idempotency using event ID
2. Store processed event IDs in database
3. Return 200 OK immediately, process async
4. Example:
   ```go
   func handleWebhook(eventID string, payload []byte) error {
       // Check if already processed
       if isProcessed(eventID) {
           return nil // Already handled
       }
       
       // Process event
       if err := processEvent(payload); err != nil {
           return err
       }
       
       // Mark as processed
       markProcessed(eventID)
       return nil
   }
   ```

### Virtual Account Issues

#### Problem: Virtual Account Not Receiving Deposits

**Possible Causes**:
- Wrong network used for deposit
- Insufficient gas for transaction
- Virtual account not active
- Minimum deposit amount not met

**Solutions**:
1. Verify virtual account is active: `GET /v1/virtual_accounts/{reference}`
2. Check correct network (Ethereum, Polygon, etc.)
3. Verify deposit address is correct
4. Check blockchain explorer for transaction status
5. Ensure deposit meets minimum amount requirements

#### Problem: Conversion Not Happening

**Possible Causes**:
- Recipient not properly configured
- Insufficient liquidity
- Currency pair not supported
- Account limits exceeded

**Solutions**:
1. Verify recipient exists and is active
2. Check supported currency pairs: `GET /v1/channels`
3. Review account limits
4. Contact Due support for liquidity issues

### Transfer Issues

#### Problem: Transfer Stuck in "pending"

**Possible Causes**:
- Blockchain confirmation delays
- Bank processing times (ACH: 1-3 days)
- Compliance review
- Insufficient balance

**Solutions**:
1. Check transfer status: `GET /v1/transfers/{id}`
2. For crypto: Check blockchain confirmations
3. For fiat: Normal processing times apply
4. Monitor webhook events for status updates

#### Problem: Transfer Failed

**Possible Causes**:
- Invalid recipient details
- Insufficient funds
- Compliance rejection
- Network issues

**Solutions**:
1. Check error message in transfer object
2. Verify recipient bank details
3. Ensure sufficient balance
4. Contact Due support for compliance issues

### Testing Tips

#### Sandbox Environment

1. **KYC Auto-Approval**:
   - Use any test documents
   - KYC approves automatically after submission
   - Test all status transitions

2. **Test Deposits**:
   ```bash
   # Simulate deposit to virtual account
   curl -X POST https://api.sandbox.due.network/dev/payin \
     -H "Authorization: Bearer YOUR_API_KEY" \
     -d '{
       "virtualAccountId": "va_123",
       "amount": "100.00",
       "currency": "USDC"
     }'
   ```

3. **Webhook Testing**:
   - Use tools like ngrok for local testing
   - Use webhook.site to inspect payloads
   - Test all event types

#### Common Test Scenarios

1. **Happy Path**:
   - Create account → Complete KYC → Accept ToS → Link wallet → Create recipient → Create virtual account → Deposit → Transfer

2. **KYC Rejection**:
   - Test resubmission flow
   - Test user notifications
   - Test UI state handling

3. **Failed Transfer**:
   - Test error handling
   - Test user notifications
   - Test retry logic

4. **Webhook Failures**:
   - Test retry mechanism
   - Test idempotency
   - Test timeout handling

### Debug Checklist

When encountering issues, check:

- [ ] API key is correct and for right environment
- [ ] All required headers are present
- [ ] Request body matches API specification
- [ ] Account KYC status is "passed"
- [ ] Account ToS status is "accepted"
- [ ] Account status is "active"
- [ ] Wallet is linked before transfers
- [ ] Recipient exists before transfers
- [ ] Virtual account is active
- [ ] Webhook endpoint is publicly accessible
- [ ] Webhook endpoint returns 200 OK
- [ ] SSL certificate is valid
- [ ] Rate limits not exceeded
- [ ] Network connectivity is stable

### Getting Help

1. **Documentation**: https://due.readme.io/docs
2. **API Reference**: https://due.readme.io/reference
3. **Support Email**: demo@due.network
4. **Status Page**: Check for service outages

When contacting support, include:
- Account ID
- Request ID (from response headers)
- Timestamp of issue
- Full error message
- Steps to reproduce

---

## Next Steps

1. **Get API Credentials**: Contact Due at demo@due.network for API keys
2. **Review Schemas**: Check `/v1/channels` for supported configurations
3. **Implement KYC Flow**: Use the code examples above as a starting point
4. **Test in Sandbox**: Implement and test full KYC flow with test data
5. **Setup Webhooks**: Configure webhook endpoints for real-time updates
6. **Security Review**: Implement webhook signature verification
7. **Load Testing**: Test with expected user volumes
8. **Monitor & Alert**: Set up monitoring for KYC completion rates
9. **Go Live**: Switch to production credentials after thorough testing
10. **Post-Launch**: Monitor KYC approval rates and user feedback
