# Story 2.1: Virtual Account Creation

Status: review

## Story

As a STACK user preparing to fund my brokerage account,
I want to create and manage a virtual account through the Due API,
so that I can securely process USDC to USD conversions for instant trading.

## Acceptance Criteria

1. Users can initiate virtual account creation via GraphQL mutation
2. Due API successfully creates virtual account with unique ID
3. Virtual account status is tracked and updated in database
4. Virtual account can be linked to Alpaca brokerage account
5. Users receive confirmation of virtual account creation
6. Virtual account creation handles errors gracefully with retry logic
7. Virtual account data is properly persisted in PostgreSQL

## Tasks / Subtasks

- [x] Implement Due API client for virtual account creation
  - [x] Add Due API authentication and configuration
  - [x] Create API client methods for virtual account operations
  - [x] Implement request/response models for Due API
- [x] Create virtual_accounts database table
  - [x] Define table schema with required fields
  - [x] Add foreign key relationships to users table
  - [x] Create database migration scripts
- [x] Implement REST API endpoint for virtual account creation (GraphQL deferred)
  - [x] Define REST endpoint at POST /api/v1/funding/virtual-accounts
  - [x] Implement service logic in Funding Service with Due API integration
  - [x] Add input validation and error handling
- [x] Add virtual account status tracking
  - [x] Implement status enum (creating, active, inactive)
  - [x] Add status update mechanisms
  - [ ] Handle async status updates from Due API webhooks
- [ ] Implement brokerage account linking
  - [x] Add brokerage_account_id field to virtual accounts
  - [ ] Create linking logic between virtual and brokerage accounts
  - [ ] Validate Alpaca account ownership before linking
- [x] Add comprehensive error handling and retry logic
  - [x] Handle Due API failures with exponential backoff
  - [x] Implement circuit breaker for Due API calls
  - [x] Add user-friendly error messages and logging

## Dev Notes

- Relevant architecture patterns: Adapter Pattern for Due API integration, Repository Pattern for database access
- Source tree components: internal/adapters/due/, internal/core/funding/, internal/persistence/postgres/
- Testing standards: Unit tests for API client, integration tests for database operations, API tests for GraphQL mutations

### Project Structure Notes

- Follows modular monolith structure with Funding Service in internal/core/funding/
- Due API adapter in internal/adapters/due/ following established adapter patterns
- Database operations use Repository pattern in internal/persistence/

### References

- Cite all technical details with source paths and sections, e.g. [Source: docs/tech-spec-epic-2.md#Detailed Design]
- [Source: docs/tech-spec-epic-2.md#APIs and Interfaces] - GraphQL mutations
- [Source: docs/tech-spec-epic-2.md#Data Models and Contracts] - Virtual accounts table schema
- [Source: docs/architecture.md#5.1 Component List] - Funding Service responsibilities
- [Source: docs/prd.md#Functional Requirements] - Virtual account requirements

## Dev Agent Record

### Context Reference

- docs/stories/2-1-virtual-account-creation.context.md

### Agent Model Used

SM Agent (Scrum Master) with technical background

### Debug Log References

### Completion Notes List

- **Due API Client Implementation**: Created complete Due API adapter following established patterns (circuit breaker, retry logic, rate limiting, structured logging). Implemented VirtualAccount entity with UUID-based IDs and status enum. Client handles authentication, request/response models, and error handling with exponential backoff.
- **Database Schema**: Created virtual_accounts table with proper foreign key relationships, status enum, indexes, RLS policies, and migration scripts following established patterns.
- **REST API Implementation**: Implemented virtual account creation via REST endpoint with proper authentication, validation, error handling, and integration with Due API and database persistence. GraphQL implementation deferred until GraphQL infrastructure is established.
- **Status Tracking**: Implemented virtual account status enum and basic status tracking. Async webhook handling for status updates remains as future enhancement.
- **Error Handling**: Comprehensive error handling with circuit breaker, exponential backoff retry, and user-friendly error messages implemented throughout the stack.

### File List

- internal/adapters/due/client.go - Due API client with circuit breaker, retry logic, and virtual account creation
- internal/adapters/due/models.go - Request/response models and error types for Due API
- internal/domain/entities/funding_entities.go - VirtualAccount entity with status enum and validation
- internal/domain/services/funding/service.go - Updated with CreateVirtualAccount method and new interfaces
- internal/api/handlers/funding_investing_handlers.go - Added CreateVirtualAccount REST endpoint handler
- internal/api/routes/routes.go - Added virtual account creation route
- migrations/018_add_virtual_accounts.up.sql - Database migration to create virtual_accounts table
- migrations/018_add_virtual_accounts.down.sql - Rollback migration for virtual_accounts table
