# Security Enhancements

This document describes the security enhancements implemented to protect user funds and data.

## Overview

The following security features have been added to strengthen the platform's security posture:

1. **Login Protection** - Account lockout after failed attempts
2. **Device Tracking** - Fingerprinting and anomaly detection
3. **Withdrawal Security** - Confirmation flow and velocity checks
4. **IP Whitelisting** - Restrict sensitive operations to trusted IPs
5. **Password Policy** - Complexity rules and breach checking
6. **Sensitive Data Masking** - Prevent PII leakage in logs
7. **Security Event Logging** - Comprehensive audit trail
8. **Key Rotation** - Infrastructure for encryption key rotation

---

## 1. Login Protection Service

**Location:** `internal/domain/services/security/login_protection.go`

### Features:
- Tracks failed login attempts per email/identifier
- Locks accounts after 5 failed attempts (configurable)
- 15-minute lockout duration (configurable)
- Automatic cleanup of expired attempt records
- Admin unlock capability

### Usage:
```go
// Check if login is allowed
result, err := loginProtection.CheckLoginAllowed(ctx, email)
if !result.Allowed {
    // Account is locked
}

// Record failed attempt
result, err := loginProtection.RecordFailedAttempt(ctx, email, ip, userAgent)

// Clear on successful login
loginProtection.ClearFailedAttempts(ctx, email)
```

### API Behavior:
- Returns `429 Too Many Requests` when account is locked
- Includes `locked_until` timestamp in response

---

## 2. Device Tracking Service

**Location:** `internal/domain/services/security/device_tracking.go`

### Features:
- Device fingerprinting based on User-Agent, language, screen resolution, timezone
- Tracks known devices per user
- Detects new device logins
- Risk scoring based on device patterns
- Trust/revoke device management

### Risk Factors:
- `new_device` - First time seeing this device (+0.5 risk)
- `ip_changed` - Known device from new IP (+0.2 risk)
- `multiple_devices` - >3 devices in 24 hours (+0.3 risk)

### API Endpoints:
- `GET /api/v1/security/devices` - List user's devices
- `POST /api/v1/security/devices/:id/trust` - Trust a device
- `DELETE /api/v1/security/devices/:id` - Revoke a device

---

## 3. Withdrawal Security Service

**Location:** `internal/domain/services/security/withdrawal_security.go`

### Features:
- Risk assessment for withdrawal requests
- Confirmation flow with secure tokens
- Velocity checks (max 5 withdrawals/day)
- New destination address detection
- Unusual amount detection (>3x average)
- Time-based anomaly detection (unusual hours)

### Risk Assessment:
```go
assessment, err := withdrawalSecurity.AssessWithdrawalRisk(ctx, userID, amount, destAddress)
// Returns:
// - Allowed: bool
// - RequiresMFA: bool
// - RequiresEmail: bool
// - RiskScore: float64
// - RiskFactors: []string
```

### Confirmation Flow:
1. Create confirmation: `CreateConfirmation(ctx, userID, withdrawalID, amount, destAddress)`
2. Send token to user via email
3. User confirms: `VerifyConfirmation(ctx, token, userID)`
4. Process withdrawal

### API Endpoints:
- `POST /api/v1/security/withdrawals/confirm` - Confirm pending withdrawal

---

## 4. IP Whitelist Service

**Location:** `internal/domain/services/security/ip_whitelist.go`

### Features:
- Per-user IP whitelist management
- Verification required before activation
- Redis caching for fast lookups
- Auto-whitelist first login IP (optional)

### API Endpoints:
- `GET /api/v1/security/ip-whitelist` - List whitelisted IPs
- `POST /api/v1/security/ip-whitelist` - Add IP (requires verification)
- `POST /api/v1/security/ip-whitelist/:id/verify` - Verify and activate IP
- `DELETE /api/v1/security/ip-whitelist/:id` - Remove IP
- `GET /api/v1/security/current-ip` - Get client's current IP

### Middleware:
```go
// Apply to sensitive routes
router.Use(middleware.RequireIPWhitelist(ipService, logger))
```

---

## 5. Password Policy Service

**Location:** `internal/domain/services/security/password_policy.go`

