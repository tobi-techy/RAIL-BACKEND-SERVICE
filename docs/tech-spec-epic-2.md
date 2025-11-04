# Epic Technical Specification: Stablecoin Funding Flow

Date: 2025-11-03
Author: Tobi
Epic ID: 2
Status: Draft

---

## Overview

Epic 2 focuses on enabling seamless stablecoin funding and withdrawal flows using Due API integration. The system will replace current Circle-based off-ramp/on-ramp operations with Due's comprehensive payment processing capabilities, including virtual account management and enhanced recipient handling. This epic establishes the core payment infrastructure for Web3-to-TradFi transfers within the STACK platform.

## Objectives and Scope

### In-Scope
- Due API integration for USDC/USD conversion and transfers
- Virtual account creation and management for brokerage funding
- Enhanced KYC validation for payment operations
- Recipient management system for withdrawal destinations
- Multi-pathway deposit support (Solana, Ethereum, etc.)
- Real-time balance synchronization between payment and brokerage accounts
- Comprehensive error handling and retry mechanisms

### Out-of-Scope
- Additional payment providers beyond Due
- Advanced trading features (covered in Epic 4)
- Investment basket funding (covered in Epic 3)
- Multi-currency support beyond USDC/USD
- International payment processing complexities

### Success Criteria
- Users can fund brokerage accounts within minutes of stablecoin deposits
- End-to-end success rate >99% for funding/withdrawal operations
- Support for at least 2 blockchain deposit pathways
- Virtual account creation and linking works reliably
- Due API integration handles all payment processing requirements

## System Architecture Alignment

This epic integrates with the existing modular monolith architecture, extending the Funding Service Module to use Due API instead of Circle for payment operations. The implementation follows established patterns:

- **Funding Service Module**: Enhanced with Due API client and virtual account management
- **Integration Points**: Due API for payments, Alpaca API for brokerage, existing blockchain monitoring
- **Data Flow**: Enhanced to include virtual account routing and recipient management
- **Security**: Maintains existing security patterns with additional Due API authentication
- **Observability**: Extends current metrics to include Due API performance monitoring

## Detailed Design

### Services and Modules

| Service/Module | Responsibility | Key Interfaces | Dependencies | Owner |
|---------------|----------------|----------------|--------------|-------|
| **Funding Service** | Orchestrate payment flows, virtual accounts, recipient management | `CreateVirtualAccount()`, `InitiateDueTransfer()`, `LinkBrokerageAccount()` | Due API, Alpaca API, PostgreSQL | Payment Team |
| **Payment Processing** | Handle Due API interactions and payment state management | `ProcessDeposit()`, `ProcessWithdrawal()`, `ValidatePayment()` | Due API, Blockchain monitors | Payment Team |
| **Account Management** | Virtual account creation and brokerage account linking | `CreateVirtualAccount()`, `LinkToBrokerage()`, `SyncBalances()` | Due API, Alpaca API | Integration Team |

### Data Models and Contracts

#### Virtual Accounts Table
```sql
CREATE TABLE virtual_accounts (
    id UUID PRIMARY KEY,
    user_id UUID REFERENCES users(id),
    due_account_id VARCHAR UNIQUE NOT NULL,
    brokerage_account_id VARCHAR,
    status ENUM ('creating', 'active', 'inactive'),
    created_at TIMESTAMP,
    updated_at TIMESTAMP
);
```

#### Recipients Table
```sql
CREATE TABLE recipients (
    id UUID PRIMARY KEY,
    user_id UUID REFERENCES users(id),
    name VARCHAR NOT NULL,
    blockchain_address VARCHAR,
    bank_account_details JSONB,
    type ENUM ('blockchain', 'bank_account'),
    is_default BOOLEAN DEFAULT false,
    created_at TIMESTAMP,
    updated_at TIMESTAMP
);
```

### APIs and Interfaces

#### GraphQL Mutations
```graphql
mutation CreateVirtualAccount {
  createVirtualAccount {
    id
    dueAccountId
    status
  }
}

mutation InitiateFunding($amount: Float!, $asset: AssetType!) {
  initiateFunding(amount: $amount, asset: $asset) {
    transactionId
    status
    estimatedCompletion
  }
}

mutation AddRecipient($input: RecipientInput!) {
  addRecipient(input: $input) {
    id
    name
    type
  }
}
```

### Workflows and Sequencing

#### Enhanced Funding Flow
1. **User deposits USDC** → Blockchain monitor detects transaction
2. **Funding Service** → Validates deposit and creates Due transfer request
3. **Due API** → Converts USDC to USD and credits virtual account
4. **Virtual Account** → Triggers brokerage funding via Alpaca API
5. **Balance Update** → Syncs buying power across all user accounts
6. **Notification** → User receives funding confirmation

#### Enhanced Withdrawal Flow
1. **User initiates withdrawal** → Validates balance and recipient
2. **Brokerage Debit** → Alpaca API processes USD withdrawal
3. **Due API Transfer** → Converts USD to USDC via Due
4. **Blockchain Transfer** → Sends USDC to user's specified recipient
5. **Status Updates** → Real-time progress tracking throughout

## Non-Functional Requirements

### Performance
- **Transfer Completion**: 95% of funding operations complete within 5 minutes
- **API Response Times**: Due API calls respond within 3 seconds (p95)
- **Concurrent Operations**: Support 500+ simultaneous funding/withdrawal operations
- **Blockchain Confirmations**: Handle varying confirmation times across chains

