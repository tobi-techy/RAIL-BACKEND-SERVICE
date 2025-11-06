<story-context id="bmad/bmm/workflows/4-implementation/story-context/template" v="1.0">
  <metadata>
    <epicId>2</epicId>
    <storyId>3</storyId>
    <title>alpaca-account-funding</title>
    <status>drafted</status>
    <generatedAt>2025-11-06</generatedAt>
    <generator>BMAD Story Context Workflow</generator>
    <sourceStoryPath>docs/stories/2-3-alpaca-account-funding.md</sourceStoryPath>
  </metadata>

  <story>
    <asA>As a user,</asA>
    <iWant>I want the USD from my stablecoin off-ramp to be securely transferred to my linked Alpaca brokerage account,</iWant>
    <soThat>so that I can invest in stocks and options with instant buying power.</soThat>
    <tasks>- [ ] Implement Alpaca brokerage funding initiation after off-ramp completion (AC: 1, 2)
  - [ ] Create InitiateBrokerFunding method in Alpaca adapter
  - [ ] Add broker_funded status tracking to deposits table
  - [ ] Implement circuit breaker for Alpaca API calls
  - [ ] Write unit tests for funding initiation
  - [ ] Write integration tests with mocked Alpaca API

- [ ] Update user brokerage balance upon successful funding (AC: 3)
  - [ ] Update balances table buying_power_usd after funding confirmation
  - [ ] Send real-time notification to user via GraphQL subscription or push notification
  - [ ] Write unit tests for balance updates
  - [ ] Write integration tests for balance synchronization

- [ ] Add comprehensive error handling and retry logic (AC: 4)
  - [ ] Implement Alpaca-specific error parsing and mapping to internal error types
  - [ ] Add structured logging for all funding steps using Zap logger
  - [ ] Implement user notification system for failed funding attempts
  - [ ] Add metrics collection for funding success/failure rates
  - [ ] Write tests for error scenarios and retry logic

- [ ] Ensure complete audit trail and compliance tracking (AC: 5, 6)
  - [ ] Update deposit status progression (off_ramp_complete → broker_funded)
  - [ ] Add Alpaca transaction reference IDs to deposits table
  - [ ] Implement end-to-end flow monitoring for success rate tracking
  - [ ] Write database tests for audit trail integrity
  - [ ] Write integration tests for complete funding flow</tasks>
  </story>

  <acceptanceCriteria>1. Upon completion of USDC-to-USD off-ramp via Due, the system automatically initiates a funding request to Alpaca to deposit the USD into the user's brokerage account. [Source: docs/prd.md#Functional-Requirements, docs/architecture.md#7.2-Funding-Flow]

2. The Alpaca funding process completes successfully, transferring the full USD amount and increasing the user's buying power for trading. [Source: docs/prd.md#Functional-Requirements, docs/epics.md#Epic-2]

3. The user's brokerage balance (buying_power_usd) is updated in real-time following successful Alpaca funding completion. [Source: docs/architecture.md#4.4-balances]

