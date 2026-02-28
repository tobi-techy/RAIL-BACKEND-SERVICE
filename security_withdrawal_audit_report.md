# Withdrawal Security Audit Report

## Executive Summary
Focused audit of withdrawal endpoints and execution flow identified multiple exploitable weaknesses in limit enforcement, concurrency handling, and sensitive-token protection. High-impact fixes were applied directly in request handlers, withdrawal service logic, and token hashing.

## Critical / High

### F-001: Missing enforcement of per-account daily amount cap on withdrawal endpoints
- Severity: High
- Location: `internal/api/handlers/wallet/withdrawal_handlers.go`
- Evidence: The middleware set `withdrawal_security_config`/`withdrawal_security_store`, but handlers did not call `ValidateWithdrawalAmount`, so only request-count checks applied.
- Impact: Attackers on new accounts could submit fewer large withdrawals and bypass intended amount-based caps.
- Fix: Enforced policy checks in both `InitiateCryptoWithdrawal` and `InitiateFiatWithdrawal` using middleware-configured store/config before service execution.
- Status: Fixed

### F-002: Race window on concurrent withdrawal initiation could oversubscribe available balance
- Severity: High
- Location: `internal/domain/services/withdrawal/service.go`, `internal/infrastructure/repositories/withdrawal_repository.go`
- Evidence: Balance check executed before transfer initiation without including pending withdrawals; concurrent requests could pass checks before ledger settlement.
- Impact: Multiple near-simultaneous withdrawals could exceed intended available balance controls.
- Fix:
  - Added per-user lock striping inside withdrawal service to serialize initiation in-process.
  - Added pending-withdrawal capacity checks before external transfer execution.
  - Added post-create pending exposure re-check to fail unsafe concurrent requests before provider transfer.
  - Updated pending total query to include `fee_amount` in reserved exposure.
- Status: Fixed (single-instance robust, multi-instance residual risk remains; see Residual Risks)

## Medium

### F-003: Insecure token hashing for withdrawal confirmations
- Severity: Medium
- Location: `internal/domain/services/security/withdrawal_security.go`
- Evidence: `hashToken` copied token bytes into a fixed buffer rather than applying cryptographic hashing.
- Impact: Stored token hash values were not cryptographically derived, weakening security assumptions for persisted confirmation records.
- Fix: Replaced with SHA-256 hashing.
- Status: Fixed

### F-004: Sensitive routing number logged in fiat withdrawal initiation
- Severity: Medium
- Location: `internal/domain/services/withdrawal/service.go`
- Evidence: Fiat initiation log included raw `routing_number`.
- Impact: Bank routing data could leak to logs/SIEM and broaden blast radius in log compromise.
- Fix: Removed raw routing number from logs.
- Status: Fixed

## Residual Risks / Follow-up

### R-001: Concurrency protection is not distributed across multiple app instances
- Severity: Medium
- Location: `internal/domain/services/withdrawal/service.go`
- Notes: Current lock striping is process-local. In horizontally scaled deployments, concurrent requests routed to different instances still rely primarily on pending-check timing.
- Recommendation: Add distributed locking (Redis/Postgres advisory lock) or atomic DB reservation transaction around withdrawal creation.

### R-002: Withdrawal confirmation/security service is not wired into initiation flow
- Severity: Medium
- Location: `internal/domain/services/security/withdrawal_security.go`, `internal/api/handlers/security/security_handlers.go`, `internal/domain/services/withdrawal/service.go`
- Notes: Confirmation/risk assessment methods exist, and `/api/v1/security/withdrawals/confirm` exists, but initiation flow does not create or require confirmations.
- Recommendation: Integrate `AssessWithdrawalRisk` + `CreateConfirmation` into initiation path, set status to `awaiting_confirmation` when required, and only execute provider transfer after confirmation.
