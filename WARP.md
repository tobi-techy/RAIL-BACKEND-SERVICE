# STACK Service - Project Rules

## Project Overview
STACK is a **Go-based modular monolith** deployed on AWS Fargate that provides a hybrid Web3-to-TradFi investment platform. The system integrates **Circle Developer-Controlled Wallets** for wallet management/funding and **Alpaca** as the brokerage partner, exposing a unified GraphQL API to a React Native mobile app.

## Architectural Principles

### Modular Monolith Architecture
- Single Go application with strongly defined internal boundaries
- Organized by domain modules: Onboarding, Wallet, Funding, Investing, AI-CFO
- Each module communicates internally via Go interfaces
- Design supports future extraction into microservices

### Design Patterns (MANDATORY)
- **Repository Pattern**: Abstract all database interactions via Go interfaces
- **Adapter Pattern**: All external partner integrations (Circle, Alpaca, 0G) must use interfaces with concrete implementations
- **Asynchronous Orchestration (Sagas)**: Complex multi-step flows (funding, withdrawal) use event-driven approach with SQS
- **Circuit Breaker**: Use `gobreaker` for all critical external dependencies
- **Idempotency**: All SQS message handlers and critical endpoints must be idempotent

## Technology Stack (MANDATORY)

### Backend
- **Language**: Go 1.21.x
- **Web Framework**: Gin v1.11.0
- **Database**: PostgreSQL 15.x via `lib/pq` driver
- **API**: GraphQL (gqlgen library)
- **Cache**: Redis 7.x (AWS ElastiCache)
- **Queue**: AWS SQS
- **Logging**: Zap (structured JSON)
- **Tracing**: OpenTelemetry
- **Metrics**: Prometheus Client
- **Circuit Breaker**: gobreaker
- **API Documentation**: gin-swagger

### Infrastructure
- **Cloud**: AWS (Fargate/ECS, RDS, ElastiCache, SQS, Secrets Manager, S3, CloudFront, WAF)
- **IaC**: Terraform 1.6.x
- **CI/CD**: GitHub Actions
- **Containerization**: Docker

### External Partners
- **Identity**: Auth0/Cognito (TBD)
- **Wallet/Funding**: Circle API (Developer Wallets, USDC On/Off-Ramp)
- **Brokerage**: Alpaca API (Stock/Options trading, Custody)
- **AI/Storage**: 0G

## Project Structure (MUST FOLLOW)

```
stack-monorepo/
├── cmd/api/                    # Main application entrypoint (main.go)
├── internal/                   # Private application code
│   ├── api/                    # GraphQL handlers, resolvers, middleware
│   ├── core/                   # Business logic modules
│   │   ├── onboarding/
│   │   ├── wallet/
│   │   ├── funding/
│   │   ├── investing/
│   │   └── aicfo/
│   ├── adapters/               # External service integrations
│   │   ├── circle/
│   │   ├── Alpaca/
│   │   ├── authprovider/
│   │   ├── kycprovider/
│   │   └── zerog/
│   ├── persistence/            # Database repositories
│   │   ├── postgres/
│   │   └── migrations/
│   └── config/                 # Configuration management
├── pkg/common/                 # Shared utilities, types, errors
├── infrastructure/aws/         # Terraform IaC
├── api/graph/                  # GraphQL schema files
└── scripts/                    # Build, test, deployment scripts
```

## Core Data Models

### Key Entities
- **users**: End-user with KYC status, auth info, passcode
- **wallets**: Circle Developer-Controlled Wallets per chain
- **deposits**: Incoming USDC deposits with status tracking
- **withdrawals**: USD withdrawal requests with multi-step status
- **balances**: Brokerage buying power at Alpaca
- **baskets**: Curated investment portfolios with composition
- **orders**: Buy/sell requests to Alpaca
- **positions_cache**: Current holdings (derived/cached from Alpaca)
- **ai_summaries**: Generated AI CFO summaries

### Enums (Use PostgreSQL ENUM types)
- `kyc_status`: not_started, pending, approved, rejected
- `deposit_status`: pending_confirmation, confirmed_on_chain, off_ramp_initiated, off_ramp_complete, broker_funded, failed
- `withdrawal_status`: pending, broker_withdrawal_initiated, broker_withdrawal_complete, on_ramp_initiated, on_ramp_complete, transfer_initiated, complete, failed
- `order_status`: pending, accepted_by_broker, partially_filled, filled, failed, canceled
- `asset_type`: basket, option, stock, etf
- `chain_type`: ethereum, solana

## Critical Coding Standards (AI AGENT REQUIREMENTS)

### Error Handling (MANDATORY)
- **NEVER** ignore errors - check every error returned
- Wrap errors with context: `fmt.Errorf("operation failed: %w", err)`
- Define custom error types for business logic: `var ErrInsufficientFunds = errors.New("insufficient funds")`
- Top-level handlers (Gin middleware, GraphQL resolvers) catch, log, and return appropriate responses

### Context Propagation (MANDATORY)
- Pass `context.Context` as first argument to functions in requests/background tasks
- Use context for deadlines, cancellation, and request-scoped values (correlation IDs)
- Include OpenTelemetry trace IDs in context for correlation

### Database Access (MANDATORY)
- **MUST** use Repository pattern from `internal/persistence/`
- **NEVER** use `lib/pq` directly in business logic (`internal/core/`)
- Use database transactions for atomic operations
- Ensure idempotency in data operations

