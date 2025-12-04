<story-context id="bmad/bmm/workflows/4-implementation/story-context/template" v="1.0">
  <metadata>
    <epicId>2</epicId>
    <storyId>2</storyId>
    <title>due-off-ramp-integration</title>
    <status>drafted</status>
    <generatedAt>2025-11-05</generatedAt>
    <generator>BMAD Story Context Workflow</generator>
    <sourceStoryPath>/Users/Aplle/Development/rail_service/docs/stories/2-2-due-off-ramp-integration.md</sourceStoryPath>
  </metadata>

  <story>
    <asA>user</asA>
    <iWant>deposit USDC and have it automatically off-ramped to USD via Due API</iWant>
    <soThat>the USD is transferred to my due api already created virtual account that would be linked to Alpaca broker, enabling instant buying power for trading</soThat>
    <tasks>- [ ] Implement virtual account deposit webhook handler (AC: 1)
  - [ ] Add POST endpoint for Due webhook events
  - [ ] Validate webhook signature for security
  - [ ] Parse deposit event data (amount, virtual account ID)
  - [ ] Verify deposit is for a known virtual account
  - [ ] Write unit test for webhook handler
  - [ ] Write integration test with mocked webhook payload

- [ ] Add off-ramp initiation logic to Funding Service (AC: 1, 2)
  - [ ] Create InitiateOffRamp method in DUE adapter
  - [ ] Add off-ramp status tracking to deposits table (off_ramp_initiated_at, off_ramp_completed_at)
  - [ ] Implement circuit breaker for DUE API calls
  - [ ] Add retry logic with exponential backoff
  - [ ] Write unit tests for off-ramp initiation
  - [ ] Write integration tests with mocked DUE API

- [ ] Implement Alpaca brokerage funding after off-ramp completion (AC: 3, 4)
  - [ ] Monitor off-ramp completion via DUE webhooks or polling
  - [ ] Create InitiateBrokerFunding method in Alpaca adapter
  - [ ] Update deposit status to broker_funded
  - [ ] Update user balance in balances table
  - [ ] Send real-time notification to user (via GraphQL subscription or push)
  - [ ] Write unit tests for balance updates
  - [ ] Write integration tests with mocked Alpaca API

- [ ] Add comprehensive error handling and logging (AC: 5)
  - [ ] Implement DUE-specific error parsing and mapping
  - [ ] Add structured logging for all off-ramp steps
  - [ ] Implement user notification for failed off-ramps
  - [ ] Add metrics collection for success/failure rates
  - [ ] Write tests for error scenarios

