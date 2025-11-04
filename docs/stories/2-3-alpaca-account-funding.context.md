<story-context id="bmad/bmm/workflows/4-implementation/story-context/template" v="1.0">
  <metadata>
    <epicId>2</epicId>
    <storyId>3</storyId>
    <title>Alpaca Account Funding</title>
    <status>drafted</status>
    <generatedAt>2025-11-03</generatedAt>
    <generator>BMAD Story Context Workflow</generator>
    <sourceStoryPath>docs/stories/2-3-alpaca-account-funding.md</sourceStoryPath>
  </metadata>

  <story>
    <asA>As a user who has converted USDC to USD in my virtual account,</asA>
    <iWant>I want the system to automatically transfer USD from my virtual account to my linked Alpaca brokerage account,</iWant>
    <soThat>so that I can immediately access the funds for trading stocks and options.</soThat>
    <tasks>- [ ] Implement Alpaca API transfer functionality (AC: #3, #4)
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
  - [ ] Add metrics for funding success rates and timing</tasks>
  </story>

  <acceptanceCriteria>1. **Virtual Account Linking**: Users can link their virtual accounts to Alpaca brokerage accounts
2. **Balance Detection**: System detects when virtual account has available USD balance
3. **Alpaca Transfer Initiation**: System creates Alpaca account funding request for available USD
4. **Balance Update**: User's Alpaca buying power is updated upon successful funding
5. **Status Tracking**: Transfer status is tracked from 'broker_funded_initiated' to 'broker_funded_complete'
6. **Error Handling**: Failed Alpaca transfers are retried with exponential backoff and user notifications
7. **Audit Logging**: All Alpaca API interactions are logged with correlation IDs for troubleshooting</acceptanceCriteria>

  <artifacts>
    <docs>
      <entry>
        <path>docs/tech-spec-epic-2.md</path>
        <title>Epic Technical Specification: Stablecoin Funding Flow</title>
        <section>Acceptance Criteria #3: Brokerage Funding</section>
        <snippet>USD transfers successfully credit Alpaca brokerage accounts</snippet>
      </entry>
      <entry>
        <path>docs/architecture.md</path>
        <title>STACK Architecture Document</title>
        <section>Data Flow (Funding)</section>
        <snippet>Virtual Account -> Alpaca Deposit (USD) -> "Buying Power" updated</snippet>
      </entry>
      <entry>
        <path>docs/prd/epic-2-stablecoin-funding-flow.md</path>
        <title>PRD: Stablecoin Funding Flow</title>
        <section>Functional Requirements</section>
        <snippet>Transfer off-ramped USD directly into the user's linked Alpaca brokerage account to create "buying power"</snippet>
      </entry>
    </docs>
    <code>
      <entry>
        <path>internal/domain/services/funding/service.go</path>
        <kind>service</kind>
        <symbol>FundingService</symbol>
        <lines>1-100</lines>
        <reason>Core funding service that orchestrates Alpaca account funding workflow</reason>
      </entry>
      <entry>
        <path>internal/adapters/alpaca/client.go</path>
        <kind>adapter</kind>
        <symbol>Client</symbol>
        <lines>1-50</lines>
        <reason>Existing Alpaca API client that needs extension for funding methods</reason>
      </entry>
      <entry>
        <path>internal/infrastructure/repositories/virtual_account_repository.go</path>
        <kind>repository</kind>
        <symbol>VirtualAccountRepository</symbol>
        <lines>1-50</lines>
        <reason>Virtual account management for brokerage account linking</reason>
      </entry>
      <entry>
        <path>internal/infrastructure/repositories/deposit_repository.go</path>
        <kind>repository</kind>
        <symbol>DepositRepository</symbol>
        <lines>1-50</lines>
        <reason>Deposit status tracking that needs extension for brokerage funding states</reason>
      </entry>
      <entry>
        <path>internal/infrastructure/repositories/balances_repository.go</path>
        <kind>repository</kind>
        <symbol>BalancesRepository</symbol>
        <lines>1-50</lines>
        <reason>Balance updates for Alpaca buying power synchronization</reason>
      </entry>
    </code>
    <dependencies>
      <entry>
        <ecosystem>go</ecosystem>
        <package>github.com/sony/gobreaker</package>
        <version>v1.0.0</version>
        <purpose>Circuit breaker for Alpaca API resilience</purpose>
      </entry>
      <entry>
        <ecosystem>go</ecosystem>
        <package>go.uber.org/zap</package>
        <version>v1.27.0</version>
        <purpose>Structured logging for audit trails</purpose>
      </entry>
      <entry>
        <ecosystem>go</ecosystem>
        <package>github.com/google/uuid</package>
        <version>v1.6.0</version>
        <purpose>Correlation ID generation</purpose>
      </entry>
      <entry>
        <ecosystem>go</ecosystem>
        <package>github.com/shopspring/decimal</package>
        <version>v1.4.0</version>
        <purpose>Precise decimal arithmetic for financial amounts</purpose>
      </entry>
    </dependencies>
  </artifacts>

  <constraints>- Adapter Pattern: All Alpaca API calls must go through internal/adapters/alpaca/ client
- Repository Pattern: Database access must use internal/infrastructure/repositories/
- Circuit Breaker: Alpaca API calls must be protected with gobreaker
- Exponential Backoff: Failed funding transfers must retry with configurable backoff
- Correlation IDs: All API interactions must include correlation IDs for tracing
- Structured Logging: Use Zap logger with JSON format for audit logging
- Transaction Boundaries: Multi-step operations must use database transactions
- Idempotency: Funding operations must be idempotent to handle retries
- Input Validation: All external inputs must be validated at API boundaries
- Error Wrapping: Errors must be wrapped with context using fmt.Errorf with %w</constraints>

  <interfaces>- FundingService.InitiateAlpacaFunding(ctx, virtualAccountID, usdAmount) (transferID, error)
- AlpacaAdapter.FundAccount(ctx, accountID, amount, currency) (transferRef, error)
- AlpacaAdapter.GetTransferStatus(ctx, transferID) (status, error)
- DepositRepository.UpdateBrokerageFundingStatus(ctx, depositID, status, transferRef) error
- VirtualAccountRepository.GetByDueAccountID(ctx, dueAccountID) (*VirtualAccount, error)
- BalancesRepository.UpdateBuyingPower(ctx, userID, newAmount) error</interfaces>

  <tests>
    <standards>- Unit tests for all exported functions with testify/assert
- Integration tests using testcontainers for PostgreSQL
- API tests for GraphQL endpoints with real schema validation
- Circuit breaker tests for failure scenarios
- Mock external APIs using httptest or go-vcr
- Race condition tests with go run -race flag
- Coverage target: 80%+ for critical business logic</standards>
    <locations>- Unit tests: *_test.go in same package as implementation
- Integration tests: test/integration/ directory
- API tests: test/api/ directory
- E2E tests: test/e2e/ directory</locations>
    <ideas>- Test AC #1: Virtual account can be successfully linked to Alpaca brokerage account
- Test AC #2: System detects available USD balance in virtual account
- Test AC #3: Alpaca funding request created for available USD amount
- Test AC #4: Alpaca buying power updated upon successful funding
- Test AC #5: Deposit status progresses through brokerage funding states
- Test AC #6: Failed funding transfers retry with exponential backoff
- Test AC #7: All Alpaca API calls logged with correlation IDs</ideas>
  </tests>
</story-context>
