# Security Best Practices Report

Date: 2026-02-17
Scope: `RAIL_BACKEND` Go backend (API handlers, middleware, security services, dependencies)
Mode: Active security audit + targeted remediation

## Executive Summary
A focused white-hat security audit identified 4 priority findings. Two code-level vulnerabilities were fixed in this pass (critical webhook verification fail-open path and breached-password check false negatives). Two issues remain open (dependency CVEs in runtime/toolchain and plaintext geo-IP transport risk).

## Critical Findings

### CRIT-001 (Fixed): Unified funding webhook allowed fail-open verification and bypassed Circle signature validation
- Impact: An attacker could submit forged funding webhooks to `/api/v1/webhooks/funding` when provider secrets were unset/misconfigured, potentially triggering unauthorized balance-affecting flows.
- Evidence:
  - Previous behavior was fail-open when secret missing in `internal/api/handlers/webhooks/unified_funding_webhook.go` (old logic at `verifySignature`).
  - Current fixed verification path: `internal/api/handlers/webhooks/unified_funding_webhook.go:153`
  - Circle-specific strict checks (`X-Circle-Key-Id`, `X-Circle-Signature`, API key presence) at `internal/api/handlers/webhooks/unified_funding_webhook.go:165`
  - Environment-bound bypass only in development wired via DI at `internal/infrastructure/di/container.go:1120`
- Remediation implemented:
  - Switched to provider-native verification delegates (Bridge/Alpaca handlers, Circle ECDSA verification).
  - Enforced fail-closed behavior in non-development environments.
  - Added body restoration before Alpaca downstream handlers (`internal/api/handlers/webhooks/unified_funding_webhook.go:342`).

## High Findings

### HIGH-001 (Fixed): Breached-password check could miss leaked passwords due to partial HIBP response reads
- Impact: Weak/leaked passwords could be accepted if the target suffix appeared outside the first 64KB of the HIBP response.
- Evidence:
  - Fixed in `internal/domain/services/security/password_policy.go:159`
- Remediation implemented:
  - Replaced single-buffer read with full-body read (`io.ReadAll`) to avoid false negatives.

### HIGH-002 (Open): Runtime/dependency vulnerabilities detected by `govulncheck`
- Impact: Multiple known DoS/TLS/x509 issues in currently resolved Go stdlib/toolchain and one reachable `quic-go` vulnerability increase exploitability surface.
- Evidence:
  - `go.mod` toolchain baseline: `go.mod:3`
  - Vulnerable `quic-go`: `go.mod:130` (found `v0.54.0`, fixed `v0.57.0`)
  - `govulncheck` reported 14 reachable vulnerabilities (examples: GO-2026-4341, GO-2026-4340, GO-2026-4337, GO-2025-4233).
- Recommended remediation:
  - Upgrade Go toolchain/runtime to patched version line (at least `go1.25.7` per `govulncheck` findings).
  - Upgrade `github.com/quic-go/quic-go` to `v0.57.0` or newer.
  - Re-run `govulncheck ./...` and CI tests post-upgrade.

## Medium Findings

### MED-001 (Open): Geo-security fallback uses plaintext HTTP endpoint
- Impact: Geo-risk decisions can be influenced by network-layer tampering when fallback provider is used, reducing trust in risk scoring and country checks.
- Evidence:
  - Plain HTTP usage at `internal/domain/services/security/geo_security.go:184`.
- Recommended remediation:
  - Move fallback provider to HTTPS-capable endpoint, or disable fallback in production when only plaintext provider is available.

## Validation Performed
- `govulncheck ./...` (reachable vuln scan)
- `go test ./internal/api/handlers/webhooks`
- `go test ./internal/domain/services/security`
- `go test ./...` (fails in existing integration test unrelated to this patch: panic in `test/integration/virtual_account_integration_test.go`)

## Files Changed In This Remediation
- `internal/api/handlers/webhooks/unified_funding_webhook.go`
- `internal/infrastructure/di/container.go`
- `internal/domain/services/security/password_policy.go`
