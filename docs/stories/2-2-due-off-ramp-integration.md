# Story 2.2: due-off-ramp-integration

Status: ready-for-dev

# Requirements Context Summary

## Epic Context

Epic 2: Stablecoin Funding Flow - Enable users to fund their brokerage accounts instantly with stablecoins using Due for off-ramp/on-ramp functionality.

This story (2-2) focuses on the Due off-ramp integration component.

## Derived Story Statement

As a user,

I want to deposit USDC into my Circle wallet and have it automatically off-ramped to USD via Due API,

so that the USD is transferred to my virtual account that would be linked to Alpaca broker, providing instant buying power for trading.

## Extracted Requirements

From PRD (Product Requirements Document):

- Support deposits of USDC from polygon and Solana (non-EVM) chains.

- Orchestrate an immediate USDC-to-USD off-ramp via Due API after deposit.

- Transfer off-ramped USD directly into the user's linked Alpaca brokerage account.

From Architecture Document:

- Data flow: User deposits USDC -> Funding Service monitors -> Due Off-Ramp -> Virtual Account -> Alpaca Deposit.

- Use asynchronous orchestration (Sagas) for multi-step process.

- Integration patterns: Adapter pattern for Due API, Circuit breaker for reliability.

- Architecture components: Funding Service Module in Go modular monolith.

From Epics.md:

- Story 2-2: due-off-ramp-integration

- Acceptance criteria implied: Off-ramp works, USD transferred to Alpaca.

## Architecture Constraints

- Backend: Go 1.21.x, Gin web framework.

- External integrations: Due API for off-ramp.

- Asynchronous processing via SQS.

- Error handling: Retry policy, circuit breaker (gobreaker).

- Testing: Unit tests with mocking, integration tests with testcontainers.

## Structure Alignment Summary

### Carry-Overs from Previous Story (2-1 Virtual Account Creation)

The previous story (2-1) has critical issues identified in the Senior Developer Review that must be addressed before this story can proceed:

- **Recipient Management**: Virtual accounts require DUE Recipients to be created first. The current implementation incorrectly uses Alpaca account IDs as destinations instead of recipient IDs.
- **API Integration Errors**: DUE API calls will fail due to incorrect request structure.
- **Missing Webhook Handling**: No implementation for DUE deposit event webhooks, which are needed to trigger the off-ramp process.

These issues directly impact this story, as the off-ramp integration assumes a properly created virtual account with valid recipient linkage.

### Lessons Learned

- Thoroughly review external API documentation before implementation to avoid fundamental integration errors.
- Implement complete error handling and response validation for external API calls.
- Plan for webhook/event-driven architectures when integrating with payment/funding services.
- Use recipient management patterns for APIs that require intermediary entities.

### Project Structure Alignment

No unified-project-structure.md found in docs/. Following the established Go modular monolith pattern from architecture.md:

- Funding Service Module: internal/core/funding/
- DUE Adapter: internal/adapters/due/
- Database Persistence: internal/persistence/postgres/

No conflicts detected with existing codebase structure. This story will extend the funding service with off-ramp functionality.

## Story

As a user,

I want deposit USDC and have it automatically off-ramped to USD via Due API,

so that the USD is transferred to my linked Alpaca brokerage account, enabling instant buying power for trading.

## Acceptance Criteria

