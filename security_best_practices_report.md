# Security Best Practices Review Report

Date: 2026-02-10
Repository: `/Users/tobi/Development/RAIL_BACKEND`

## Executive Summary
I reviewed the Go backend with a secure-by-default focus (auth/session controls, webhook verification, logging hygiene, and abuse controls). I found **7 security issues**:
- **1 Critical**
- **4 High**
- **2 Medium**

The most urgent issue is a context type mismatch for `user_id` that can cause protected middleware checks (MFA/IP/fraud/user-rate-limit) to silently skip enforcement.

## Critical Findings

### F-001
- Rule ID: `GO-AUTH-CONTEXT-001`
- Severity: **Critical**
- Location: `internal/api/middleware/middleware.go:308`, `internal/api/middleware/enhanced_auth.go:116`, `internal/api/middleware/device_validation.go:105`, `internal/api/middleware/security_middleware.go:17`, `internal/api/middleware/comprehensive_security.go:30`, `internal/api/middleware/rate_limiting.go:37`
- Evidence:
  - Auth middleware stores `uuid.UUID` in context: `c.Set("user_id", claims.UserID)`.
  - Multiple security middlewares read `c.GetString("user_id")` and skip if empty or parse fails.
  - In Gin, `GetString` returns `""` for non-string values.
- Impact: **Security controls can fail open** for authenticated requests, bypassing MFA/IP whitelist/fraud checks and weakening user-level rate limiting.
- Fix:
  - Normalize context identity type globally (prefer `uuid.UUID` with typed helper, or always store string UUID).
  - Replace direct `GetString("user_id")` in security-sensitive code with a single typed helper (e.g., `common.GetUserID(c)` from `internal/api/handlers/common/common.go:15`).
  - For protected routes, fail closed if `user_id` is missing/invalid.
- Mitigation:
  - Add middleware unit tests that set `user_id` as `uuid.UUID` and assert enforcement paths execute (not skip).
- False positive notes:
  - This is deterministic from current code path; not environment-dependent.

## High Findings

### F-002
- Rule ID: `GO-AUTH-SESSION-002`
- Severity: **High**
- Location: `internal/api/handlers/auth/auth_handlers.go:840`, `internal/api/handlers/auth/auth_handlers.go:870`, `internal/domain/services/session/service.go:64`, `internal/domain/services/session/service.go:145`
- Evidence:
  - Refresh flow validates JWT signature/claims then issues new access token, returning the same refresh token.
  - It does **not** validate refresh token against active session state (`refresh_token_hash`) or session revocation.
  - Session service stores `refresh_token_hash` and supports invalidation, but refresh path does not consult it.
- Impact: Stolen refresh tokens can remain usable until expiry even after session invalidation/logout workflows.
- Fix:
  - Bind refresh to session store: lookup by `refresh_token_hash`, require active/unexpired session, reject revoked sessions.
  - Implement rotation (one-time-use refresh) and revoke old refresh token on each successful refresh.
  - Prefer existing enhanced JWT rotation path (`pkg/auth/enhanced_jwt.go:136`).
- Mitigation:
  - Shorten refresh TTL while migrating; alert on refresh reuse.
- False positive notes:
  - If a gateway enforces rotation externally, verify and document it. No evidence in app code.

### F-003
- Rule ID: `GO-CONFIG-001`
- Severity: **High**
- Location: `internal/infrastructure/adapters/bridge/client.go:101`, `internal/infrastructure/adapters/bridge/types.go:116`, `internal/api/handlers/webhooks/alpaca_webhook_handlers.go:189`, `internal/api/handlers/webhooks/alpaca_webhook_handlers.go:217`
- Evidence:
  - Full Bridge customer request is JSON-marshaled and logged.
  - Request fields include identity/KYC/PII (`first_name`, `last_name`, `email`, `phone`, `residential_address`, `identifying_information`).
  - Alpaca webhook handlers log full raw webhook bodies.
- Impact: Sensitive personal/financial data can leak into logs and downstream observability systems.
- Fix:
  - Remove full-body/request logging for customer creation and webhook payloads.
  - Log only non-sensitive metadata (event IDs, provider, status, correlation/request IDs).
  - Add centralized log-redaction policy for known sensitive keys.
- Mitigation:
  - Reduce retention and access to existing logs; rotate credentials/tokens if any were logged.
- False positive notes:
  - Even in non-prod, this is high-risk if logs are shared/centralized.

