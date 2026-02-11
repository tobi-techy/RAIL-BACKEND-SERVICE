# RAIL Backend API Map

## Complete API Endpoint Documentation for Bruno Testing

### Base URL
- **Local**: `http://localhost:8080`
- **Staging**: `https://staging-api.rail.app`
- **Production**: `https://api.rail.app`

---

## 1. Health & System Endpoints (No Auth Required)

### 1.1 Core Health Check
- `GET /health` - Application health status
- `GET /ready` - Readiness check (DB, dependencies)
- `GET /live` - Liveness probe
- `GET /version` - Application version
- `GET /metrics` - Prometheus metrics
- `GET /swagger/*any` - Swagger documentation (dev only)

---

## 2. Authentication Endpoints

### 2.1 User Registration & Auth
- `POST /api/v1/auth/register` - Register new user
- `POST /api/v1/auth/login` - User login
- `POST /api/v1/auth/verify` - Verify email/phone
- `POST /api/v1/auth/refresh` - Refresh access token
- `POST /api/v1/auth/logout` - Logout user
- `POST /api/v1/auth/resend-code` - Resend verification code
- `POST /api/v1/auth/forgot-password` - Request password reset
- `POST /api/v1/auth/reset-password` - Reset password

### 2.2 Social Authentication
- `POST /api/v1/auth/social/url` - Get social auth URL
- `POST /api/v1/auth/social/login` - Social login callback
- `POST /api/v1/auth/webauthn/login/begin` - Begin WebAuthn login

---

## 3. User Management Endpoints

### 3.1 User Profile
- `GET /api/v1/users/me` - Get current user profile
- `PUT /api/v1/users/me` - Update user profile
- `POST /api/v1/users/me/change-password` - Change password
- `DELETE /api/v1/users/me` - Delete account
- `POST /api/v1/users/me/enable-2fa` - Enable 2FA
- `POST /api/v1/users/me/disable-2fa` - Disable 2FA

### 3.2 KYC (Know Your Customer)
- `GET /api/v1/kyc/status` - Get KYC status
- `GET /api/v1/kyc/verification-url` - Get KYC verification URL
- `GET /api/v1/kyc/bridge/link` - Get Bridge KYC link
- `GET /api/v1/kyc/bridge/status` - Get Bridge KYC status
- `POST /api/v1/kyc/callback/:provider_ref` - KYC provider webhook callback

### 3.3 Onboarding
- `POST /api/v1/onboarding/complete` - Complete onboarding
- `POST /api/v1/onboarding/kyc/submit` - Submit KYC documents

---

## 4. Security Endpoints

### 4.1 Passcode Management
- `GET /api/v1/security/passcode` - Get passcode status
- `POST /api/v1/security/passcode` - Create passcode
- `PUT /api/v1/security/passcode` - Update passcode
- `POST /api/v1/security/passcode/verify` - Verify passcode
- `DELETE /api/v1/security/passcode` - Remove passcode

### 4.2 Social Accounts & Passkeys
- `GET /api/v1/security/social-accounts` - Get linked accounts
- `POST /api/v1/security/social-accounts/link` - Link social account
- `DELETE /api/v1/security/social-accounts/:provider` - Unlink provider
- `GET /api/v1/security/passkeys` - Get WebAuthn credentials
- `POST /api/v1/security/passkeys/register` - Register passkey
- `DELETE /api/v1/security/passkeys/:id` - Delete passkey

### 4.3 Device Management
- `GET /api/v1/security/devices` - Get trusted devices
- `POST /api/v1/security/devices/:id/trust` - Trust device
- `DELETE /api/v1/security/devices/:id` - Revoke device

### 4.4 IP Whitelist
- `GET /api/v1/security/ip-whitelist` - Get IP whitelist
- `POST /api/v1/security/ip-whitelist` - Add IP to whitelist
- `POST /api/v1/security/ip-whitelist/:id/verify` - Verify whitelisted IP
- `DELETE /api/v1/security/ip-whitelist/:id` - Remove IP