4. Failed Alpaca funding attempts are logged with detailed error information and retried up to 3 times with exponential backoff, with final failures triggering user notification via the app. [Source: docs/architecture.md#11.3-Error-Handling-Patterns]

5. The system maintains a complete audit trail of Alpaca funding transactions, updating deposit status to broker_funded with timestamp. [Source: docs/architecture.md#4.3-deposits]

6. End-to-end funding flow (USDC deposit → off-ramp → Alpaca funding) completes within minutes with >99% success rate. [Source: docs/epics.md#Epic-2]</acceptanceCriteria>

  <artifacts>
    <docs>
      <artifact>
        <path>docs/prd.md</path>
        <title>Product Requirements Document</title>
        <section>Functional Requirements</section>
        <snippet>Orchestrate an immediate USDC-to-USD off-ramp via Due API after deposit. Transfer off-ramped USD directly into the user's linked Alpaca brokerage account.</snippet>
      </artifact>
      <artifact>
        <path>docs/architecture.md</path>
        <title>STACK Architecture Document</title>
        <section>7.2 Funding Flow</section>
        <snippet>Data flow: User deposits USDC -> Funding Service monitors -> Due Off-Ramp -> Virtual Account -> Alpaca Deposit (USD) -> "Buying Power" updated.</snippet>
      </artifact>
      <artifact>
        <path>docs/architecture.md</path>
        <title>STACK Architecture Document</title>
        <section>4.3 deposits</section>
        <snippet>Tracks deposit status progression from pending_confirmation to broker_funded, including timestamps and transaction references.</snippet>
      </artifact>
      <artifact>
        <path>docs/architecture.md</path>
        <title>STACK Architecture Document</title>
        <section>4.4 balances</section>
        <snippet>buying_power_usd field represents user's brokerage buying power available at Alpaca, updated after successful funding.</snippet>
      </artifact>
      <artifact>
        <path>docs/architecture.md</path>
        <title>STACK Architecture Document</title>
        <section>11.3 Error Handling Patterns</section>
        <snippet>Implement retry policy with exponential backoff, circuit breaker for external API calls, structured logging for debugging.</snippet>
      </artifact>
      <artifact>
        <path>docs/architecture.md</path>
        <title>STACK Architecture Document</title>
        <section>13.2 Test Types and Organization</section>
        <snippet>Unit tests for isolated logic, integration tests for module interactions, focus on testing behavior not implementation.</snippet>
      </artifact>
    </docs>
    <code>
      <artifact>
        <path>internal/core/funding/</path>
        <kind>service</kind>
        <symbol>FundingService</symbol>
        <lines></lines>
        <reason>Core service for orchestrating funding flows including Alpaca brokerage funding after off-ramp completion.</reason>
      </artifact>
      <artifact>
        <path>internal/adapters/alpaca/</path>
        <kind>adapter</kind>
        <symbol>AlpacaAdapter</symbol>
        <lines></lines>
        <reason>External API adapter for Alpaca brokerage operations including funding deposits and balance updates.</reason>
      </artifact>
      <artifact>
        <path>internal/persistence/postgres/</path>
        <kind>repository</kind>
        <symbol>DepositRepository</symbol>
        <lines></lines>
        <reason>Database repository for managing deposit records and status updates throughout the funding flow.</reason>
      </artifact>
    </code>
    <dependencies>
      <ecosystem name="go">
        <dependency>github.com/gin-gonic/gin v1.11.0</dependency>
        <dependency>github.com/lib/pq v1.10.9</dependency>
        <dependency>github.com/sony/gobreaker v1.0.0</dependency>
        <dependency>github.com/stretchr/testify v1.11.1</dependency>
        <dependency>go.uber.org/zap v1.27.0</dependency>
      </ecosystem>
    </dependencies>
  </artifacts>

  <constraints>- Use Go 1.21.x with Gin web framework for API implementation
- Implement circuit breaker pattern for Alpaca API reliability using gobreaker
- Use Repository pattern for database access, avoiding direct lib/pq usage in business logic
- Apply structured logging with Zap, correlation IDs, and error context
- Validate all input at API boundaries with comprehensive error handling
- Follow Adapter pattern for external Alpaca API integration
- Use PostgreSQL with JSONB support for flexible data storage
- Implement asynchronous orchestration with SQS for multi-step funding flow
- Ensure idempotency for message processing and critical API endpoints
- Follow Go coding standards: camelCase variables, PascalCase types, lowercase packages</constraints>
  <interfaces>
    <interface>
      <name>InitiateBrokerFunding</name>
      <kind>function</kind>
      <signature>InitiateBrokerFunding(ctx context.Context, depositID uuid.UUID, amount decimal.Decimal) error</signature>
      <path>internal/core/funding/service.go</path>
    </interface>
    <interface>
      <name>DepositFunds</name>
      <kind>API call</kind>
      <signature>POST /api/v1/accounts/{account_id}/transfers (Alpaca ACH transfer endpoint)</signature>
      <path>internal/adapters/alpaca/client.go</path>
    </interface>
    <interface>
      <name>UpdateDepositStatus</name>
      <kind>repository method</kind>
      <signature>UpdateDepositStatus(ctx context.Context, depositID uuid.UUID, status string, timestamp time.Time) error</signature>
      <path>internal/persistence/postgres/deposit_repository.go</path>
    </interface>
  </interfaces>
  <tests>
    <standards>Unit tests for all service methods and adapters using Go testing package and testify mocking, integration tests with testcontainers for database and mocked external APIs, end-to-end tests for critical funding flow. Focus on testing behavior, not implementation details. Aim for >80% code coverage for core business logic.</standards>
    <locations>Unit tests in same package as code (*_test.go), integration tests in test/integration/, E2E tests against staging environment.</locations>
    <ideas>- AC1: Unit test for InitiateBrokerFunding method with mocked Alpaca adapter, verify correct API calls and status updates
- AC2: Integration test for complete funding flow from off-ramp completion to balance update, using testcontainers for database
- AC3: Unit test for balance update logic with mocked repository, verify buying_power_usd calculation and persistence
- AC4: Integration test for error handling and retry logic, simulate Alpaca API failures and verify exponential backoff
- AC5: Database test for audit trail integrity, verify deposit status progression and timestamp accuracy
- AC6: E2E test for end-to-end funding flow success rate monitoring and metrics collection</ideas>
  </tests>
</story-context>
