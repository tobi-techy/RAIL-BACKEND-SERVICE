<story-context id="bmad/bmm/workflows/4-implementation/story-context/template" v="1.0">
  <metadata>
    <epicId>2</epicId>
    <storyId>1</storyId>
    <title>Virtual Account Creation</title>
    <status>drafted</status>
    <generatedAt>2025-11-03</generatedAt>
    <generator>BMAD Story Context Workflow</generator>
    <sourceStoryPath>docs/stories/2-1-virtual-account-creation.md</sourceStoryPath>
  </metadata>

  <story>
    <asA>STACK user preparing to fund my brokerage account</asA>
    <iWant>create and manage a virtual account through the Due API</iWant>
    <soThat>securely process USDC to USD conversions for instant trading</soThat>
    <tasks>- [ ] Implement Due API client for virtual account creation
  - [ ] Add Due API authentication and configuration
  - [ ] Create API client methods for virtual account operations
  - [ ] Implement request/response models for Due API
- [ ] Create virtual_accounts database table
  - [ ] Define table schema with required fields
  - [ ] Add foreign key relationships to users table
  - [ ] Create database migration scripts
- [ ] Implement GraphQL mutation for virtual account creation
  - [ ] Define GraphQL schema for CreateVirtualAccount mutation
  - [ ] Implement resolver logic in Funding Service
  - [ ] Add input validation and error handling
- [ ] Add virtual account status tracking
  - [ ] Implement status enum (creating, active, inactive)
  - [ ] Add status update mechanisms
  - [ ] Handle async status updates from Due API webhooks
- [ ] Implement brokerage account linking
  - [ ] Add brokerage_account_id field to virtual accounts
  - [ ] Create linking logic between virtual and brokerage accounts
  - [ ] Validate Alpaca account ownership before linking
- [ ] Add comprehensive error handling and retry logic
  - [ ] Handle Due API failures with exponential backoff
  - [ ] Implement circuit breaker for Due API calls
  - [ ] Add user-friendly error messages and logging</tasks>
  </story>

  <acceptanceCriteria>1. Users can initiate virtual account creation via GraphQL mutation
