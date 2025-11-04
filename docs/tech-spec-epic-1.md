# Epic Technical Specification: Onboarding & Wallet Management

Date: 2025-11-03
Author: Tobi
Epic ID: 1
Status: Draft

---

## Overview

Epic 1 focuses on delivering a smooth user onboarding and wallet management experience that abstracts away Web3 complexity for end users. The system will provide a mobile-first sign-up flow, implement passcode-based authentication for app login, and create managed Circle Developer-Controlled Wallets that eliminate the need for users to manage cryptographic seed phrases. This epic establishes the foundation for user identity and wallet management within the STACK platform, ensuring security and usability while maintaining Web3 capabilities under the hood.

## Objectives and Scope

### In-Scope
- Simple, mobile-first sign-up flow with Auth0/Cognito integration
- Passcode support for secure app login (hashing and verification)
- Automated wallet creation using Circle Developer-Controlled Wallets
- User profile management and KYC/AML orchestration
- Security and custody abstraction for Web3 complexity

### Out-of-Scope
- Advanced KYC/AML provider integration details
- Multi-chain wallet support beyond initial implementation
- Advanced user profile customization features
- Social login integrations
- Password-based authentication (passcode-only for MVP)

### Success Criteria
- 90%+ of new users complete onboarding successfully
- Wallet creation works 99%+ of the time
- Passcode authentication is secure and reliable
- User experience abstracts Web3 complexity effectively

## System Architecture Alignment

This epic aligns with the Go-based modular monolith architecture, implementing the Onboarding Service Module and Wallet Service Module as defined in the system architecture. The implementation follows the established patterns:

- **Onboarding Service Module**: Handles user registration, profile management, KYC orchestration, and passcode setup/verification
- **Wallet Service Module**: Manages Circle Developer-Controlled Wallet lifecycle and address provision
- **Integration Points**: Auth0/Cognito for identity, Circle API for wallet management, PostgreSQL for data persistence
- **Communication**: Internal service communication via Go interfaces, external access through GraphQL API Gateway
- **Security**: Passcode hashing, secure wallet management, and custody abstraction as specified in the architecture patterns

## Detailed Design

### Services and Modules

| Service/Module | Responsibility | Key Interfaces | Dependencies | Owner |
|---------------|----------------|----------------|--------------|-------|
| **Onboarding Service** | User registration, profile management, KYC orchestration, passcode setup/verification | `CreateUser()`, `GetUserProfile()`, `UpdateKYCStatus()`, `SetPasscode()`, `VerifyPasscode()` | Auth0/Cognito, KYC Provider, Wallet Service, PostgreSQL (users table) | Onboarding Team |
| **Wallet Service** | Circle Developer-Controlled Wallet lifecycle management and address provision | `CreateUserWallet()`, `GetDepositAddress()` | Circle API, PostgreSQL (users, wallets tables) | Wallet Team |
| **API Gateway (GraphQL)** | Unified external API interface for mobile app | Sign-up/Login mutations, profile queries, wallet address queries | All service modules | Platform Team |

### Data Models and Contracts

#### users table
```sql
CREATE TABLE users (
    id UUID PRIMARY KEY,
    auth_provider_id VARCHAR UNIQUE NOT NULL,
    email VARCHAR UNIQUE,
    phone_number VARCHAR UNIQUE,
    kyc_status ENUM ('not_started', 'pending', 'approved', 'rejected'),
    passcode_hash VARCHAR,
    created_at TIMESTAMP,
    updated_at TIMESTAMP
);
```

#### wallets table
```sql
CREATE TABLE wallets (
    id UUID PRIMARY KEY,
    user_id UUID REFERENCES users(id),
    chain ENUM ('ethereum', 'solana'),
    address VARCHAR,
    circle_wallet_id VARCHAR,
    status ENUM ('creating', 'active', 'inactive'),
    created_at TIMESTAMP,
    updated_at TIMESTAMP
);
```

**Key Relationships:**
- users.id → wallets.user_id (1:many)
- Wallets are created automatically during onboarding
- Passcode_hash uses secure hashing (bcrypt/PBKDF2)

### APIs and Interfaces

#### GraphQL API Endpoints
```graphql
# User Registration
mutation SignUp($input: SignUpInput!) {
  signUp(input: $input) {
    user {
      id
      email
      kycStatus
    }
    accessToken
  }
}

# Passcode Setup
mutation SetPasscode($passcode: String!) {
  setPasscode(passcode: $passcode) {
    success
  }
}

# Wallet Address Query
query GetDepositAddress($chain: ChainType!) {
  depositAddress(chain: $chain)
}
```

