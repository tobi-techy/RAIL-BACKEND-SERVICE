# Story 2.3: alpaca-account-funding

Status: ready-for-dev

# Requirements Context Summary

## Epic Context

Epic 2: Stablecoin Funding Flow - Enable users to fund their brokerage accounts instantly with stablecoins using Due for off-ramp/on-ramp functionality.

This story (2-3) focuses on the Alpaca account funding component of the funding flow.

## Derived Story Statement

As a user,

I want the USD from my stablecoin off-ramp to be securely transferred to my linked Alpaca brokerage account,

so that I can invest in stocks and options with instant buying power.

## Extracted Requirements

From PRD (Product Requirements Document):

- Orchestrate an immediate USDC-to-USD off-ramp via Due API after deposit.

- Transfer off-ramped USD directly into the user's linked Alpaca brokerage account to create "buying power" for instant trading.

From Architecture Document:

- Data flow: User deposits USDC -> Funding Service monitors -> Due Off-Ramp -> Virtual Account -> Alpaca Deposit (USD) -> "Buying Power" updated.

- Use asynchronous orchestration (Sagas) for multi-step funding flow.

- Integration patterns: Adapter pattern for Alpaca API, Circuit breaker for reliability.

- Architecture components: Funding Service Module in Go modular monolith.

From Epics.md:

- Story 2-3: alpaca-account-funding

- Success Criteria: Users can fund brokerage account within minutes, end-to-end success rate >99%.

## Architecture Constraints

- Backend: Go 1.21.x, Gin web framework.

- External integrations: Alpaca API for brokerage funding.

- Asynchronous processing via SQS.

- Error handling: Retry policy, circuit breaker (gobreaker).

- Testing: Unit tests with mocking, integration tests with testcontainers.

- Data models: deposits table for status tracking, balances table for buying_power_usd.

## Structure Alignment Summary

### Carry-Overs from Previous Story (2-2 Due Off-Ramp Integration)

The previous story (2-2) provides critical context for Alpaca funding implementation:

- **API Integration Best Practices**: Thoroughly review Alpaca API documentation to avoid integration errors, implement complete error handling and response validation.

- **Asynchronous Processing**: Use webhook/event-driven patterns for Alpaca funding completion monitoring.

- **Error Handling Patterns**: Implement retry logic with exponential backoff, circuit breaker for Alpaca API calls.

- **Audit Trail**: Maintain complete transaction tracking in deposits table with funding status updates.

These lessons directly inform the Alpaca funding implementation to ensure reliable brokerage account funding.

### Lessons Learned

- Follow established patterns from Due integration for consistent API adapter implementation.

- Ensure webhook handling for Alpaca funding completion events.

- Implement comprehensive error recovery for brokerage funding failures.

- Maintain transaction audit trail for compliance and debugging.

### Project Structure Alignment

No unified-project-structure.md found in docs/. Following the established Go modular monolith pattern from architecture.md:

- Funding Service Module: internal/core/funding/

- Alpaca Adapter: internal/adapters/alpaca/

- Database Persistence: internal/persistence/postgres/

No conflicts detected with existing codebase structure. This story extends the funding service with Alpaca brokerage funding functionality, building on the off-ramp integration from story 2-2.

## Story

As a user,

I want the USD from my stablecoin off-ramp to be securely transferred to my linked Alpaca brokerage account,

so that I can invest in stocks and options with instant buying power.

## Acceptance Criteria

1. Upon completion of USDC-to-USD off-ramp via Due, the system automatically initiates a funding request to Alpaca to deposit the USD into the user's brokerage account. [Source: docs/prd.md#Functional-Requirements, docs/architecture.md#7.2-Funding-Flow]

2. The Alpaca funding process completes successfully, transferring the full USD amount and increasing the user's buying power for trading. [Source: docs/prd.md#Functional-Requirements, docs/epics.md#Epic-2]

3. The user's brokerage balance (buying_power_usd) is updated in real-time following successful Alpaca funding completion. [Source: docs/architecture.md#4.4-balances]