- [ ] Update database schema for off-ramp tracking (AC: 6)
  - [ ] Add off_ramp_tx_id field to deposits table
  - [ ] Add alpaca_funding_tx_id field to deposits table
  - [ ] Create database migration
  - [ ] Update repository methods
  - [ ] Write database tests</tasks>
  </story>

  <acceptanceCriteria>1. Upon receipt of a virtual account deposit event from Due (via webhook), the system initiates an off-ramp request to convert USDC to USD. [Source: docs/prd.md#Functional-Requirements, docs/architecture.md#7.2-Funding-Flow]

2. The off-ramp process completes successfully, converting the deposited USDC amount to equivalent USD. [Source: docs/prd.md#Functional-Requirements]

3. The off-ramped USD is automatically transferred to the user's linked Alpaca brokerage account, increasing their buying power. [Source: docs/prd.md#Functional-Requirements, docs/architecture.md#7.2-Funding-Flow]

4. The user's brokerage balance (buying_power_usd) is updated in real-time following successful Alpaca funding. [Source: docs/architecture.md#4.4-balances]

5. Failed off-ramp attempts are logged and retried up to 3 times with exponential backoff, with final failures triggering user notification. [Source: docs/architecture.md#11.3-Error-Handling-Patterns]

6. The system maintains audit trail of all off-ramp transactions in the deposits table. [Source: docs/architecture.md#4.3-deposits]</acceptanceCriteria>

  <artifacts>
    <docs>
      <doc>
        <path>docs/prd.md</path>
        <title>Product Requirements Document</title>
        <section>Functional Requirements</section>
        <snippet>Support deposits of USDC from Ethereum (EVM) and Solana (non-EVM) chains. Orchestrate an immediate USDC-to-USD off-ramp via Due API after deposit. Transfer off-ramped USD directly into the user's linked Alpaca brokerage account.</snippet>
      </doc>
      <doc>
        <path>docs/architecture.md</path>
        <title>STACK Architecture Document</title>
        <section>7.2 Funding Flow</section>
        <snippet>User deposits USDC -> Funding Service monitors -> Due Off-Ramp -> Virtual Account -> Alpaca Deposit. Asynchronous orchestration (Sagas) for multi-step process.</snippet>
      </doc>
      <doc>
        <path>docs/epics.md</path>
        <title>STACK MVP Epics</title>
        <section>Epic 2: Stablecoin Funding Flow</section>
        <snippet>Enable users to fund their brokerage accounts instantly with stablecoins using Due for off-ramp/on-ramp functionality. Create virtual accounts linked to Alpaca brokerage accounts. Orchestrate immediate USDC-to-USD off-ramp via Due API.</snippet>
      </doc>
    </docs>
    <code>
      <entry>
        <path>internal/core/funding/</path>
        <kind>service</kind>
        <symbol>FundingService</symbol>
        <lines></lines>
        <reason>Core service for funding operations, will contain off-ramp logic</reason>
      </entry>
      <entry>
        <path>internal/adapters/due/</path>
        <kind>adapter</kind>
        <symbol>DueAdapter</symbol>
        <lines></lines>
        <reason>DUE API integration for off-ramp operations</reason>
      </entry>
      <entry>
        <path>internal/adapters/alpaca/</path>
        <kind>adapter</kind>
        <symbol>AlpacaAdapter</symbol>
        <lines></lines>
        <reason>Alpaca API integration for brokerage funding</reason>
      </entry>
    </code>
    <dependencies>
      <go>
        <github.com/sony/gobreaker>Circuit breaker for DUE API calls</github.com/sony/gobreaker>
        <github.com/go-redis/redis/v8>Caching layer for session data</github.com/go-redis/redis/v8>
        <github.com/lib/pq>PostgreSQL driver</github.com/lib/pq>
        <go.uber.org/zap>Structured logging</go.uber.org/zap>
      </go>
    </dependencies>
  </artifacts>

  <constraints>Asynchronous orchestration (Sagas) for multi-step funding flow, Adapter Pattern for DUE and Alpaca API integration, Circuit Breaker for external API resilience, Repository Pattern for database access. Required patterns: event-driven architecture for webhooks, exponential backoff for retries. Layer restrictions: business logic in core/, external calls in adapters/, data access in persistence/. Testing requirements: 80%+ code coverage, integration tests for API interactions. Coding standards: Go formatting with gofmt, error wrapping with context, structured logging with Zap.</constraints>
  <interfaces>
    <interface>
      <name>DueOffRampAPI</name>
      <kind>REST endpoint</kind>
      <signature>POST /offramp {virtualAccountId, amount, currencyIn: USDC, currencyOut: USD}</signature>
      <path>internal/adapters/due/</path>
    </interface>
    <interface>
      <name>AlpacaFundingAPI</name>
      <kind>REST endpoint</kind>
      <signature>POST /funding {accountId, amount, currency: USD}</signature>
      <path>internal/adapters/alpaca/</path>
    </interface>
    <interface>
      <name>WebhookHandler</name>
      <kind>HTTP handler</kind>
      <signature>POST /webhooks/due/deposit {event: deposit, virtualAccountId, amount}</signature>
      <path>internal/api/handlers/funding/</path>
    </interface>
  </interfaces>
  <tests>
    <standards>Unit tests for all service methods and adapters using testify for assertions, integration tests with mocked external APIs using httptest, database tests for repository methods. Framework: Go standard testing package with testify extensions.</standards>
    <locations>test/unit/ for unit tests, test/integration/ for integration tests, testdata/ for fixture files</locations>
    <ideas>Test virtual account deposit webhook parsing and validation, test off-ramp initiation and status tracking, test Alpaca funding transfer and balance update, test error scenarios with circuit breaker activation, test retry logic with exponential backoff, test audit trail in deposits table.</ideas>
  </tests>
</story-context>