### F-004
- Rule ID: `GO-OAUTH-STATE-001`
- Severity: **High**
- Location: `internal/api/handlers/auth/social_auth_handlers.go:57`, `internal/domain/entities/social_auth_entities.go:34`, `internal/domain/services/socialauth/service.go:92`
- Evidence:
  - OAuth state is generated and returned by auth URL endpoint.
  - `SocialLoginRequest` does not carry state, and auth flow does not verify state before accepting social login.
- Impact: Missing CSRF binding for OAuth callback flow can enable login CSRF/account confusion attacks.
- Fix:
  - Persist generated state server-side (short TTL, single-use, bound to client/session nonce).
  - Require state in login callback request and validate strictly before token exchange.
  - Invalidate state after use.
- Mitigation:
  - If mobile/native flow is intended, document explicit PKCE/state alternatives and enforce them.
- False positive notes:
  - If state verification is entirely frontend-only, backend still should verify to be secure-by-default.

### F-005
- Rule ID: `GO-WEBHOOK-REPLAY-001`
- Severity: **High**
- Location: `internal/api/routes/routes.go:602`, `internal/api/middleware/webhook_security.go:215`, `internal/api/handlers/wallet/wallet_funding_handlers.go:1826`
- Evidence:
  - Active `/api/v1/webhooks` group uses `WebhookSecurityWithRedisV8`.
  - The middleware explicitly states v8 path omits full replay protection.
  - Handler-level signature check validates HMAC only; no timestamp/nonce replay window enforcement.
- Impact: Captured valid webhook payloads can be replayed, potentially re-triggering funding-related side effects where idempotency is incomplete.
- Fix:
  - Add replay protection on v8 path (event-id/nonce + timestamp window + Redis dedupe keys).
  - Reject stale webhook timestamps and repeated event IDs.
  - Keep per-provider signature verification plus replay checks.
- Mitigation:
  - Ensure strict idempotency keys on all webhook processing paths until replay checks are added.
- False positive notes:
  - Some event types may already be idempotent, but replay defense should be explicit and universal.

## Medium Findings

### F-006
- Rule ID: `GO-CRYPTO-PASSWORD-001`
- Severity: **Medium**
- Location: `internal/infrastructure/config/config.go:592`, `pkg/crypto/crypto.go:19`
- Evidence:
  - Security config sets `security.bcrypt_cost = 12`.
  - Password hashing function uses `bcrypt.DefaultCost` instead of configured cost.
- Impact: Intended stronger password hashing policy is not enforced consistently.
- Fix:
  - Thread configured bcrypt cost into hashing service and use it in all password-hash creation paths.
  - Add startup validation to reject out-of-policy bcrypt cost in production.
- Mitigation:
  - Rehash on next successful login when stored hash cost < configured target.
- False positive notes:
  - None.

### F-007
- Rule ID: `GO-ABUSE-RATELIMIT-001`
- Severity: **Medium**
- Location: `internal/infrastructure/config/config.go:669`, `configs/config.yaml:27`, `pkg/ratelimit/distributed_middleware.go:59`
- Evidence:
  - Defaults set `rate_limit.fail_open: true`.
  - Middleware allows requests when limiter errors occur if fail-open is enabled.
- Impact: During Redis/rate-limiter outages, abuse controls are bypassed for sensitive endpoints.
- Fix:
  - Set production default to fail-closed for auth, credential, and money-movement endpoints.
  - Use endpoint-tier policy: strict fail-closed on high-risk paths, optionally fail-open on low-risk reads.
- Mitigation:
  - Add alerts on limiter failures and fallback-mode activation.
- False positive notes:
  - If this profile is strictly dev-only, enforce environment-specific override guards.

## Secure-by-Default Improvement Plan (Recommended Order)
1. Fix context identity type consistency and fail-closed semantics for protected middleware (`F-001`).
2. Enforce refresh token rotation + session-bound validation (`F-002`).
3. Remove/redact sensitive logs (`F-003`).
4. Add server-side OAuth state verification (`F-004`).
5. Implement webhook replay protection in v8 middleware path (`F-005`).
6. Wire bcrypt cost config into hashing logic (`F-006`).
7. Introduce risk-tiered fail-open policy for rate limiting (`F-007`).

## Notes
- I did not modify application code in this pass; this is an audit/report-only review.
- I did not find evidence that `.env` is tracked in git in the current repo state.
