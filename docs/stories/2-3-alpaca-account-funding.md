# Story 2.3: Alpaca Account Funding

Status: ready-for-dev

## Story

As a user who has converted USDC to USD in my virtual account,
I want the system to automatically transfer USD from my virtual account to my linked Alpaca brokerage account,
so that I can immediately access the funds for trading stocks and options.

## Acceptance Criteria

1. **Virtual Account Linking**: Users can link their virtual accounts to Alpaca brokerage accounts
2. **Balance Detection**: System detects when virtual account has available USD balance
3. **Alpaca Transfer Initiation**: System creates Alpaca account funding request for available USD
4. **Balance Update**: User's Alpaca buying power is updated upon successful funding
5. **Status Tracking**: Transfer status is tracked from 'broker_funded_initiated' to 'broker_funded_complete'
6. **Error Handling**: Failed Alpaca transfers are retried with exponential backoff and user notifications
7. **Audit Logging**: All Alpaca API interactions are logged with correlation IDs for troubleshooting

## Tasks / Subtasks

- [ ] Implement Alpaca API transfer functionality (AC: #3, #4)
  - [ ] Extend Alpaca API client with account funding methods
  - [ ] Add funding request/response models to Alpaca API adapter
  - [ ] Implement transfer status checking and webhook handling
  - [ ] Add unit tests for funding functionality
- [ ] Enhance Funding Service with Alpaca funding integration (AC: #3)
  - [ ] Add InitiateAlpacaFunding method to FundingService interface
  - [ ] Implement funding logic with brokerage account validation
  - [ ] Update deposit status from 'off_ramp_complete' to 'broker_funded_initiated'
  - [ ] Add integration tests for funding initiation
- [ ] Integrate with virtual account balance monitoring (AC: #2, #3)
  - [ ] Modify Due transfer completion handler to trigger Alpaca funding
  - [ ] Add async processing queue for brokerage funding operations
  - [ ] Implement webhook handler for Alpaca funding completion
  - [ ] Add end-to-end tests for virtual account to brokerage funding flow
- [ ] Implement comprehensive error handling and resilience (AC: #6)
  - [ ] Add exponential backoff retry for failed funding transfers
  - [ ] Implement circuit breaker protection for Alpaca API
  - [ ] Add funding failure notifications and status updates
  - [ ] Create integration tests for error scenarios
- [ ] Update database schema for enhanced tracking (AC: #5)
  - [ ] Add broker_funded_initiated_at timestamp to deposits table
  - [ ] Add broker_funded_completed_at timestamp to deposits table
  - [ ] Add alpaca_transfer_reference field for tracking
  - [ ] Create and test database migration scripts
- [ ] Add monitoring and audit logging (AC: #7)
  - [ ] Implement structured logging for all Alpaca API interactions
  - [ ] Add correlation ID tracking across funding operations
  - [ ] Configure alerts for funding failures and timeouts
  - [ ] Add metrics for funding success rates and timing

## Dev Notes

- Relevant architecture patterns: Adapter Pattern for Alpaca API integration, Repository Pattern for database access, Circuit Breaker for resilience
- Source tree components to touch: internal/adapters/alpaca/, internal/domain/services/funding/, internal/infrastructure/repositories/
- Testing standards: Unit tests for Alpaca API client, integration tests for funding flow, API tests for deposit processing

### Project Structure Notes

- Build upon existing Alpaca API client implementation from existing codebase
- Extend Funding Service with AlpacaFunding method following established patterns
- Leverage existing virtual_accounts and deposit status tracking infrastructure
- Add new deposit status fields for brokerage funding states
- Follow established REST API patterns, integrate with existing funding workflow
- Use existing error handling patterns: exponential backoff retry, circuit breaker, user-friendly error messages
- Maintain separation between payment processing (Due API) and brokerage funding (Alpaca API)

### References

- [Source: docs/tech-spec-epic-2.md#Acceptance Criteria] - AC #3: Brokerage Funding
- [Source: docs/tech-spec-epic-2.md#Workflows and Sequencing] - Enhanced Funding Flow step 4
- [Source: docs/architecture.md#Data Flow (Funding)] - Virtual Account -> Alpaca Deposit (USD)
- [Source: docs/prd/epic-2-stablecoin-funding-flow.md] - Securely transfer USD into Alpaca brokerage account

## Dev Agent Record

### Context Reference

- docs/stories/2-3-alpaca-account-funding.context.md

### Agent Model Used

BMAD Story Creation Agent v6.0.0-alpha.0

### Debug Log References

### Completion Notes List

### File List

### Change Log

- **2025-11-03**: Story drafted by BMAD SM Agent
  - Derived requirements from tech-spec-epic-2.md AC #3 (Brokerage Funding)
  - Mapped to architecture patterns: Adapter Pattern, Repository Pattern, Circuit Breaker
  - Incorporated lessons from story 2-1 and 2-2 (Due API patterns, error handling, transfer workflows)
  - Aligned with existing Alpaca integration and brokerage funding workflows
