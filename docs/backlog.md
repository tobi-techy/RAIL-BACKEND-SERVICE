# STACK Service - Technical Backlog

This file tracks technical debt, follow-up items, and improvements identified during code reviews and development.

## Format
| Date | Story | Epic | Type | Severity | Owner | Status | Notes |
|------|-------|------|------|----------|-------|--------|-------|

## Backlog Items

| Date | Story | Epic | Type | Severity | Owner | Status | Notes |
|------|-------|------|------|----------|-------|--------|-------|
| 2025-11-05 | 2.1 | 2 | Bug | Critical | TBD | Open | Implement DUE Recipients API integration - virtual account creation requires recipient ID as destination, not Alpaca account ID. See: internal/adapters/due/adapter.go:36-42 |
| 2025-11-05 | 2.1 | 2 | Bug | Critical | TBD | Open | Fix virtual account creation flow to use recipient ID instead of Alpaca account ID. Update CreateVirtualAccount method. See: internal/adapters/due/adapter.go |
| 2025-11-05 | 2.1 | 2 | TechDebt | Critical | TBD | Open | Add database migration for recipient tracking (recipient_id, schema_in, currency_in, rail_out, currency_out fields). Create: migrations/016_add_recipient_fields_to_virtual_accounts.up.sql |
| 2025-11-05 | 2.1 | 2 | TechDebt | High | TBD | Open | Implement comprehensive integration tests for DUE API with mocked responses. See: test/integration/virtual_account_integration_test.go |
| 2025-11-05 | 2.1 | 2 | Enhancement | High | TBD | Open | Enhance error handling to parse DUE-specific error responses and map to error types. See: internal/adapters/due/client.go:145-149 |
| 2025-11-05 | 2.1 | 2 | Enhancement | Medium | TBD | Open | Implement DUE webhook handler for deposit notifications with signature verification. Add to: internal/api/handlers/funding_investing_handlers.go |
| 2025-11-05 | 2.1 | 2 | Enhancement | Medium | TBD | Open | Make currency/rail configuration flexible to support international users. See: internal/adapters/due/adapter.go:36-42 |
| 2025-11-05 | 2.1 | 2 | TechDebt | Medium | TBD | Open | Add validation for DUE API responses to handle missing/null fields gracefully. See: internal/adapters/due/adapter.go |
| 2025-11-06 | 2.3 | 2 | TechDebt | Medium | TBD | Open | Verify database migrations include alpaca_funding_tx_id and alpaca_funded_at columns in deposits table. See: internal/domain/entities/stack_entities.go:95-96 |
| 2025-11-06 | 2.3 | 2 | Enhancement | Low | TBD | Open | Extract retry configuration (maxFundingRetries, fundingRetryDelay) to environment variables for operational flexibility. See: internal/workers/funding_webhook/alpaca_funding.go:14-15 |
| 2025-11-06 | 2.3 | 2 | TechDebt | Low | TBD | Open | Add correlation ID extraction from context to all log statements per architecture standards (Architecture.md 11.2). See: internal/workers/funding_webhook/alpaca_funding.go |
| 2025-11-06 | 2.3 | 2 | Enhancement | Low | TBD | Open | Implement actual notification delivery mechanisms (push/email/SMS) in NotificationService. See: internal/domain/services/notification_service.go |
| 2025-11-06 | 2.3 | 2 | TechDebt | Low | TBD | Open | Add missing test cases: virtual account not found, balance update failure, notification failures. See: test/unit/funding/alpaca_funding_test.go |
| 2025-11-06 | 2.3 | 2 | Enhancement | Low | TBD | Open | Consider adding rate limiting on funding operations for security hardening to prevent abuse or accidental duplicate processing |
| 2025-11-06 | 2.4 | 2 | TechDebt | High | Tobi | Done | Implement SQS-based async processing for withdrawal flow - replaced goroutines with queue.Publisher. See: pkg/queue/sqs.go, withdrawal_service.go:125-133 |
| 2025-11-06 | 2.4 | 2 | TechDebt | High | Tobi | Done | Add circuit breaker protection for Alpaca and Due API calls using gobreaker. See: pkg/circuitbreaker/breaker.go, withdrawal_service.go |
| 2025-11-06 | 2.4 | 2 | Bug | High | Tobi | Done | Complete saga compensation logic - implemented compensateAlpacaDebit method. See: withdrawal_service.go:283-315 |
| 2025-11-06 | 2.4 | 2 | Bug | Medium | Tobi | Done | Fix test race condition in withdrawal unit tests - added MockQueuePublisher. See: test/unit/withdrawal_service_test.go |
| 2025-11-06 | 2.4 | 2 | TechDebt | Medium | TBD | Open | Replace blocking polling with event-driven approach using Due webhooks or SQS. See: internal/domain/services/withdrawal_service.go:217-245 |
| 2025-11-06 | 2.4 | 2 | Enhancement | Medium | Tobi | Done | Implement virtual account existence check before use in withdrawal flow. See: internal/adapters/due/onramp.go:165-169 |
| 2025-11-06 | 2.4 | 2 | Enhancement | Low | Tobi | Done | Add GraphQL mutation support for withdrawal requests per story AC #1. See: internal/api/graphql/withdrawal_resolver.go |
| 2025-11-06 | 2.4 | 2 | Enhancement | Low | TBD | Open | Implement user notification system for withdrawal completion events per AC #6 |
| 2025-11-06 | 2.4 | 2 | TechDebt | Low | TBD | Open | Add integration and E2E tests with Testcontainers to verify >99% success rate per AC #7 |
| 2025-11-06 | 2.4 | 2 | Enhancement | Low | TBD | Open | Enhance address validation with chain-specific format checks (Ethereum checksum, Solana base58). See: internal/api/handlers/withdrawal_handlers.go |