2. Due API successfully creates virtual account with unique ID
3. Virtual account status is tracked and updated in database
4. Virtual account can be linked to Alpaca brokerage account
5. Users receive confirmation of virtual account creation
6. Virtual account creation handles errors gracefully with retry logic
7. Virtual account data is properly persisted in PostgreSQL</acceptanceCriteria>

  <artifacts>
    <docs>
      <artifact>
        <path>docs/tech-spec-epic-2.md</path>
        <title>Epic Technical Specification: Stablecoin Funding Flow</title>
        <section>Detailed Design</section>
        <snippet>Virtual Account Creation and Management. Funding Service handles virtual accounts, Due API integration for USDC/USD conversion</snippet>
        <reason>Technical specification for virtual account implementation and Due API integration</reason>
      </artifact>
      <artifact>
        <path>docs/prd.md</path>
        <title>Product Requirements Document</title>
        <section>Stablecoin Funding Flow</section>
        <snippet>Create virtual accounts linked to Alpaca brokerage accounts for each user</snippet>
        <reason>Business requirements for virtual account functionality</reason>
      </artifact>
      <artifact>
        <path>docs/architecture.md</path>
        <title>STACK Architecture Document</title>
        <section>Funding Service Module</section>
        <snippet>Orchestrates payment flows, virtual accounts, recipient management. Dependencies: Due API, Alpaca API</snippet>
        <reason>Architecture patterns for Funding Service and virtual account management</reason>
      </artifact>
      <artifact>
        <path>docs/architecture.md</path>
        <title>Data Models</title>
        <section>virtual_accounts table</section>
        <snippet>CREATE TABLE virtual_accounts (id UUID, user_id UUID, due_account_id VARCHAR, brokerage_account_id VARCHAR, status ENUM)</snippet>
        <reason>Database schema for virtual accounts storage</reason>
      </artifact>
    </docs>
    <code>
      <artifact>
        <path>internal/core/funding/service.go</path>
        <kind>service</kind>
        <symbol>CreateVirtualAccount</symbol>
        <lines>1-50</lines>
        <reason>Core service method for virtual account creation</reason>
      </artifact>
      <artifact>
        <path>internal/adapters/due/client.go</path>
        <kind>adapter</kind>
        <symbol>CreateVirtualAccount</symbol>
        <lines>1-100</lines>
        <reason>Due API client for virtual account operations</reason>
      </artifact>
      <artifact>
        <path>internal/api/graphql/resolvers/funding.go</path>
        <kind>resolver</kind>
        <symbol>CreateVirtualAccount</symbol>
        <lines>1-30</lines>
        <reason>GraphQL resolver for virtual account creation mutation</reason>
      </artifact>
      <artifact>
        <path>internal/persistence/postgres/virtual_account_repository.go</path>
        <kind>repository</kind>
        <symbol>Create</symbol>
        <lines>1-40</lines>
        <reason>Database operations for virtual account persistence</reason>
      </artifact>
    </code>
    <dependencies>
      <dependency>
        <name>github.com/google/uuid</name>
        <version>v1.6.0</version>
        <purpose>UUID generation for virtual account IDs</purpose>
      </dependency>
      <dependency>
        <name>github.com/lib/pq</name>
        <version>v1.10.9</version>
        <purpose>PostgreSQL database driver for virtual accounts</purpose>
      </dependency>
      <dependency>
        <name>github.com/go-resty/resty/v2</name>
        <version>v2.12.0</version>
        <purpose>HTTP client for Due API communication</purpose>
      </dependency>
      <dependency>
        <name>github.com/eapache/go-resiliency</name>
        <version>v1.6.0</version>
        <purpose>Circuit breaker and retry logic for Due API</purpose>
      </dependency>
    </dependencies>
  </artifacts>

  <constraints>Follow Adapter Pattern for Due API integration. Use Repository Pattern for database access. Implement circuit breaker for Due API calls. Use exponential backoff for retries. Validate all input at API boundary. Handle errors gracefully without exposing internals. Use structured JSON logging with correlation IDs. Implement proper status tracking for async operations.</constraints>
  <interfaces>
    <interface>
      <name>FundingService.CreateVirtualAccount</name>
      <kind>service-method</kind>
      <signature>func (s *Service) CreateVirtualAccount(ctx context.Context, userID uuid.UUID) (*entities.VirtualAccount, error)</signature>
      <path>internal/core/funding/service.go</path>
    </interface>
    <interface>
      <name>DueAdapter.CreateVirtualAccount</name>
      <kind>adapter-method</kind>
      <signature>func (a *Adapter) CreateVirtualAccount(ctx context.Context, userID string) (*due.VirtualAccountResponse, error)</signature>
      <path>internal/adapters/due/client.go</path>
    </interface>
    <interface>
      <name>GraphQL Mutation</name>
      <kind>graphql-mutation</kind>
      <signature>createVirtualAccount: VirtualAccount!</signature>
      <path>api/graphql/schema.graphql</path>
    </interface>
    <interface>
      <name>VirtualAccountRepository.Create</name>
      <kind>repository-method</kind>
      <signature>func (r *Repository) Create(ctx context.Context, account *entities.VirtualAccount) error</signature>
      <path>internal/persistence/postgres/virtual_account_repository.go</path>
    </interface>
  </interfaces>
  <tests>
    <standards>Unit tests for service methods and API clients, integration tests for database operations and GraphQL endpoints, API contract tests for Due API. Use testify/assert for assertions. Test error conditions, async operations, and circuit breaker behavior.</standards>
    <locations>internal/core/funding/*_test.go, internal/adapters/due/*_test.go, internal/api/graphql/*_test.go, internal/persistence/postgres/*_test.go</locations>
    <ideas>1. Test virtual account creation via GraphQL mutation - AC 1, 5
2. Test Due API virtual account creation with success/failure - AC 2, 6
3. Test virtual account status tracking and database persistence - AC 3, 7
4. Test brokerage account linking validation - AC 4
5. Test error handling and retry logic for API failures - AC 6
6. Test circuit breaker activation on repeated failures - AC 6</ideas>
  </tests>
</story-context>
