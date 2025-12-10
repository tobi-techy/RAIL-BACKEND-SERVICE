# Security Enhancements V2

This document describes the security improvements implemented to address identified vulnerabilities.

## 1. Secrets Management

### Implementation
- **AWS Secrets Manager Provider** (`pkg/secrets/aws_secrets_manager.go`)
  - Secure storage of sensitive credentials
  - Automatic caching with configurable TTL
  - Support for JSON secrets

- **Secret Rotation Service** (`pkg/secrets/rotation.go`)
  - Automated 90-day rotation cycle
  - Audit trail for all rotations
  - Rollback capability for failed rotations

### Configuration
```yaml
security:
  secrets_provider: "aws_secrets_manager"  # or "env" for development
  aws_secrets_region: "us-east-1"
  aws_secrets_prefix: "rail/"
  secret_rotation_days: 90
```

### Usage
```go
// Initialize AWS Secrets Manager provider
provider, err := secrets.NewAWSSecretsManagerProvider(ctx, region, prefix, cacheTTL)

// Get a secret
jwtSecret, err := provider.GetSecret(ctx, "JWT_SECRET")

// Rotate a secret
rotationService := secrets.NewRotationService(provider, db, logger)
err := rotationService.RotateSecret(ctx, "JWT_SECRET", "scheduled", "system")
```

## 2. JWT Token Security

### Implementation
- **Token Blacklist** (`pkg/auth/token_blacklist.go`)
  - Redis-based token revocation
  - Per-token and per-user blacklisting
  - Automatic expiration cleanup

- **Enhanced JWT Service** (`pkg/auth/enhanced_jwt.go`)
  - Short-lived access tokens (15 minutes)
  - Refresh token rotation (one-time use)
  - Token ID (JTI) for revocation tracking

### Configuration
```yaml
jwt:
  access_token_ttl: 900      # 15 minutes
  refresh_token_ttl: 604800  # 7 days

security:
  enable_token_blacklist: true
```

### Token Flow
1. User logs in → receives access token (15 min) + refresh token (7 days)
2. Access token expires → client uses refresh token to get new pair
3. Old refresh token is invalidated (rotation)
4. Logout → both tokens are blacklisted

### API Changes
- `POST /api/v1/auth/logout` - Blacklists current tokens
- `POST /api/v1/auth/logout-all` - Blacklists all user sessions
- `POST /api/v1/auth/refresh` - Rotates refresh token

## 3. Password Storage

### Implementation
- **Enhanced Password Service** (`internal/domain/services/security/password_service.go`)
  - Bcrypt cost increased to 12 (from 10)
  - Password history tracking (last 5 passwords)
  - Password expiration (90 days)

- **Password Policy Service** (`internal/domain/services/security/password_policy.go`)
  - Complexity requirements (3 of 4: upper, lower, digit, special)
  - HaveIBeenPwned breach checking
  - Sequential/repeated character detection

### Configuration
```yaml
security:
  bcrypt_cost: 12
  password_history_count: 5
  password_expiration_days: 90
  check_password_breaches: true
```

### Password Requirements
- Minimum 8 characters
- At least 3 of: uppercase, lowercase, digit, special character
- Not in HaveIBeenPwned database
- Not one of last 5 passwords used
- Must be changed every 90 days

## 4. API Rate Limiting

### Implementation
- **Tiered Rate Limiter** (`pkg/ratelimit/tiered_limiter.go`)
  - Global limits (all requests)
  - Per-IP limits
  - Per-user limits
  - Per-endpoint limits

- **Login Attempt Tracker** (`pkg/ratelimit/tiered_limiter.go`)
  - Exponential backoff for failed attempts
  - CAPTCHA requirement after 3 failures
  - Account lockout after 10 failures

### Configuration
```yaml
server:
  rate_limit_per_min: 100  # Global limit

security:
  max_login_attempts: 10
  lockout_duration: 900    # 15 minutes base
  captcha_threshold: 3
```

### Rate Limit Tiers
| Tier | Limit | Window | Description |
|------|-------|--------|-------------|
| Global | 1000 | 1 min | All requests |
| Per-IP | 100 | 1 min | Per client IP |
| Per-User | 200 | 1 min | Per authenticated user |
| Login | 5 | 15 min | Login attempts |

### Login Protection Flow
1. Attempts 1-3: Normal login
2. Attempts 4+: CAPTCHA required
3. Attempts 10+: Account locked with exponential backoff
4. Successful login: Reset attempt counter

## Database Migrations

Migration `070_create_enhanced_security_tables.up.sql` creates:
- `user_rate_limits` - Per-user rate limit tracking
- `sessions` - Token session management
- `refresh_token_rotations` - Refresh token audit trail
- `secret_rotations` - Secret rotation audit log
- `login_attempts` - Login attempt history

## Middleware Integration

### Enhanced Authentication
```go
// In routes setup
router.Use(middleware.EnhancedAuthentication(cfg, blacklist, logger, sessionService))
```

### Tiered Rate Limiting
```go
limiter := ratelimit.NewTieredLimiter(redis, config, logger)
router.Use(middleware.TieredRateLimiting(limiter, logger))
```