### 4.5 MFA Management
- `GET /api/v1/security/mfa` - Get MFA settings
- `POST /api/v1/security/mfa/sms` - Setup SMS MFA
- `POST /api/v1/security/mfa/send-code` - Send MFA code
- `POST /api/v1/security/mfa/verify` - Verify MFA code
- `GET /api/v1/security/geo-info` - Get geo info

### 4.6 Security Events
- `GET /api/v1/security/events` - Get security events
- `GET /api/v1/security/current-ip` - Get current IP info

### 4.7 Withdrawal Security
- `POST /api/v1/security/withdrawals/confirm` - Confirm withdrawal

---

## 5. Wallet & Funding Endpoints

### 5.1 Funding Operations
- `POST /api/v1/funding/deposit/address` - Create deposit address
- `GET /api/v1/funding/confirmations` - Get funding confirmations
- `POST /api/v1/funding/virtual-account` - Create virtual account
- `GET /api/v1/funding/transactions` - Get transaction history

### 5.2 Balances
- `GET /api/v1/balances` - Get all account balances

### 5.3 Wallet Operations
- `GET /api/v1/wallet/addresses` - Get wallet addresses
- `GET /api/v1/wallet/status` - Get wallet status
- `POST /api/v1/wallets/initiate` - Initiate wallet creation
- `POST /api/v1/wallets/provision` - Provision wallets
- `GET /api/v1/wallets/:chain/address` - Get wallet by chain

---

## 6. Account & Home Screen Endpoints

### 6.1 Station (Home Screen)
- `GET /api/v1/account/station` - Get home screen data (balances, status)

### 6.2 Spending Stash
- `GET /api/v1/account/spending-stash` - Get comprehensive spending data

### 6.3 Investment Stash
- `GET /api/v1/account/investment-stash` - Get comprehensive investment data

---

## 7. Limits Endpoints
- `GET /api/v1/limits` - Get user limits
- `POST /api/v1/limits/validate/deposit` - Validate deposit
- `POST /api/v1/limits/validate/withdrawal` - Validate withdrawal

---

## 8. Investment Endpoints

### 8.1 Baskets
- `GET /api/v1/baskets` - List curated baskets
- `GET /api/v1/baskets/:id` - Get basket details
- `POST /api/v1/baskets/:id/invest` - Invest in basket

### 8.2 Orders
- `POST /api/v1/orders` - Create order
- `GET /api/v1/orders` - List orders
- `GET /api/v1/orders/:id` - Get order details

### 8.3 Assets (Alpaca)
- `GET /api/v1/assets` - List tradable assets
- `GET /api/v1/assets/:symbol_or_id` - Get asset details

---

## 9. Portfolio Endpoints

### 9.1 Portfolio Overview
- `GET /api/v1/portfolio/overview` - Get portfolio summary

### 9.2 Portfolio Analytics (AI Financial Manager)
- `GET /api/v1/portfolio/weekly-stats` - Get weekly statistics
- `GET /api/v1/portfolio/allocations` - Get portfolio allocations
- `GET /api/v1/portfolio/top-movers` - Get top movers
- `GET /api/v1/portfolio/performance` - Get performance metrics

### 9.3 Activity (AI Financial Manager)
- `GET /api/v1/activity/contributions` - Get contributions
- `GET /api/v1/activity/streak` - Get streak data
- `GET /api/v1/activity/timeline` - Get activity timeline

---

## 10. AI & Insights Endpoints

### 10.1 AI Chat (AI Financial Manager)
- `POST /api/v1/ai/chat` - Chat with AI
- `GET /api/v1/ai/wrapped` - Get AI wrapped report
- `GET /api/v1/ai/quick-insight` - Get quick insight
- `GET /api/v1/ai/suggestions` - Get suggested questions

### 10.2 News (AI Financial Manager)
- `GET /api/v1/news/feed` - Get news feed
- `GET /api/v1/news/weekly` - Get weekly news
- `POST /api/v1/news/read` - Mark news as read
- `GET /api/v1/news/unread-count` - Get unread count
- `POST /api/v1/news/refresh` - Refresh news

---

## 11. Mobile Endpoints
- `GET /api/v1/mobile/home` - Get mobile home screen
- `POST /api/v1/mobile/batch` - Batch execute operations
- `POST /api/v1/mobile/sync` - Sync mobile data

