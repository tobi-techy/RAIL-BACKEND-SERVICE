<story-context id="bmad/bmm/workflows/4-implementation/story-context/template" v="1.0">
  <metadata>
    <epicId>2</epicId>
    <storyId>4</storyId>
    <title>Due Withdrawal Integration</title>
    <status>drafted</status>
    <generatedAt>2025-11-06</generatedAt>
    <generator>BMAD Story Context Workflow</generator>
    <sourceStoryPath>docs/stories/2-4-due-withdrawal-integration.md</sourceStoryPath>
  </metadata>

  <story>
    <asA>user</asA>
    <iWant>withdraw USD from my Alpaca brokerage account to USDC on the blockchain</iWant>
    <soThat>access my funds as stablecoins</soThat>
    <tasks>- [ ] Withdrawal API endpoint implementation
  - [ ] GraphQL mutation for withdrawal request
  - [ ] Input validation (amount, address format)
  - [ ] Balance check against buying power
- [ ] Broker withdrawal orchestration
  - [ ] Alpaca API integration for USD withdrawal
  - [ ] Virtual account management
  - [ ] Async processing with SQS queue
- [ ] Due on-ramp integration
  - [ ] API calls for USD to USDC conversion
  - [ ] Handle multiple blockchain chains (Ethereum, Solana)
- [ ] On-chain transfer
  - [ ] USDC transfer to user wallet
  - [ ] Transaction monitoring and confirmation
- [ ] Error handling and retries
  - [ ] Circuit breaker for external APIs
  - [ ] Compensation logic for failed steps
  - [ ] User notification system
- [ ] Testing implementation
  - [ ] Unit tests for withdrawal logic
  - [ ] Integration tests with mocked APIs
  - [ ] End-to-end withdrawal flow test</tasks>
  </story>

  <acceptanceCriteria>1. User can initiate withdrawal request from mobile app specifying amount and target blockchain address
2. System validates sufficient buying power in Alpaca account
3. Alpaca brokerage account debits the requested USD amount to the virtual account
4. Due API on-ramps USD from virtual account to USDC
5. System transfers USDC to user's specified blockchain address
6. User receives confirmation when withdrawal is complete
7. End-to-end withdrawal success rate >99%</acceptanceCriteria>

  <artifacts>
    <docs>{"docs":{"entries":[{"path":"docs/PRD.md","title":"Product Requirements Document","section":"Functional Requirements","snippet":"**NEW:** Handle the reverse flow: **USD (Alpaca) -> USDC (Due)** for withdrawals."},{"path":"docs/architecture.md","title":"System Architecture","section":"7.4 Withdrawal Flow","snippet":"Sequence diagram showing the withdrawal flow from Alpaca brokerage account through Due on-ramp to blockchain transfer."},{"path":"docs/architecture.md","title":"System Architecture","section":"5.1 Component List","snippet":"Funding Service Module responsibilities including withdrawal orchestration."},{"path":"docs/epics.md","title":"Epic Breakdown","section":"Epic 2","snippet":"Stablecoin Funding Flow including withdrawal functionality."},{"path":"docs/DUE_API_IMPLEMENTATION_GUIDE.md","title":"Due API Guide","section":"Withdrawal Channels","snippet":"API documentation for Due withdrawal functionality and virtual accounts."}]}}</docs>
    <code>{"code":{"entries":[{"path":"internal/domain/services/funding/service.go","kind":"service","symbol":"Service","lines":"1-479","reason":"Main funding service handling deposit/withdrawal orchestration"},{"path":"internal/domain/entities/funding_job_entities.go","kind":"entity","symbol":"FundingEventJob","lines":"1-203","reason":"Entity for tracking asynchronous funding/withdrawal jobs"},{"path":"internal/adapters/due/adapter.go","kind":"adapter","symbol":"DueAdapter","lines":"","reason":"Due API integration for on-ramp/off-ramp operations"},{"path":"internal/adapters/alpaca/adapter.go","kind":"adapter","symbol":"AlpacaAdapter","lines":"","reason":"Alpaca API integration for brokerage operations"},{"path":"internal/infrastructure/database/database.go","kind":"infrastructure","symbol":"Database","lines":"","reason":"PostgreSQL database connection and migrations"}]}}</code>
    <dependencies>{"dependencies":{"entries":[{"ecosystem":"go","packages":[{"name":"github.com/gin-gonic/gin","version":"v1.11.0","purpose":"Web framework for API endpoints"},{"name":"github.com/lib/pq","version":"v1.10.9","purpose":"PostgreSQL driver"},{"name":"github.com/go-redis/redis/v8","version":"v8.11.5","purpose":"Redis caching"},{"name":"github.com/sony/gobreaker","version":"v1.0.0","purpose":"Circuit breaker for external API resilience"},{"name":"go.uber.org/zap","version":"v1.27.0","purpose":"Structured logging"}]}]}}</dependencies>
  </artifacts>

  <constraints>Asynchronous orchestration using SQS for multi-step withdrawal flow
Circuit breaker pattern for Alpaca and Due API calls
Saga pattern for compensating failed withdrawal steps
Go module structure following internal/core/funding/ pattern
PostgreSQL database with withdrawals table
AWS SQS for async processing
Redis for caching
Due API for USD to USDC conversion
Alpaca API for brokerage account operations
Support for Ethereum and Solana chains
End-to-end testing required for 99% success rate</constraints>
  <interfaces>{"interfaces":{"entries":[{"name":"InitiateWithdrawal","kind":"GraphQL mutation","signature":"initiateWithdrawal(amount: Float!, chain: Chain!, address: String!): WithdrawalResponse","path":"internal/api/handlers/funding_investing_handlers.go","reason":"API endpoint for withdrawal requests"},{"name":"DueAdapter.CreateVirtualAccount","kind":"function","signature":"CreateVirtualAccount(ctx context.Context, userID uuid.UUID, alpacaAccountID string) (*entities.VirtualAccount, error)","path":"internal/adapters/due/adapter.go","reason":"Create virtual account for withdrawal processing"},{"name":"AlpacaAdapter.CreateJournal","kind":"function","signature":"CreateJournal(ctx context.Context, req *entities.AlpacaJournalRequest) (*entities.AlpacaJournalResponse, error)","path":"internal/adapters/alpaca/adapter.go","reason":"Debit USD from brokerage account"},{"name":"FundingService.ProcessWithdrawal","kind":"service method","signature":"ProcessWithdrawal(ctx context.Context, req *entities.WithdrawalRequest) error","path":"internal/domain/services/funding/service.go","reason":"Main withdrawal processing logic"}]}}</interfaces>
  <tests>
    <standards>Unit tests for isolated service methods using testify and gomock
Integration tests with Testcontainers for database and external API mocking
End-to-end tests covering the complete withdrawal flow
Test coverage >80% for core business logic
Circuit breaker testing for external API failures
Asynchronous processing testing with SQS simulation</standards>
    <locations>Unit tests in same package as code (_test.go files)
Integration tests in test/integration/ directory
E2E tests in test/e2e/ directory
Test fixtures in test/testdata/ directory</locations>
    <ideas>1. Test withdrawal request validation (invalid amounts, addresses)
2. Test insufficient balance rejection
3. Test Alpaca API journal creation for USD debit
4. Test Due API on-ramp processing
5. Test USDC transfer to user wallet
6. Test circuit breaker activation on API failures
7. Test compensation logic for failed withdrawals
8. Test async processing with SQS message handling
9. Test end-to-end flow with mocked external APIs</ideas>
  </tests>
</story-context>