### Login Protection
```go
tracker := ratelimit.NewLoginAttemptTracker(redis, logger)
authRoutes.Use(middleware.LoginRateLimiting(tracker, logger))
```

## Environment Variables

New environment variables for production:
```bash
# Secrets Management
SECRETS_PROVIDER=aws_secrets_manager
AWS_SECRETS_REGION=us-east-1
AWS_SECRETS_PREFIX=rail/

# Enhanced Security
BCRYPT_COST=12
PASSWORD_HISTORY_COUNT=5
PASSWORD_EXPIRATION_DAYS=90
ACCESS_TOKEN_TTL=900
ENABLE_TOKEN_BLACKLIST=true
CHECK_PASSWORD_BREACHES=true
CAPTCHA_THRESHOLD=3
```

## Security Headers

All responses include:
- `X-Content-Type-Options: nosniff`
- `X-Frame-Options: DENY`
- `X-XSS-Protection: 1; mode=block`
- `Strict-Transport-Security: max-age=31536000; includeSubDomains`
- `Content-Security-Policy: default-src 'self'`

## Monitoring & Alerts

### Metrics
- `security_login_attempts_total` - Login attempt counter
- `security_token_revocations_total` - Token revocation counter
- `security_password_changes_total` - Password change counter
- `security_secret_rotations_total` - Secret rotation counter

### Alerts
- Failed login spike (>10 per minute per IP)
- Account lockout events
- Secret rotation failures
- Token blacklist errors

## Testing

Run security-related tests:
```bash
go test ./pkg/auth/... -v
go test ./pkg/ratelimit/... -v
go test ./pkg/secrets/... -v
go test ./internal/domain/services/security/... -v
```

## Rollout Plan

1. **Phase 1**: Deploy with `enable_token_blacklist: false`
2. **Phase 2**: Enable token blacklist, monitor for issues
3. **Phase 3**: Enable password breach checking
4. **Phase 4**: Switch to AWS Secrets Manager
5. **Phase 5**: Enable automatic secret rotation

---

## 7. Multi-Factor Authentication (MFA) Enhancements

### Features
- **Multiple MFA Methods**: TOTP, SMS, WebAuthn/Passkeys
- **Backup Codes**: 8 single-use recovery codes
- **Mandatory MFA**: Enforced for high-value accounts (>$50k)
- **Grace Period**: 7-day setup period after enforcement

### API Endpoints
```
GET  /api/v1/security/mfa           - Get MFA settings
POST /api/v1/security/mfa/sms       - Setup SMS MFA
POST /api/v1/security/mfa/send-code - Send verification code
POST /api/v1/security/mfa/verify    - Verify MFA code
```

## 8. IP Whitelisting & Geo-Security

### Features
- **IP Whitelisting**: Required for high-value accounts
- **Geo-Blocking**: Block access from high-risk countries (KP, IR, SY, CU)
- **VPN/Proxy Detection**: Flag requests from VPNs, proxies, Tor
- **Location Anomaly Detection**: Detect impossible travel

### API Endpoints
```
GET  /api/v1/security/geo-info               - Get current geo info
GET  /api/v1/admin/security/blocked-countries - List blocked countries
POST /api/v1/admin/security/blocked-countries - Block country
```

## 9. Real-time Fraud Detection

### Detection Signals
| Signal | Description | Weight |
|--------|-------------|--------|
| `velocity` | Unusual transaction frequency | 1.0 |
| `amount_anomaly` | Transaction >3x average | 1.2 |
| `device_anomaly` | New or untrusted device | 0.8 |
| `destination_risk` | New withdrawal destination | 1.0 |
| `geo_anomaly` | Unusual location | 1.1 |

### Fraud Score Actions
| Score Range | Action |
|-------------|--------|
| 0.0 - 0.4 | Allow |
| 0.4 - 0.6 | Require MFA |
| 0.6 - 0.8 | Manual Review |
| 0.8 - 1.0 | Block |

## 10. Incident Response System

### Automated Detection
- Credential stuffing attacks (>20 failed logins/hour)
- Account takeover attempts
- Fraudulent transaction patterns

### Automated Response
- IP blocking
- Account locking
- Session revocation
- User notification

### API Endpoints
```
GET  /api/v1/admin/security/dashboard        - Security metrics
GET  /api/v1/admin/security/incidents        - List open incidents
POST /api/v1/admin/security/incidents/:id/playbook - Execute playbook
```

## 11. Secure Communication

### TLS Configuration
- **Minimum Version**: TLS 1.3
- **Perfect Forward Secrecy**: X25519, P-384, P-256 curves
- **HTTP/2**: Enabled by default

### mTLS for Service-to-Service
- Client certificate authentication
- CA-signed certificates required

## Configuration

```yaml
security:
  # MFA settings
  mfa_grace_period_days: 7
  high_value_threshold: 50000
  
  # Geo-security
  enable_geo_blocking: true
  enable_vpn_detection: true
  
  # Fraud detection
  fraud_score_threshold: 0.7
  fraud_review_threshold: 0.5
  
  # Incident response
  incident_alert_webhook: ""
  auto_block_threshold: 20
```

## See Also
- [Incident Response Playbook](./INCIDENT_RESPONSE_PLAYBOOK.md)