4. Failed Alpaca funding attempts are logged with detailed error information and retried up to 3 times with exponential backoff, with final failures triggering user notification via the app. [Source: docs/architecture.md#11.3-Error-Handling-Patterns]

5. The system maintains a complete audit trail of Alpaca funding transactions, updating deposit status to broker_funded with timestamp. [Source: docs/architecture.md#4.3-deposits]

6. End-to-end funding flow (USDC deposit → off-ramp → Alpaca funding) completes within minutes with >99% success rate. [Source: docs/epics.md#Epic-2]

## Tasks / Subtasks

- [ ] Implement Alpaca brokerage funding initiation after off-ramp completion (AC: 1, 2)
  - [ ] Monitor off-ramp completion via Due webhooks or polling
  - [ ] Create InitiateBrokerFunding method in Alpaca adapter
  - [ ] Add broker_funded status tracking to deposits table (broker_funded_at timestamp)
  - [ ] Implement circuit breaker for Alpaca API calls
  - [ ] Write unit tests for funding initiation
  - [ ] Write integration tests with mocked Alpaca API

- [ ] Update user brokerage balance upon successful funding (AC: 3)
  - [ ] Update balances table buying_power_usd after funding confirmation
  - [ ] Send real-time notification to user via GraphQL subscription or push notification
  - [ ] Write unit tests for balance updates
  - [ ] Write integration tests for balance synchronization

- [ ] Add comprehensive error handling and retry logic (AC: 4)
  - [ ] Implement Alpaca-specific error parsing and mapping to internal error types
  - [ ] Add structured logging for all funding steps using Zap logger
  - [ ] Implement user notification system for failed funding attempts
  - [ ] Add metrics collection for funding success/failure rates
  - [ ] Write tests for error scenarios and retry logic

- [ ] Ensure complete audit trail and compliance tracking (AC: 5, 6)
  - [ ] Update deposit status progression (off_ramp_complete → broker_funded)
  - [ ] Add Alpaca transaction reference IDs to deposits table
  - [ ] Implement end-to-end flow monitoring for success rate tracking
  - [ ] Write database tests for audit trail integrity
  - [ ] Write integration tests for complete funding flow

## Dev Notes

- Relevant architecture patterns and constraints: Asynchronous orchestration (Sagas) for multi-step funding flow, Adapter Pattern for Alpaca API integration, Circuit Breaker for external API resilience, Repository Pattern for database access. [Source: docs/architecture.md#4.4-Architectural-and-Design-Patterns, docs/architecture.md#11.3-Error-Handling-Patterns]

- Source tree components to touch: internal/core/funding/, internal/adapters/alpaca/, internal/persistence/postgres/, internal/api/handlers/ [Source: docs/architecture.md#5.1-Component-List, docs/architecture.md#9-Source-Tree]

- Testing standards summary: Unit tests for all service methods and adapters using Go testing package and testify mocking, integration tests with testcontainers for database and mocked external APIs, end-to-end tests for critical funding flow. [Source: docs/architecture.md#13.2-Test-Types-and-Organization]

### Project Structure Notes

- Alignment with unified project structure (paths, modules, naming): Follows Go modular monolith with clear separation of core business logic, adapters, and persistence layers as defined in architecture.md. [Source: docs/architecture.md#9-Source-Tree]

- Detected conflicts or variances (with rationale): None detected, builds on existing funding service architecture from story 2-2 off-ramp integration.

### References

- [Source: docs/prd.md#Functional-Requirements] - Alpaca brokerage funding requirements

- [Source: docs/architecture.md#7.2-Funding-Flow] - Detailed sequence diagram for funding flow including Alpaca integration

- [Source: docs/architecture.md#4.3-deposits] - Deposit status tracking and audit trail schema

- [Source: docs/architecture.md#4.4-balances] - Balance update requirements for buying power

- [Source: docs/architecture.md#11.3-Error-Handling-Patterns] - Error handling, retry, and circuit breaker patterns for external APIs

- [Source: docs/epics.md#Epic-2] - Epic context, success criteria, and story breakdown

- [Source: docs/architecture.md#5.2-Component-Diagrams] - Funding service module interactions

## Dev Agent Record

### Context Reference

- docs/stories/2-3-alpaca-account-funding.context.md

### Agent Model Used

Amp AI Agent

### Debug Log References

**Implementation Plan:**
1. Extend Funding Service with Alpaca funding orchestration after off-ramp completion
2. Implement Alpaca ACH transfer API integration for brokerage account funding
3. Add real-time balance updates and user notifications
4. Implement comprehensive error handling with retry logic
5. Update database schema for funding audit trail
6. Add monitoring and metrics for funding success rates

**Key Design Decisions:**
- Use Alpaca ACH transfer API for funding brokerage accounts
- Monitor funding completion via Alpaca webhooks or polling
- Circuit breaker pattern for Alpaca API reliability
- Exponential backoff retry (3 attempts max) for failed funding
- Real-time balance updates via GraphQL subscriptions
- Complete audit trail in deposits table with Alpaca reference IDs

### Completion Notes List

### File List

## Change Log

| Date | Version | Description | Author |
|------|---------|-------------|--------|
| 2025-11-06 | 1.0 | Initial draft created by SM workflow | SM Agent |