### Security
- **Due API Authentication**: Secure API key management and request signing
- **Virtual Account Isolation**: Separate payment processing from brokerage accounts
- **Recipient Validation**: Prevent unauthorized withdrawal destinations
- **Audit Logging**: Complete transaction history and compliance logging

### Reliability/Availability
- **Service Availability**: 99.5% uptime for payment operations
- **Transaction Atomicity**: Ensure funding/withdrawal operations are fully reversible on failure
- **Error Recovery**: Automatic retry for transient failures (up to 5 attempts)
- **Circuit Breakers**: Prevent cascade failures in external API dependencies

### Observability
- **Metrics**: Funding success rates, transfer times, API error rates
- **Tracing**: End-to-end transaction tracing across Due, Alpaca, and blockchain
- **Alerts**: Failed transfers, Due API degradation, balance discrepancies
- **Dashboards**: Real-time payment flow monitoring and bottleneck identification

## Dependencies and Integrations

| Component | Technology | Version/Constraints | Purpose |
|-----------|------------|-------------------|---------|
| **Due API** | REST/GraphQL | v2.0+ | Payment processing and virtual accounts |
| **Alpaca API** | REST | v2+ | Brokerage account management and transfers |
| **Blockchain Monitors** | Web3/WebSocket | Latest | Deposit detection and confirmation |
| **PostgreSQL** | Database | 15.x+ | Transaction and account state persistence |
| **Redis** | Cache/Queue | 7.x+ | Async operation queuing and caching |

## Acceptance Criteria (Authoritative)

1. **Virtual Account Creation**: Users can create and manage virtual accounts through Due API
2. **Deposit Processing**: System automatically processes USDC deposits and converts to USD via Due
3. **Brokerage Funding**: USD transfers successfully credit Alpaca brokerage accounts
4. **Withdrawal Processing**: Users can withdraw USD from brokerage to USDC via Due
5. **Recipient Management**: Users can add, edit, and manage withdrawal recipients
6. **Balance Synchronization**: Real-time balance updates across payment and brokerage accounts
7. **Error Handling**: Comprehensive error handling with user-friendly messaging
8. **Multi-Chain Support**: Support deposits from at least 2 blockchain networks
9. **Transaction Tracking**: Complete audit trail for all payment operations
10. **KYC Validation**: Enhanced KYC checks for high-value payment operations

## Traceability Mapping

| AC # | Acceptance Criteria | Spec Section | Component/API | Test Case |
|------|-------------------|---------------|---------------|-----------|
| 1 | Virtual Account Creation | APIs and Interfaces | Funding Service, Due API | `test_virtual_account_creation` |
| 2 | Deposit Processing | Workflows and Sequencing | Payment Processing, Due API | `test_deposit_processing` |
| 3 | Brokerage Funding | Workflows and Sequencing | Funding Service, Alpaca API | `test_brokerage_funding` |
| 4 | Withdrawal Processing | Workflows and Sequencing | Payment Processing, Due API | `test_withdrawal_processing` |
| 5 | Recipient Management | APIs and Interfaces | Funding Service, Recipients API | `test_recipient_management` |
| 6 | Balance Synchronization | Data Models | Virtual accounts, balances sync | `test_balance_sync` |
| 7 | Error Handling | Reliability NFRs | Error handling, retry logic | `test_error_handling` |
| 8 | Multi-Chain Support | Workflows and Sequencing | Blockchain monitors, multi-chain | `test_multi_chain_support` |
| 9 | Transaction Tracking | Observability | Audit logging, transaction history | `test_transaction_tracking` |
| 10 | KYC Validation | Security NFRs | KYC validation, compliance | `test_kyc_validation` |

## Risks, Assumptions, Open Questions

### Risks
- **Due API Reliability**: New API dependency could introduce payment processing delays
- **Virtual Account Complexity**: Additional abstraction layer increases system complexity
- **Regulatory Changes**: Payment processing regulations could impact Due API capabilities
- **Alpaca Integration**: Changes to Alpaca API could break brokerage funding flows

### Assumptions
- Due API will provide stable, production-ready payment processing capabilities
- Virtual account creation and management will work seamlessly with Alpaca
- Multi-chain deposit detection will integrate cleanly with existing blockchain monitors
- Enhanced KYC requirements won't significantly impact user experience

### Open Questions
- What specific Due API endpoints and authentication methods are required?
- How should virtual accounts be mapped to brokerage accounts?
- What additional compliance requirements come with enhanced payment features?
- How to handle international transfers and currency conversion complexities?

## Test Strategy Summary

### Test Levels
- **Unit Tests**: Individual service methods and Due API client interactions
- **Integration Tests**: End-to-end payment flows with mocked external APIs
- **API Tests**: GraphQL endpoint testing with various payment scenarios
- **Contract Tests**: Due API and Alpaca API integration verification
- **End-to-End Tests**: Complete funding/withdrawal flows in staging environment

### Test Frameworks
- **Unit**: Go testing with testify/assert and mocked Due API responses
- **Integration**: Test containers with real PostgreSQL and mocked external APIs
- **API**: GraphQL client testing with real schema validation
- **E2E**: Cypress for frontend payment flow testing

### Coverage Goals
- 85%+ code coverage for payment processing services
- 100% coverage of acceptance criteria and critical payment paths
- All error scenarios and edge cases covered
- Performance testing under load for concurrent payment operations

### Test Environments
- **Local**: Docker Compose with mocked Due/Alpaca APIs
- **CI/CD**: Automated testing on every PR with integration test suites
- **Staging**: Full environment with sandbox Due/Alpaca APIs
- **Production**: Gradual rollout with feature flags and monitoring