---

## 12. Allocation Endpoints (70/30 Smart Mode)
- `POST /api/v1/user/:id/allocation/enable` - Enable allocation mode
- `POST /api/v1/user/:id/allocation/pause` - Pause allocation mode
- `POST /api/v1/user/:id/allocation/resume` - Resume allocation mode
- `GET /api/v1/user/:id/allocation/status` - Get allocation status
- `GET /api/v1/user/:id/allocation/balances` - Get allocation balances

---

## 13. Cards Endpoints
- `GET /api/v1/cards` - List all cards
- `POST /api/v1/cards` - Create new card
- `GET /api/v1/cards/transactions` - Get all card transactions
- `GET /api/v1/cards/:id` - Get card details
- `POST /api/v1/cards/:id/freeze` - Freeze card
- `POST /api/v1/cards/:id/unfreeze` - Unfreeze card
- `GET /api/v1/cards/:id/transactions` - Get card transactions

---

## 14. Roundups Endpoints
- `GET /api/v1/roundups/settings` - Get roundup settings
- `PUT /api/v1/roundups/settings` - Update roundup settings
- `GET /api/v1/roundups/summary` - Get roundup summary
- `GET /api/v1/roundups/transactions` - Get roundup transactions
- `POST /api/v1/roundups/transactions` - Process transaction
- `POST /api/v1/roundups/preview` - Calculate preview
- `POST /api/v1/roundups/collect` - Collect roundups

---

## 15. Copy Trading Endpoints

### 15.1 Conductors
- `GET /api/v1/copy/conductors` - List conductors
- `GET /api/v1/copy/conductors/:id` - Get conductor details
- `GET /api/v1/copy/conductors/:id/signals` - Get conductor signals

### 15.2 Drafts (Copy Relationships)
- `GET /api/v1/copy/drafts` - List user drafts
- `POST /api/v1/copy/drafts` - Create draft
- `GET /api/v1/copy/drafts/:id` - Get draft details
- `DELETE /api/v1/copy/drafts/:id` - Unlink draft
- `POST /api/v1/copy/drafts/:id/pause` - Pause draft
- `POST /api/v1/copy/drafts/:id/resume` - Resume draft
- `PUT /api/v1/copy/drafts/:id/resize` - Resize draft
- `GET /api/v1/copy/drafts/:id/history` - Get draft history

---

## 16. Advanced Features Endpoints

### 16.1 Analytics
- `GET /api/v1/analytics/dashboard` - Get analytics dashboard
- `GET /api/v1/analytics/performance` - Get performance metrics
- `GET /api/v1/analytics/risk` - Get risk metrics
- `GET /api/v1/analytics/diversification` - Get diversification analysis
- `GET /api/v1/analytics/history` - Get portfolio history
- `POST /api/v1/analytics/snapshot` - Take portfolio snapshot

### 16.2 Market Data
- `GET /api/v1/market/quote/:symbol` - Get quote for symbol
- `GET /api/v1/market/quotes` - Get multiple quotes
- `GET /api/v1/market/bars/:symbol` - Get historical bars
- `POST /api/v1/market/alerts` - Create price alert
- `GET /api/v1/market/alerts` - Get price alerts
- `DELETE /api/v1/market/alerts/:id` - Delete alert

### 16.3 Scheduled Investments
- `POST /api/v1/scheduled-investments` - Create scheduled investment
- `GET /api/v1/scheduled-investments` - List scheduled investments
- `GET /api/v1/scheduled-investments/:id` - Get details
- `PATCH /api/v1/scheduled-investments/:id` - Update
- `DELETE /api/v1/scheduled-investments/:id` - Cancel
- `POST /api/v1/scheduled-investments/:id/pause` - Pause
- `POST /api/v1/scheduled-investments/:id/resume` - Resume
- `GET /api/v1/scheduled-investments/:id/executions` - Get execution history

