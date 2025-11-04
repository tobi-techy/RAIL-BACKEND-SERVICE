<story-context id="bmad/bmm/workflows/4-implementation/story-context/template" v="1.0">
  <metadata>
    <epicId>2</epicId>
    <storyId>2</storyId>
    <title>Due Off-Ramp Integration</title>
    <status>drafted</status>
    <generatedAt>2025-11-03</generatedAt>
    <generator>BMAD Story Context Workflow</generator>
    <sourceStoryPath>docs/stories/2-2-due-off-ramp-integration.md</sourceStoryPath>
  </metadata>

  <story>
    <asA>As a user who has deposited USDC to fund my brokerage account,</asA>
    <iWant>I want the system to automatically convert my USDC to USD via Due API,</iWant>
    <soThat>so that I can instantly access trading funds in my Alpaca brokerage account.</soThat>
    <tasks>- [ ] Implement Due API transfer functionality (AC: #2, #3)
  - [ ] Extend Due API client with transfer (USDC→USD) methods
  - [ ] Add transfer request/response models to Due API adapter
  - [ ] Implement transfer status checking and webhook handling
  - [ ] Add unit tests for transfer functionality
- [ ] Enhance Funding Service with Due transfer integration (AC: #2)
  - [ ] Add InitiateDueTransfer method to FundingService interface
  - [ ] Implement transfer logic with virtual account validation
  - [ ] Update deposit status from 'confirmed_on_chain' to 'off_ramp_initiated'
  - [ ] Add integration tests for transfer initiation
- [ ] Integrate with blockchain deposit processing (AC: #1, #2)
  - [ ] Modify deposit confirmation handler to trigger Due transfers
  - [ ] Add async processing queue for off-ramp operations
  - [ ] Implement webhook handler for Due transfer completion
  - [ ] Add end-to-end tests for deposit-to-transfer flow
- [ ] Implement comprehensive error handling and resilience (AC: #5, #6)
  - [ ] Add exponential backoff retry for failed transfers
  - [ ] Implement circuit breaker protection for Due API
  - [ ] Add transfer failure notifications and status updates
  - [ ] Create integration tests for error scenarios
- [ ] Update database schema for enhanced tracking (AC: #4)
  - [ ] Add off_ramp_initiated_at timestamp to deposits table
  - [ ] Add off_ramp_completed_at timestamp to deposits table
  - [ ] Add due_transfer_reference field for tracking
  - [ ] Create and test database migration scripts
- [ ] Add monitoring and audit logging (AC: #7)
  - [ ] Implement structured logging for all Due API interactions
  - [ ] Add correlation ID tracking across transfer operations
  - [ ] Configure alerts for transfer failures and timeouts
  - [ ] Add metrics for transfer success rates and timing</tasks>
  </story>

  <acceptanceCriteria>1. **Deposit Detection**: System detects confirmed USDC deposits from supported blockchain networks (Ethereum, Solana)
2. **Due Transfer Initiation**: Upon deposit confirmation, system creates Due API transfer request to convert USDC to USD
3. **Virtual Account Crediting**: USD from Due conversion is credited to user's virtual account
4. **Status Tracking**: Deposit status is updated from 'confirmed_on_chain' to 'off_ramp_initiated' to 'off_ramp_complete'
5. **Error Handling**: Failed Due transfers are retried with exponential backoff and user notifications
6. **Circuit Breaker**: Due API calls are protected by circuit breaker to prevent cascade failures
7. **Audit Logging**: All Due API interactions are logged with correlation IDs for troubleshooting</acceptanceCriteria>

  <artifacts>
    <docs>
      <entry>
        <path>docs/tech-spec-epic-2.md</path>
        <title>Epic Technical Specification: Stablecoin Funding Flow</title>
        <section>Acceptance Criteria #2: Deposit Processing</section>
        <snippet>System automatically processes USDC deposits and converts to USD via Due</snippet>
      </entry>
      <entry>
        <path>docs/architecture.md</path>
        <title>STACK Architecture Document</title>
        <section>Data Flow (Funding)</section>
        <snippet>User deposits USDC (on-chain) -> Monitored by Funding Service -> Due Off-Ramp (USDC to USD) -> Virtual Account -> Alpaca Deposit (USD)</snippet>
      </entry>
      <entry>
        <path>docs/prd/epic-2-stablecoin-funding-flow.md</path>
        <title>PRD: Stablecoin Funding Flow</title>
        <section>Functional Requirements</section>
        <snippet>Orchestrate an immediate USDC-to-USD off-ramp via Due API</snippet>
      </entry>
    </docs>
    <code>
      <entry>
        <path>internal/domain/services/funding/service.go</path>
        <kind>service</kind>
        <symbol>DueAdapter</symbol>
        <lines>74-78</lines>
        <reason>Existing DueAdapter interface that needs extension for transfer functionality</reason>
      </entry>
      <entry>
        <path>internal/adapters/due/client.go</path>
        <kind>adapter</kind>
        <symbol>CreateVirtualAccount</symbol>
        <lines>141-170</lines>
        <reason>Existing Due API client pattern with circuit breaker and retry logic</reason>
      </entry>
      <entry>
        <path>internal/adapters/due/models.go</path>
        <kind>models</kind>
        <symbol>CreateVirtualAccountRequest</symbol>
        <lines>67-75</lines>
        <reason>Existing Due API request/response models pattern</reason>
      </entry>
      <entry>
        <path>internal/infrastructure/repositories/deposit_repository.go</path>
        <kind>repository</kind>
        <symbol>DepositRepository</symbol>
        <lines>1-50</lines>
        <reason>Existing deposit status tracking that needs extension for off-ramp states</reason>
      </entry>
      <entry>
        <path>internal/infrastructure/repositories/virtual_account_repository.go</path>
        <kind>repository</kind>
        <symbol>VirtualAccountRepository</symbol>
        <lines>1-50</lines>
        <reason>Virtual account management for USD crediting</reason>
      </entry>
    </code>
    <dependencies>
      <entry>
        <ecosystem>go</ecosystem>
        <package>github.com/sony/gobreaker</package>
        <version>v1.0.0</version>
        <purpose>Circuit breaker for Due API resilience</purpose>
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
        <package>github.com/lib/pq</package>
        <version>v1.10.9</version>
        <purpose>PostgreSQL database access</purpose>
      </entry>
    </dependencies>
  </artifacts>

  <constraints>- Adapter Pattern: All Due API calls must go through internal/adapters/due/ client
- Repository Pattern: Database access must use internal/infrastructure/repositories/
- Circuit Breaker: Due API calls must be protected with gobreaker
- Exponential Backoff: Failed transfers must retry with configurable backoff
- Correlation IDs: All API interactions must include correlation IDs for tracing
- Structured Logging: Use Zap logger with JSON format for audit logging
- Transaction Boundaries: Multi-step operations must use database transactions
- Idempotency: Transfer operations must be idempotent to handle retries
- Input Validation: All external inputs must be validated at API boundaries
- Error Wrapping: Errors must be wrapped with context using fmt.Errorf with %w</constraints>

  <interfaces>- FundingService.InitiateDueTransfer(ctx, depositID, virtualAccountID) (transferID, error)
- DueAdapter.CreateTransfer(ctx, fromAccount, toAccount, amount, currency) (transfer, error)
- DueAdapter.GetTransferStatus(ctx, transferID) (status, error)
- DepositRepository.UpdateOffRampStatus(ctx, depositID, status, transferRef) error
- BlockchainMonitor.OnDepositConfirmed(deposit) // webhook/event handler</interfaces>

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
    <ideas>- Test AC #1: Deposit detection triggers Due transfer initiation
- Test AC #2: Due API transfer converts USDC to USD successfully
- Test AC #3: Virtual account receives USD crediting
- Test AC #4: Deposit status updates through all off-ramp states
- Test AC #5: Failed transfers retry with exponential backoff
- Test AC #6: Circuit breaker prevents cascade failures during Due API outages
- Test AC #7: All Due API calls logged with correlation IDs</ideas>
  </tests>
</story-context>
