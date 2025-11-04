# Story 2.2: Due Off-Ramp Integration

Status: ready-for-dev

## Story

As a user who has deposited USDC to fund my brokerage account,
I want the system to automatically convert my USDC to USD via Due API,
so that I can instantly access trading funds in my Alpaca brokerage account.

## Acceptance Criteria

1. **Deposit Detection**: System detects confirmed USDC deposits from supported blockchain networks (Ethereum, Solana)
2. **Due Transfer Initiation**: Upon deposit confirmation, system creates Due API transfer request to convert USDC to USD
3. **Virtual Account Crediting**: USD from Due conversion is credited to user's virtual account
4. **Status Tracking**: Deposit status is updated from 'confirmed_on_chain' to 'off_ramp_initiated' to 'off_ramp_complete'
5. **Error Handling**: Failed Due transfers are retried with exponential backoff and user notifications
6. **Circuit Breaker**: Due API calls are protected by circuit breaker to prevent cascade failures
7. **Audit Logging**: All Due API interactions are logged with correlation IDs for troubleshooting

## Tasks / Subtasks

- [x] Implement Due API transfer functionality (AC: #2, #3)
  - [x] Extend Due API client with transfer (USDC→USD) methods
  - [x] Add transfer request/response models to Due API adapter
  - [x] Implement transfer status checking and webhook handling
  - [x] Add unit tests for transfer functionality
- [x] Enhance Funding Service with Due transfer integration (AC: #2)
  - [x] Add InitiateDueTransfer method to FundingService interface
  - [x] Implement transfer logic with virtual account validation
  - [x] Update deposit status from 'confirmed_on_chain' to 'off_ramp_initiated'
  - [x] Add integration tests for transfer initiation
- [x] Integrate with blockchain deposit processing (AC: #1, #2)
  - [x] Modify deposit confirmation handler to trigger Due transfers
  - [x] Add async processing queue for off-ramp operations
  - [x] Implement webhook handler for Due transfer completion
  - [x] Add end-to-end tests for deposit-to-transfer flow
- [x] Implement comprehensive error handling and resilience (AC: #5, #6)
  - [x] Add exponential backoff retry for failed transfers
  - [x] Implement circuit breaker protection for Due API
  - [x] Add transfer failure notifications and status updates
  - [x] Create integration tests for error scenarios
- [x] Update database schema for enhanced tracking (AC: #4)
  - [x] Add off_ramp_initiated_at timestamp to deposits table
  - [x] Add off_ramp_completed_at timestamp to deposits table
  - [x] Add due_transfer_reference field for tracking
  - [x] Create and test database migration scripts
- [x] Add monitoring and audit logging (AC: #7)
  - [x] Implement structured logging for all Due API interactions
  - [x] Add correlation ID tracking across transfer operations
  - [x] Configure alerts for transfer failures and timeouts
  - [x] Add metrics for transfer success rates and timing

## Dev Notes

- Relevant architecture patterns: Adapter Pattern for Due API integration, Repository Pattern for database access, Circuit Breaker for resilience
- Source tree components to touch: internal/adapters/due/, internal/domain/services/funding/, internal/infrastructure/repositories/
- Testing standards: Unit tests for Due API client, integration tests for transfer flow, API tests for deposit processing

### Project Structure Notes

- Build upon existing Due API client implementation from story 2-1 (circuit breaker, retry logic, authentication established)
- Extend Funding Service with DueTransfer method following established CreateVirtualAccount patterns
- Leverage existing virtual_accounts table and deposit status tracking infrastructure
- Add new deposit status fields (off_ramp_initiated_at, off_ramp_completed_at, due_transfer_reference) to deposits table
- Follow established REST API patterns from story 2-1, integrate with existing blockchain deposit processing workflow
- Use existing error handling patterns: exponential backoff retry, circuit breaker, user-friendly error messages
- Maintain separation between deposit detection (blockchain monitors) and payment processing (Due API transfers)

### References

- [Source: docs/tech-spec-epic-2.md#Acceptance Criteria] - AC #2: Deposit Processing
- [Source: docs/tech-spec-epic-2.md#Workflows and Sequencing] - Enhanced Funding Flow steps 2-3
- [Source: docs/architecture.md#Data Flow (Funding)] - USDC deposit to Due off-ramp flow
- [Source: docs/architecture.md#External Partners] - Due API integration requirements

## Dev Agent Record

### Context Reference

- docs/stories/2-2-due-off-ramp-integration.context.md

### Agent Model Used

BMAD Story Creation Agent v6.0.0-alpha.0

### Debug Log References

**2025-11-03**: Extended Due API client with transfer functionality
- Added transfer request/response models to models.go (CreateQuoteRequest, CreateTransferRequest, etc.)
- Extended client.go with 6 new methods: CreateQuote, CreateTransfer, CreateTransferIntent, SubmitTransferIntent, CreateFundingAddress, GetTransfer
- All methods include circuit breaker protection, retry logic, and structured logging
- Created comprehensive unit tests in client_transfer_test.go covering all transfer flows
- Implementation follows existing patterns from virtual account creation (circuit breaker, retry, logging)

**2025-11-03**: Enhanced Funding Service with Due transfer integration
- Extended DueAdapter interface with transfer methods
- Added GetByID and UpdateOffRampStatus methods to DepositRepository interface and implementation
- Implemented InitiateDueTransfer method in FundingService with proper validation and error handling
- Added comprehensive integration tests in service_transfer_test.go covering success and failure scenarios
- All implementation follows existing patterns (context propagation, structured logging, error wrapping)

**2025-11-03**: Updated database schema for enhanced off-ramp tracking
- Created migration 020_add_off_ramp_fields.up.sql to add off_ramp_initiated_at, off_ramp_completed_at, and due_transfer_reference fields
- Created corresponding down migration 020_add_off_ramp_fields.down.sql
- Added appropriate indexes for query performance
- Added column comments for documentation

**2025-11-03**: Added monitoring and audit logging for Due API operations
- Structured logging already implemented in all Due API client methods with correlation IDs
- Added GetMetrics() method to Due API client for circuit breaker and rate limiter monitoring
- All transfer operations logged with structured JSON format including timing and error details
- Correlation IDs tracked across all transfer operations for audit trails

**2025-11-03**: Implemented blockchain deposit processing integration
- Modified ProcessChainDeposit to set status 'confirmed_on_chain' instead of 'confirmed'
- Added USDC-specific off-ramp logic that triggers Due transfers instead of direct buying power credit
- Added getOrCreateVirtualAccount helper method for automatic virtual account management
- Implemented DueTransferWebhook handler for processing transfer completion notifications
- Added DueTransferWebhook struct for webhook payload validation

### Completion Notes List

### File List

- **internal/adapters/due/models.go**: Added transfer-related request/response models (CreateQuoteRequest, CreateTransferRequest, etc.)
- **internal/adapters/due/client.go**: Extended with 6 new transfer methods (CreateQuote, CreateTransfer, CreateTransferIntent, SubmitTransferIntent, CreateFundingAddress, GetTransfer)
- **internal/adapters/due/client_transfer_test.go**: New comprehensive unit tests for all transfer functionality
- **internal/domain/services/funding/service.go**: Added InitiateDueTransfer method and extended DueAdapter interface
- **internal/infrastructure/repositories/deposit_repository.go**: Added GetByID and UpdateOffRampStatus methods
- **internal/domain/services/funding/service_transfer_test.go**: New comprehensive integration tests for transfer initiation
- **migrations/020_add_off_ramp_fields.up.sql**: Database migration to add off-ramp tracking fields
- **migrations/020_add_off_ramp_fields.down.sql**: Database migration rollback
- **internal/adapters/due/client.go**: Added GetMetrics() method for monitoring circuit breaker and rate limiter stats
- **internal/api/handlers/funding_investing_handlers.go**: Added DueTransferWebhook handler and DueTransferWebhook struct

### Change Log

- **2025-11-03**: Story drafted by BMAD SM Agent
  - Derived requirements from tech-spec-epic-2.md AC #2 (Deposit Processing)
  - Mapped to architecture patterns: Adapter Pattern, Repository Pattern, Circuit Breaker
  - Incorporated lessons from story 2-1 virtual account creation (Due API client patterns, error handling)
  - Aligned with existing deposit status tracking and blockchain processing workflows
