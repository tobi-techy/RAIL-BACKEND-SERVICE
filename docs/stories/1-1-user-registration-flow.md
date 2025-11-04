# Story 1.1: User Registration and Authentication

Status: done

## Story

As a new user,
I want to register for the STACK platform with email/password authentication,
so that I can create a secure account and begin the onboarding process.

## Acceptance Criteria

1. Users can register with email/password combination
2. Email verification system sends secure codes for account activation
3. Password hashing uses industry-standard algorithms (bcrypt/PBKDF2)
4. JWT tokens issued for authenticated sessions with refresh token support
5. Complete login/logout flow with session management

## Tasks / Subtasks

- [x] Implement user registration endpoint (AC: 1)
  - [x] Add SignUpRequest/Response entity definitions
  - [x] Create Register handler in AuthSignupHandlers
  - [x] Handle email/phone validation and duplicate checking
- [x] Implement email verification system (AC: 2)
  - [x] Add verification code generation and sending
  - [x] Create VerifyCode handler for code submission
  - [x] Handle verification success/failure scenarios
- [x] Implement secure password handling (AC: 3)
  - [x] Add password hashing using crypto.HashPassword
  - [x] Store hashed passwords securely in database
  - [x] Implement password validation rules
- [x] Implement JWT token management (AC: 4)
  - [x] Add token generation with auth.GenerateTokenPair
  - [x] Implement refresh token functionality
  - [x] Add token validation middleware
- [x] Implement complete login/logout flow (AC: 5)
  - [x] Create Login handler with password verification
  - [x] Add session tracking and last login updates
  - [x] Implement logout and token invalidation

## Dev Notes

- Relevant architecture patterns and constraints: Modular Monolith, Repository Pattern, Adapter Pattern
- Source tree components to touch: internal/onboarding/, internal/api/graphql/
- Testing standards summary: Unit tests for service methods, integration tests for external APIs

### Project Structure Notes

- Alignment with unified project structure: Follows Go module structure in internal/onboarding/
- No conflicts detected with existing architecture patterns

### References

- [Source: docs/prd/epic-1-onboarding-wallet-management.md#Story-1.1]
- [Source: internal/api/handlers/auth_signup_handlers.go]
- [Source: internal/domain/services/onboarding/]
- [Source: internal/infrastructure/repositories/user_repository.go]
- [Source: pkg/auth/ - JWT token management]
- [Source: pkg/crypto/ - Password hashing]
- [Source: docs/architecture/5-components.md#Onboarding-Service-Module]

## Dev Agent Record

### Context Reference

<!-- Path(s) to story context XML will be added here by context workflow -->

### Agent Model Used

{{agent_model_name_version}}

### Debug Log References

### Completion Notes List

### File List