#### Internal Service Interfaces
```go
// Onboarding Service Interface
type OnboardingService interface {
    CreateUser(ctx context.Context, authID, email string) (*User, error)
    UpdateKYCStatus(ctx context.Context, userID string, status KYCStatus) error
    SetPasscode(ctx context.Context, userID, passcode string) error
    VerifyPasscode(ctx context.Context, userID, passcode string) (bool, error)
}

// Wallet Service Interface
type WalletService interface {
    CreateUserWallet(ctx context.Context, userID string, chain ChainType) (*Wallet, error)
    GetDepositAddress(ctx context.Context, userID string, chain ChainType) (string, error)
}
```

#### External API Contracts
- **Auth0/Cognito**: OIDC token validation, user profile retrieval
- **Circle API**: Wallet creation, address management
- **KYC Provider**: Status updates via webhooks

### Workflows and Sequencing

#### User Onboarding Flow
1. **App → Auth0/Cognito**: User initiates sign-up/login
2. **Auth0 → App**: OIDC flow returns ID token
3. **App → API Gateway**: SignUp/Login mutation with ID token
4. **Gateway → Onboarding Service**: Verify token and create user
5. **Onboarding → PostgreSQL**: Create user record
6. **Onboarding → KYC Provider**: Initiate KYC check (async)
7. **Onboarding → Wallet Service**: Trigger wallet creation (async)
8. **Wallet → Circle API**: Create Developer-Controlled Wallet
9. **Circle → Wallet**: Return wallet ID and address
10. **Wallet → PostgreSQL**: Store wallet information
11. **KYC Provider → Onboarding**: KYC status webhook (async)
12. **Onboarding → PostgreSQL**: Update KYC status
13. **Onboarding → Gateway → App**: Return user profile

#### Passcode Setup Flow (Separate)
1. **App → Gateway**: SetPasscode mutation
2. **Gateway → Onboarding**: Hash and store passcode
3. **Onboarding → PostgreSQL**: Store passcode_hash
4. **Onboarding → Gateway → App**: Success confirmation

#### Error Handling
- Wallet creation failures: Retry with exponential backoff, notify user
- KYC failures: Update user status, provide retry mechanism
- Auth failures: Clear session, redirect to login

## Non-Functional Requirements

### Performance

- **Onboarding Completion**: 90% of users complete full onboarding (sign-up + wallet + passcode) within 5 minutes
- **Wallet Creation**: 99% of wallet creation operations complete within 30 seconds
- **API Response Times**: All GraphQL mutations return within 2 seconds (p95)
- **Concurrent Users**: Support 1000+ concurrent onboarding flows during peak hours
- **Mobile Performance**: App remains responsive during wallet creation (async operations)

### Security

- **Passcode Security**: Bcrypt/PBKDF2 hashing with minimum 10 rounds, minimum 6-character passcodes
- **Token Security**: Auth0/Cognito OIDC tokens validated on every request
- **Wallet Security**: Circle Developer-Controlled Wallets with institutional custody
- **Data Protection**: PII encrypted at rest, secure API communication (HTTPS/TLS 1.3)
- **Audit Logging**: All authentication and wallet operations logged for compliance
- **Rate Limiting**: API rate limits to prevent abuse (100 requests/minute per user)

### Reliability/Availability

- **Service Availability**: 99.9% uptime for onboarding services
- **Wallet Creation Success**: 99% success rate for wallet creation operations
- **Graceful Degradation**: Onboarding continues even if KYC provider is unavailable (deferred processing)
- **Recovery**: Automatic retry for failed wallet creation (3 attempts with exponential backoff)
- **Data Consistency**: ACID transactions for user/wallet creation to prevent orphaned records

### Observability

- **Metrics**: User registration rate, wallet creation success/failure rates, onboarding completion time
- **Logging**: Structured JSON logs for all onboarding events with correlation IDs
- **Tracing**: OpenTelemetry spans across Auth0 → Onboarding → Wallet → Circle API calls
- **Monitoring**: Real-time dashboards for onboarding funnel conversion rates
- **Alerts**: Wallet creation failures, Auth0 service degradation, high error rates

## Dependencies and Integrations

| Component | Technology | Version/Constraints | Purpose |
|-----------|------------|-------------------|---------|
| **Go Runtime** | golang | 1.21.x+ | Core application runtime |
| **PostgreSQL** | Database | 15.x+ | User and wallet data persistence |
| **Auth0/Cognito** | Identity Provider | Latest stable | User authentication and OIDC |
| **Circle API** | Wallet Provider | v2+ | Developer-Controlled Wallet creation |
| **KYC Provider** | AML Service | TBD | User verification and compliance |
| **Gin Framework** | HTTP Router | v1.11.0 | GraphQL API implementation |
| **Zap Logger** | Logging | v1.27.0 | Structured JSON logging |
| **OpenTelemetry** | Observability | v1.38.0 | Distributed tracing and metrics |