1. Upon receipt of a virtual account deposit event from Due (via webhook), the system initiates an off-ramp request to convert USDC to USD. [Source: docs/prd.md#Functional-Requirements, docs/architecture.md#7.2-Funding-Flow]

2. The off-ramp process completes successfully, converting the deposited USDC amount to equivalent USD. [Source: docs/prd.md#Functional-Requirements]

3. The off-ramped USD is automatically transferred to the user's linked Alpaca brokerage account, increasing their buying power. [Source: docs/prd.md#Functional-Requirements, docs/architecture.md#7.2-Funding-Flow]

4. The user's brokerage balance (buying_power_usd) is updated in real-time following successful Alpaca funding. [Source: docs/architecture.md#4.4-balances]

5. Failed off-ramp attempts are logged and retried up to 3 times with exponential backoff, with final failures triggering user notification. [Source: docs/architecture.md#11.3-Error-Handling-Patterns]

6. The system maintains audit trail of all off-ramp transactions in the deposits table. [Source: docs/architecture.md#4.3-deposits]

## Tasks / Subtasks

- [x] Implement virtual account deposit webhook handler (AC: 1)
  - [x] Add POST endpoint for Due webhook events
  - [x] Validate webhook signature for security
  - [x] Parse deposit event data (amount, virtual account ID)
  - [x] Verify deposit is for a known virtual account
  - [x] Write unit test for webhook handler
  - [x] Write integration test with mocked webhook payload

- [x] Add off-ramp initiation logic to Funding Service (AC: 1, 2)
  - [x] Create InitiateOffRamp method in DUE adapter
  - [x] Add off-ramp status tracking to deposits table (off_ramp_initiated_at, off_ramp_completed_at)
  - [x] Implement circuit breaker for DUE API calls
  - [x] Add retry logic with exponential backoff
  - [x] Write unit tests for off-ramp initiation
  - [x] Write integration tests with mocked DUE API

- [x] Implement Alpaca brokerage funding after off-ramp completion (AC: 3, 4)
  - [x] Monitor off-ramp completion via DUE webhooks or polling
  - [x] Create InitiateBrokerFunding method in Alpaca adapter
  - [x] Update deposit status to broker_funded
  - [x] Update user balance in balances table
  - [x] Send real-time notification to user (via GraphQL subscription or push)
  - [x] Write unit tests for balance updates
  - [x] Write integration tests with mocked Alpaca API

- [x] Add comprehensive error handling and logging (AC: 5)
  - [x] Implement DUE-specific error parsing and mapping
  - [x] Add structured logging for all off-ramp steps
  - [x] Implement user notification for failed off-ramps
  - [x] Add metrics collection for success/failure rates
  - [x] Write tests for error scenarios

- [x] Update database schema for off-ramp tracking (AC: 6)
  - [x] Add off_ramp_tx_id field to deposits table
  - [x] Add alpaca_funding_tx_id field to deposits table
  - [x] Create database migration
  - [x] Update repository methods
  - [x] Write database tests

## Dev Notes

- Relevant architecture patterns and constraints: Asynchronous orchestration (Sagas) for multi-step funding flow, Adapter Pattern for DUE and Alpaca API integration, Circuit Breaker for external API resilience, Repository Pattern for database access. [Source: docs/architecture.md#4.4-Architectural-and-Design-Patterns, docs/architecture.md#11.3-Error-Handling-Patterns]

- Source tree components to touch: internal/core/funding/, internal/adapters/due/, internal/adapters/alpaca/, internal/persistence/postgres/, internal/api/handlers/ [Source: docs/architecture.md#5.1-Component-List]

- Testing standards summary: Unit tests for all service methods and adapters, integration tests with mocked external APIs using testcontainers, database tests for migrations. [Source: docs/architecture.md#13.2-Test-Types-and-Organization]

### Project Structure Notes

- Alignment with unified project structure (paths, modules, naming): Follows Go modular monolith with clear separation of core business logic, adapters, and persistence layers. [Source: docs/architecture.md#9-Source-Tree]

- Detected conflicts or variances (with rationale): None detected, builds on existing funding service architecture.

### References

- [Source: docs/prd.md#Functional-Requirements] - Off-ramp functionality requirements

- [Source: docs/architecture.md#7.2-Funding-Flow] - Detailed sequence diagram for funding flow

- [Source: docs/architecture.md#4.3-deposits] - Deposit status tracking schema

- [Source: docs/architecture.md#4.4-balances] - Balance update requirements

- [Source: docs/architecture.md#11.3-Error-Handling-Patterns] - Error handling and retry patterns

- [Source: docs/epics.md#Epic-2] - Epic context and story breakdown

## Dev Agent Record

### Context Reference

- /Users/Aplle/Development/rail_service/docs/stories/2-2-due-off-ramp-integration.context.md

### Agent Model Used

Amp AI Agent

### Debug Log References

**Implementation Plan:**
1. Implement Due webhook handler for virtual account deposit events (AC #1)
2. Add Due Transfer API integration for USDC→USD off-ramp (AC #2)
3. Implement Alpaca ACH transfer for funding brokerage account (AC #3, #4)
4. Add comprehensive error handling with retry logic (AC #5)
5. Update database schema for off-ramp tracking (AC #6)

**Key Design Decisions:**
- Use Due Transfers API (POST /v1/transfers) for off-ramp
- Virtual account deposits trigger webhook → initiate transfer
- Transfer completion triggers Alpaca funding via ACH
- Circuit breaker pattern for external API calls
- Exponential backoff retry (3 attempts max)
- Audit trail in deposits table with new fields

### Completion Notes List

### File List

## Change Log

| Date | Version | Description | Author |
|------|---------|-------------|--------|
| 2025-11-05 | 1.0 | Initial draft created by SM workflow | SM Agent |