### Features:
- Minimum 8 characters, maximum 128
- Complexity requirements (3 of 4: uppercase, lowercase, digit, special)
- Common pattern detection
- Sequential character detection
- Repeated character detection
- HaveIBeenPwned API integration (production only)
- Password strength scoring (0-100)

### Usage:
```go
result, err := passwordPolicy.ValidatePassword(ctx, password)
// Returns:
// - Valid: bool
// - Errors: []string
// - Warnings: []string
// - Strength: int (0-100)
// - IsBreached: bool
// - BreachCount: int
```

---

## 6. Sensitive Data Masking

**Location:** `pkg/security/masking.go`

### Features:
- Email masking: `jo**@g****.com`
- Phone masking: `***-***-1234`
- Card number masking: `************1234`
- SSN masking: `***-**-****`
- JWT masking: `eyJ***REDACTED***`
- API key masking: `sk_l****`
- Wallet address masking: `0x1234...5678`
- Automatic field detection by name

### Usage:
```go
// Mask a string
masked := security.MaskString(sensitiveData)

// Mask a map (for logging)
maskedMap := security.MaskMap(logData)

// Redact headers
redacted := security.RedactHeaders(request.Header)
```

---

## 7. Security Event Logger

**Location:** `internal/domain/services/security/event_logger.go`

### Event Types:
- `login_success` / `login_failed`
- `account_locked`
- `password_changed`
- `mfa_enabled` / `mfa_disabled`
- `new_device`
- `suspicious_activity`
- `withdrawal_request` / `withdrawal_confirmed`
- `ip_whitelist_add` / `ip_whitelist_remove`
- `session_invalidated`
- `api_key_created` / `api_key_revoked`

### Severity Levels:
- `info` - Normal operations
- `warning` - Potential issues
- `critical` - Security incidents

### API Endpoints:
- `GET /api/v1/security/events` - Get user's security events

---

## 8. Key Rotation Service

**Location:** `internal/domain/services/security/key_rotation.go`

### Features:
- Versioned encryption
- Support for multiple active keys
- Re-encryption capability
- 90-day rotation interval (configurable)

### Usage:
```go
// Encrypt with current key
ciphertext, version, err := keyRotation.EncryptWithVersion(plaintext)

// Decrypt with appropriate key version
plaintext, err := keyRotation.DecryptWithVersion(ciphertext, version)

// Re-encrypt with new key
newCiphertext, newVersion, err := keyRotation.ReEncryptData(ctx, oldCiphertext, oldVersion)
```

---

## Database Tables

New tables created by migration `066_create_security_enhancement_tables`:

- `known_devices` - Device fingerprints and trust status
- `ip_whitelist` - User IP whitelists
- `withdrawal_confirmations` - Withdrawal confirmation tokens
- `security_events` - Security audit log
- `password_history` - Password reuse prevention

---

## Middleware

### Available Security Middleware:

```go
// IP whitelist enforcement
middleware.RequireIPWhitelist(ipService, logger)

// MFA requirement
middleware.RequireMFA(twoFAService, logger)

// Device verification
middleware.DeviceVerification(deviceService, logger)

// Login protection
middleware.LoginProtection(loginService, logger)

// Withdrawal security
middleware.WithdrawalSecurity(withdrawalSecurity, logger)
```

---

## Configuration

Add to `configs/config.yaml`:

```yaml
security:
  encryption_key: "your-32-byte-encryption-key"
  max_login_attempts: 5
  lockout_duration: 900  # 15 minutes in seconds
  password_min_length: 8
  require_mfa: false
  session_timeout: 3600
```

---

## Best Practices

1. **Always use MFA for withdrawals** - Enable `RequiresMFA` in withdrawal risk assessment
2. **Monitor security events** - Set up alerts for `critical` severity events
3. **Rotate encryption keys** - Run key rotation every 90 days
4. **Review device list** - Encourage users to review and revoke unknown devices
5. **Enable IP whitelist** - For high-value accounts, require IP whitelisting
6. **Check password breaches** - Enable HIBP checking in production

---

## Future Enhancements

- [ ] HSM integration for key management
- [ ] Behavioral biometrics
- [ ] Geographic anomaly detection
- [ ] Real-time fraud scoring with ML
- [ ] Hardware security key support (beyond WebAuthn)