### External API Calls (MANDATORY)
- **MUST** use Adapters from `internal/adapters/`
- **NEVER** make direct HTTP calls from core business logic
- Implement exponential backoff retries for transient errors
- Use circuit breakers for critical dependencies (Alpaca, Circle)
- Configure appropriate timeouts for all external requests
- Translate partner-specific errors into standardized internal error types

### Configuration (MANDATORY)
- Access config **ONLY** via `internal/config/` package
- **NEVER** read environment variables directly elsewhere
- Retrieve secrets via AWS Secrets Manager or config system
- **NEVER** hardcode secrets or log them

### Logging (MANDATORY)
- Use configured **Zap** logger instance
- **NEVER** use `fmt.Println` or `log.Println`
- Use structured JSON format
- Include correlation ID from context in every log message
- Log levels: Debug, Info, Warn, Error, Fatal, Panic (default: Info in production)

### Input Validation (MANDATORY)
- Validate **ALL** input at API boundary (GraphQL resolvers/Gin handlers)
- Perform explicit validation checks
- Return user-friendly error messages (don't expose internals)

### Concurrency (MANDATORY)
- Use goroutines, channels, and sync primitives carefully
- Avoid race conditions (test with `-race` flag)
- Favor structured concurrency patterns

## Naming Conventions

- **Packages**: `lowercase`, short (e.g., `funding`, `adapters`)
- **Structs/Interfaces/Types**: `PascalCase`
- **Variables/Functions**: `camelCase` (exported start with uppercase)
- **Constants**: `PascalCase` or `UPPER_SNAKE_CASE` (be consistent)
- **Acronyms**: Treat as single words (`userID`, `apiClient`, not `UserId`, `ApiClient`)

## Test Requirements

### Unit Tests
- Use Go standard `testing` package + `stretchr/testify`
- File convention: `*_test.go` in same package
- Mock external dependencies using interfaces
- Cover primary success paths, error conditions, edge cases
- Follow Arrange-Act-Assert pattern
- Target >80% coverage for core business logic

### Integration Tests
- Use **Testcontainers** for PostgreSQL and Redis
- Use localstack or Testcontainers for SQS
- Use WireMock or `go-vcr` for external API mocks
- Test module interactions and external dependencies

### CI Requirements
- Run `go test ./... -race` on every PR
- Must pass before merge
- Include `golangci-lint` checks
- Include security tests with `gosec`

## Key Workflows

### Funding Flow (USDC → USD → Alpaca)
1. User deposits USDC to Circle wallet
2. Circle webhook notifies backend → log deposit
3. Async: Initiate Circle off-ramp (USDC → USD)
4. Circle confirms → enqueue broker funding task
5. Async: Fund Alpaca account
6. Alpaca confirms → update balance, notify user

### Withdrawal Flow (Alpaca → USD → USDC)
1. User requests withdrawal
2. Async: Alpaca USD withdrawal
3. Alpaca confirms → enqueue Circle on-ramp
4. Async: Circle converts USD → USDC
5. Async: Transfer USDC on-chain to user address
6. Circle confirms → update status, notify user

### Investment Flow
1. User places order (basket/option)
2. Check balance at Alpaca
3. Create order record → submit to Alpaca
4. Alpaca accepts → update order status
5. Alpaca fill webhook → update order, cache positions

## Observability Requirements

### Tracing with OpenTelemetry
- Start and propagate spans across all service boundaries
- Attach `context.Context` to spans, logs, metrics
- Record important attributes (request params, user ID, errors)
- Use middleware for automatic HTTP/GraphQL instrumentation
- Annotate critical/slow paths with custom spans

### Metrics with Prometheus
- Monitor: request latency, throughput, error rate, resource usage
- Define SLIs (e.g., request latency < 300ms)
- Avoid excessive cardinality in labels
- Alert on critical conditions (5xx rates, DB errors, timeouts)

### Logging Standards
- JSON format for ingestion
- Include trace IDs for correlation
- Use appropriate log levels
- Include service context and user context (avoid PII)

## Deployment

### Strategy
- Blue/Green deployments on AWS Fargate (ECS)
- Traffic shifting via ALB target groups
- Zero-downtime releases

### Environments
- **Local**: Docker Compose (Postgres, Redis, mocks)
- **Staging**: AWS, partner sandbox APIs, scaled down
- **Production**: AWS, partner production APIs, HA configured

### Rollback
- Use ECS Blue/Green capabilities
- Keep traffic on previous version if health checks fail
- Database rollbacks via migration tooling
- Ensure migrations are backward compatible

## Security

- Apply input validation and sanitization rigorously
- Use secure defaults for JWT, cookies, config
- Isolate sensitive operations with permission boundaries
- Implement retries, exponential backoff, timeouts on external calls
- Use circuit breakers and rate limiting
- Consider distributed rate-limiting with Redis
- Never log or expose secrets
- Store secrets in AWS Secrets Manager

## Additional Standards

- Follow standard Go formatting (`gofmt`, `goimports`)
- Use `golangci-lint` with repo configuration
- Follow effective Go conventions
- Document public functions/packages with GoDoc-style comments
- Maintain READMEs for services and libraries
- Keep ARCHITECTURE.md up-to-date
