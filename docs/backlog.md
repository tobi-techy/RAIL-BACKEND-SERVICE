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