### 16.4 Rebalancing
- `POST /api/v1/rebalancing/configs` - Create rebalancing config
- `GET /api/v1/rebalancing/configs` - List configs
- `GET /api/v1/rebalancing/configs/:id` - Get config
- `PATCH /api/v1/rebalancing/configs/:id` - Update config
- `DELETE /api/v1/rebalancing/configs/:id` - Delete config
- `GET /api/v1/rebalancing/configs/:id/plan` - Generate rebalancing plan
- `POST /api/v1/rebalancing/configs/:id/execute` - Execute rebalancing
- `GET /api/v1/rebalancing/configs/:id/drift` - Check portfolio drift

---

## 17. Webhook Endpoints (No Auth Required)

### 17.1 Chain Deposits
- `POST /api/v1/webhooks/chain-deposit` - Blockchain deposit webhook
- `POST /api/v1/webhooks/brokerage-fill` - Brokerage fill webhook

### 17.2 Bridge Webhooks
- `POST /api/v1/webhooks/bridge` - Bridge payment webhook

---

## 18. Admin Endpoints (Admin Auth Required)

### 18.1 Wallet Admin
- `POST /api/v1/admin/wallet/create` - Create wallets for user
- `POST /api/v1/admin/wallet/retry-provisioning` - Retry wallet provisioning
- `GET /api/v1/admin/wallet/health` - Wallet health check

### 18.2 Security Admin
- `GET /api/v1/admin/security/dashboard` - Security dashboard
- `GET /api/v1/admin/security/incidents` - List incidents
- `GET /api/v1/admin/security/incidents/:id` - Get incident details
- `PUT /api/v1/admin/security/incidents/:id/status` - Update status
- `POST /api/v1/admin/security/incidents/:id/playbook` - Execute playbook
- `GET /api/v1/admin/security/blocked-countries` - Get blocked countries
- `POST /api/v1/admin/security/blocked-countries` - Block country
- `DELETE /api/v1/admin/security/blocked-countries/:country_code` - Unblock

### 18.3 Admin User Management
- `GET /api/v1/admin/applications` - List pending conductor applications
- `POST /api/v1/admin/applications/:id/review` - Review application

---

## Authentication Requirements

### No Auth Required
- Health endpoints
- Auth endpoints (except protected operations)
- Webhook endpoints
- Market data (public quotes)

### Session Auth Required
- Most user endpoints
- Portfolio, wallet, investment endpoints
- Card, roundup, copy trading endpoints

### Admin Auth Required
- All `/admin/*` endpoints

### Webhook Auth
- Verified via signature/secret (not session-based)

---

## Rate Limiting Notes

### Auth Endpoints
- Strict rate limiting (5 attempts/minute)
- Login protection enabled

### General API
- Tiered rate limiting based on user tier
- Distributed rate limiting via Redis

### Webhooks
- IP whitelisting
- Replay protection
- Signature verification

---

## Environment Variables for Testing

```bash
# Required for local testing
RAIL_ENV=development
SERVER_PORT=8080
DATABASE_URL=postgresql://...
REDIS_URL=redis://...
JWT_SECRET=your-jwt-secret
WEBHOOK_SECRET=your-webhook-secret

# Optional services
BRIDGE_API_KEY=...
ALPACA_API_KEY=...
```

---

## Quick Test Checklist

### Phase 1: Authentication
- [ ] Register new user
- [ ] Login and get tokens
- [ ] Refresh token
- [ ] Get profile
- [ ] Logout

### Phase 2: Onboarding
- [ ] Complete onboarding
- [ ] Submit KYC
- [ ] Check KYC status

### Phase 3: Core Features
- [ ] Get balances
- [ ] Create deposit address
- [ ] Get wallet addresses
- [ ] Get portfolio overview
- [ ] List baskets
- [ ] Invest in basket

### Phase 4: Advanced Features
- [ ] Get analytics
- [ ] Create price alert
- [ ] Setup scheduled investment
- [ ] Configure rebalancing

### Phase 5: Additional Features
- [ ] Create card
- [ ] Configure roundups
- [ ] Explore copy trading
- [ ] AI chat interaction
- [ ] News feed

### Phase 6: Security
- [ ] Create passcode
- [ ] Setup 2FA
- [ ] Add device
- [ ] Manage IP whitelist

---
