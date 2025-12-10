# Postman Collection Quick Start Guide

This guide will help you get started with testing the RAIL API using the auto-generated Postman collection.

## 📦 Generate Collection

The Postman collection is automatically generated from the codebase to ensure it's always up-to-date.

### Option 1: Using Make (Recommended)

```bash
make postman-collection
```

### Option 2: Using Python Directly

```bash
python3 scripts/postman_generator/generate.py
```

This creates `postman_collection_generated.json` in the project root.

## 📥 Import to Postman

1. Open Postman Desktop or Web
2. Click **Import** button (top left)
3. Select **File** tab
4. Choose `postman_collection_generated.json`
5. Click **Import**

The collection will appear in your workspace with all endpoints organized by category.

## 🔧 Setup Environment Variables

Before testing, configure these variables:

1. Click on the collection name
2. Go to **Variables** tab
3. Set the following:

| Variable | Value | Description |
|----------|-------|-------------|
| `base_url` | `http://localhost:8080` | API base URL |
| `access_token` | (auto-set after login) | JWT access token |
| `refresh_token` | (auto-set after login) | JWT refresh token |
| `user_id` | (auto-set after login) | Current user ID |

## 🚀 Testing Workflow

### 1. Health Check

Start by verifying the service is running:

```
🏥 Health & Monitoring → Health Check
```

Expected response:
```json
{
  "status": "healthy",
  "timestamp": "2025-12-09T20:00:00Z"
}
```

### 2. User Registration

Create a new user account:

```
🔐 Authentication → Register User
```

Request body:
```json
{
  "email": "test@example.com",
  "password": "SecurePass123!",
  "first_name": "John",
  "last_name": "Doe",
  "phone": "+1234567890"
}
```

### 3. Verify Email

Use the code sent to your email:

```
🔐 Authentication → Verify Email Code
```

Request body:
```json
{
  "email": "test@example.com",
  "code": "123456"
}
```

### 4. Login

Login to get access tokens:

```
🔐 Authentication → Login
```

Request body:
```json
{
  "email": "test@example.com",
  "password": "SecurePass123!"
}
```

**Note**: The `access_token` and `refresh_token` are automatically saved to collection variables.

### 5. Test Protected Endpoints

Now you can test any protected endpoint. The Bearer token is automatically included.

Example:
```
👤 Users → Get Profile
💰 Wallets → Get Wallet Addresses
💸 Funding → Get Balances
```

## 📁 Collection Structure

The collection is organized into these categories:

### 🏥 Health & Monitoring
- Health Check
- Readiness Check
- Liveness Check
- Version Info
- Metrics

### 🔐 Authentication
- Register User
- Verify Email Code
- Resend Verification Code
- Login
- Refresh Token
- Logout
- Forgot Password
- Reset Password

### 🚀 Onboarding
- Start Onboarding
- Get Onboarding Status
- Submit KYC
- Get KYC Status

### 👤 Users
- Get Profile
- Update Profile
- Change Password
- Delete Account
- Enable 2FA
- Disable 2FA

### 🔒 Security
- Get Passcode Status
- Create Passcode
- Verify Passcode
- Update Passcode
- Remove Passcode

### 💰 Wallets
- Get Wallet Addresses
- Get Wallet Status
- Get Wallet by Chain
- Initiate Wallet Creation
- Provision Wallets

### 💸 Funding
- Create Deposit Address
- Get Funding Confirmations
- Get Balances
- Create Virtual Account
- Get Transaction History

### 📈 Investment (Alpaca)
- Get Brokerage Account
- Create Brokerage Account
- Fund Brokerage Account
- Place Order
- Get Orders
- Get Positions
- Cancel Order

### 📊 Portfolio
- Get Portfolio Overview
- Get Weekly Stats
- Get Allocations
- Get Top Movers
- Get Performance

### 📉 Analytics
- Get Performance Metrics
- Get Risk Metrics
- Get Diversification Analysis
- Take Snapshot

### 💹 Market Data
- Get Quote
- Get Quotes
- Get Bars
- Create Alert
- Get Alerts

### 🔄 Scheduled Investments (DCA)
- Create Scheduled Investment
- Get Scheduled Investments
- Update Scheduled Investment
- Pause Scheduled Investment
- Resume Scheduled Investment

### ⚖️ Portfolio Rebalancing
- Create Rebalancing Config
- Get Rebalancing Configs
- Generate Rebalancing Plan
- Execute Rebalancing
- Check Drift

### 👥 Copy Trading
- List Conductors
- Get Conductor
- Create Draft (Follow)
- List User Drafts
- Pause Draft
- Resume Draft

### 🪙 Roundups
- Get Settings
- Update Settings
- Get Summary
- Get Transactions
- Process Transaction

### 🤖 AI Chat
- Chat
- Get Wrapped
- Quick Insight
- Get Suggested Questions

### 📰 News
- Get Feed
- Get Weekly News
- Mark as Read
- Get Unread Count

### 🔔 Webhooks
- Chain Deposit Webhook
- Brokerage Fill Webhook
- Alpaca Trade Update
- Alpaca Account Update

### ⚙️ Admin
- Create Wallets for User
- Retry Wallet Provisioning
- Health Check

## 🔄 Keeping Collection Updated

The collection is auto-generated from the codebase. When new endpoints are added:

1. **Add endpoint metadata** (optional but recommended):
   ```bash
   vim scripts/postman_generator/endpoint_metadata.json
   ```

2. **Regenerate collection**:
   ```bash
   make postman-collection
   ```

3. **Re-import to Postman**:
   - Postman will detect changes and ask to replace
   - Click **Replace** to update

## 🎯 Testing Tips

### Use Collection Runner

Test multiple endpoints in sequence:

1. Select collection or folder
2. Click **Run** button
3. Configure iterations and delay
4. Click **Run [Collection Name]**

### Environment Switching

Create multiple environments for different stages:

- **Local**: `http://localhost:8080`
- **Staging**: `https://staging-api.rail.app`
- **Production**: `https://api.rail.app`

### Pre-request Scripts

The collection includes scripts that automatically:
- Save tokens after login
- Set user_id after registration
- Add timestamps to requests

### Tests

Each request includes basic tests:
- Status code validation
- Response structure validation
- Variable extraction

## 🐛 Troubleshooting

### 401 Unauthorized

- Check `access_token` is set in collection variables
- Token may have expired - login again
- Verify Bearer token format in Authorization header

### 404 Not Found

- Verify `base_url` is correct
- Check service is running: `make run`
- Endpoint may not exist - regenerate collection

### Connection Refused

- Start the service: `make run`
- Check port 8080 is not in use
- Verify Docker containers are running: `docker-compose ps`

### Missing Endpoints

- Regenerate collection: `make postman-collection`
- Check endpoint exists in `internal/api/routes/`
- Verify route is registered in `routes.go`

## 📚 Additional Resources

- [API Documentation](./architecture.md)
- [Authentication Guide](./stories/1-1-user-registration-flow.md)
- [Postman Generator README](../scripts/postman_generator/README.md)

## 🤝 Contributing

To improve the collection:

1. Add detailed metadata to `endpoint_metadata.json`
2. Update extraction patterns in `endpoint_extractor.py`
3. Enhance grouping logic in `collection_builder.py`
4. Submit PR with improvements

## 📝 Notes

- Collection is auto-generated - don't edit the JSON directly
- Always regenerate after pulling new code
- Use environment variables for sensitive data
- Test in local environment before staging/production

---

**Happy Testing! 🚀**