## Acceptance Criteria (Authoritative)

1. **User Registration**: New users can successfully register using Auth0/Cognito OIDC flow
2. **Profile Creation**: User profile is created in database with auth_provider_id, email, and initial kyc_status
3. **Passcode Setup**: Users can set a 6+ character passcode that is securely hashed and stored
4. **Passcode Verification**: Users can authenticate using their passcode with proper verification
5. **Wallet Creation**: Circle Developer-Controlled Wallet is automatically created during onboarding
6. **Wallet Association**: Wallet is properly associated with user and stored in database
7. **Deposit Address**: Users can retrieve their wallet deposit address for specified chains
8. **KYC Integration**: KYC check is initiated and status is tracked in user profile
9. **Onboarding Completion**: 90%+ success rate for complete onboarding flow (sign-up → wallet → passcode)
10. **Error Handling**: Failed wallet creation is retried and user is notified appropriately

## Traceability Mapping

| AC # | Acceptance Criteria | Spec Section | Component/API | Test Case |
|------|-------------------|---------------|---------------|-----------|
| 1 | User Registration | APIs and Interfaces | Onboarding Service, GraphQL API | `test_user_registration` |
| 2 | Profile Creation | Data Models | PostgreSQL users table | `test_user_profile_creation` |
| 3 | Passcode Setup | Security NFRs | Onboarding Service | `test_passcode_hashing` |
| 4 | Passcode Verification | APIs and Interfaces | Onboarding Service | `test_passcode_verification` |
| 5 | Wallet Creation | Workflows and Sequencing | Wallet Service, Circle API | `test_wallet_creation` |
| 6 | Wallet Association | Data Models | PostgreSQL wallets table | `test_wallet_association` |
| 7 | Deposit Address | APIs and Interfaces | Wallet Service, GraphQL API | `test_deposit_address` |
| 8 | KYC Integration | Workflows and Sequencing | Onboarding Service, KYC Provider | `test_kyc_initiation` |
| 9 | Onboarding Completion | Performance NFRs | Full onboarding flow | `test_onboarding_completion_rate` |
| 10 | Error Handling | Reliability NFRs | Wallet Service, retry logic | `test_wallet_creation_retry` |

## Risks, Assumptions, Open Questions

### Risks
- **Circle API Dependency**: Wallet creation failures could block user onboarding (mitigation: retry logic, alternative wallet providers)
- **KYC Provider Integration**: Unreliable KYC service could delay user activation (mitigation: async processing, manual override capability)
- **Auth0 Rate Limits**: High registration volume could hit Auth0 limits (mitigation: rate limiting, caching strategies)
- **Mobile Network Issues**: Poor connectivity during onboarding could cause failures (mitigation: offline-capable passcode setup)

### Assumptions
- Auth0/Cognito will provide reliable OIDC token validation
- Circle Developer-Controlled Wallets meet security and custody requirements
- KYC provider integration will be available during development
- PostgreSQL will handle expected user registration volumes
- Mobile app can handle async wallet creation notifications

### Open Questions
- Which KYC provider will be selected and what are their API contracts?
- What are the specific Circle API rate limits and costs?
- How should we handle users who fail KYC checks?
- What backup wallet creation strategy if Circle is unavailable?
- How to handle international users with different regulatory requirements?

## Test Strategy Summary

### Test Levels
- **Unit Tests**: Individual service methods (OnboardingService, WalletService) with mocked dependencies
- **Integration Tests**: Service-to-service communication and external API calls (Circle, Auth0)
- **API Tests**: GraphQL endpoint testing with various user scenarios
- **End-to-End Tests**: Complete onboarding flow from mobile app perspective

### Test Frameworks
- **Unit**: Go testing with testify/assert
- **Integration**: Test containers for PostgreSQL, mocked external APIs
- **API**: GraphQL client testing with real GraphQL schema
- **E2E**: Appium for mobile app testing

### Coverage Goals
- 80%+ code coverage for Onboarding and Wallet services
- 100% coverage of acceptance criteria
- All error paths and edge cases tested

### Test Environments
- **Local**: Docker Compose with test databases
- **CI/CD**: Automated testing on every PR and merge
- **Staging**: Full environment testing before production
