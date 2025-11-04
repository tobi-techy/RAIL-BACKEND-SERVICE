# AGENTS.md - STACK Service Development Guide

## Build/Lint/Test Commands

### Single Test Execution
- **Run a specific test**: `go test -v -run TestName ./path/to/package`
- **Run tests in a package**: `go test -v ./internal/domain/services/funding/...`
- **Run tests with race detection**: `go test -race ./internal/adapters/alpaca/...`
- **Run tests with coverage**: `go test -cover -coverprofile=coverage.out ./pkg/...`

### Comprehensive Testing
- **Full test suite**: `make test` (runs `./test/run_tests.sh`)
- **Unit tests only**: `make test-unit` or `./test/run_tests.sh --unit-only`
- **Integration tests only**: `make test-integration` or `./test/run_tests.sh --integration-only`
- **Tests with coverage report**: `make test-cover` or `./test/run_tests.sh --verbose`
- **Tests with race detection**: `make test-race`

### Build & Development
- **Build**: `make build` (outputs to `bin/stack_service`)
- **Run locally**: `make run` (uses `configs/config.yaml` or env vars)
- **Format code**: `make format` (runs `go fmt` and `goimports`)
- **Lint code**: `make lint` (runs `golangci-lint`)
- **Docker build**: `make docker-build` and `make docker-run`

### Database & Infrastructure
- **Run migrations**: `make migrate-up` (auto-runs on startup)
- **Rollback migrations**: `make migrate-down`
- **Development environment**: `make dev-setup` (PostgreSQL + Redis via Docker)
- **Reset dev environment**: `make dev-reset`

## Architecture & Codebase Structure

### Technology Stack
- **Language**: Go 1.21.x
- **Framework**: Gin v1.11.0 (HTTP router)
- **Database**: PostgreSQL 15.x via `lib/pq`
- **Cache**: Redis 7.x (AWS ElastiCache)
- **Queue**: AWS SQS
- **Logging**: Zap (structured JSON)
- **Tracing**: OpenTelemetry
- **Metrics**: Prometheus Client
- **Circuit Breaker**: gobreaker
- **API Documentation**: gin-swagger

### Project Structure (MANDATORY Layout)
```
stack-monorepo/
├── cmd/main.go                    # Application entrypoint
├── internal/                      # Private application code
│   ├── api/                       # HTTP handlers, middleware, routes
│   │   ├── handlers/              # Gin handlers by feature
│   │   ├── middleware/            # HTTP middleware
│   │   └── routes/                # Route definitions
│   ├── domain/                    # Business logic (Clean Architecture)
│   │   ├── entities/              # Domain models and types
│   │   └── services/              # Business logic services
│   ├── infrastructure/            # External integrations
│   │   ├── adapters/              # External service adapters
│   │   │   ├── alpaca/            # Alpaca brokerage API
│   │   │   ├── circle/            # Circle payments API
│   │   │   └── zerog/             # 0G AI/storage API
│   │   ├── config/                # Configuration management
│   │   ├── database/              # DB connections and migrations
│   │   └── di/                    # Dependency injection
│   └── persistence/               # Data access layer (repositories)
├── pkg/                          # Shared utilities and libraries
├── configs/                      # Configuration files
├── migrations/                   # Database migrations
├── test/                         # Test utilities and integration tests
└── deployments/                  # Infrastructure as code
```

### Core Data Models
- **users**: End-user with KYC status, auth info, passcode
- **wallets**: Circle Developer-Controlled Wallets per chain (Ethereum, Solana, etc.)
- **deposits**: Incoming USDC deposits with status tracking
- **withdrawals**: USD withdrawal requests with multi-step status
- **balances**: Brokerage buying power at Alpaca
- **baskets**: Curated investment portfolios with composition
- **orders**: Buy/sell requests to Alpaca with brokerage refs
- **positions_cache**: Current holdings (derived/cached from Alpaca)

### Key Workflows
- **Funding Flow**: USDC deposits → Circle off-ramp → Alpaca funding
- **Withdrawal Flow**: Alpaca USD withdrawal → Circle on-ramp → USDC transfer
- **Investment Flow**: Basket orders → Alpaca execution → Position updates

## Code Style Guidelines

### Architecture Patterns (MANDATORY)
- **Clean Architecture**: handlers/controllers → services/use cases → repositories/data access
- **Repository Pattern**: Abstract all database interactions via interfaces
- **Adapter Pattern**: All external partner integrations use interfaces with concrete implementations
- **Asynchronous Orchestration**: Complex flows use event-driven approach with SQS
- **Circuit Breaker**: Use `gobreaker` for all critical external dependencies
- **Idempotency**: All SQS message handlers and critical endpoints must be idempotent

### Error Handling (MANDATORY)
- **NEVER** ignore errors - check every error returned
- Wrap errors with context: `fmt.Errorf("operation failed: %w", err)`
- Define custom error types for business logic: `var ErrInsufficientFunds = errors.New("insufficient funds")`
- Top-level handlers catch, log, and return appropriate responses

### Context Propagation (MANDATORY)
- Pass `context.Context` as first argument to functions in requests/background tasks
- Use context for deadlines, cancellation, and request-scoped values (correlation IDs)
- Include OpenTelemetry trace IDs in context for correlation

### Database Access (MANDATORY)
- **MUST** use Repository pattern from `internal/persistence/`
- **NEVER** use `lib/pq` directly in business logic (`internal/domain/`)
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
- Access config **ONLY** via `internal/infrastructure/config/` package
- **NEVER** read environment variables directly elsewhere
- Retrieve secrets via AWS Secrets Manager or config system
- **NEVER** hardcode secrets or log them

### Logging (MANDATORY)
- Use configured **Zap** logger instance
- **NEVER** use `fmt.Println` or `log.Println`
- Use structured JSON format
- Include correlation ID from context in every log message
- Log levels: Debug, Info, Warn, Error, Fatal, Panic (default: Info in production)

### Naming Conventions
- **Packages**: `lowercase`, short (e.g., `funding`, `adapters`)
- **Structs/Interfaces/Types**: `PascalCase`
- **Variables/Functions**: `camelCase` (exported start with uppercase)
- **Constants**: `PascalCase` or `UPPER_SNAKE_CASE` (be consistent)
- **Acronyms**: Treat as single words (`userID`, `apiClient`, not `UserId`, `ApiClient`)

### Input Validation (MANDATORY)
- Validate **ALL** input at API boundary (Gin handlers)
- Perform explicit validation checks
- Return user-friendly error messages (don't expose internals)

### Concurrency (MANDATORY)
- Use goroutines, channels, and sync primitives carefully
- Avoid race conditions (test with `-race` flag)
- Favor structured concurrency patterns

### Imports & Formatting
- Use `go fmt` and `goimports` for consistent formatting
- Group imports: standard library, third-party, internal packages
- Remove unused imports automatically with `goimports`

## Tooling & Rules

### IDE Rules
- **Cursor Rules**: `.cursor/rules/golang_rules.mdc`, `.cursor/rules/project_rules.mdc`
- **Copilot Instructions**: `.github/copilot-instructions.md`

### CI/CD Requirements
- Run `go test ./... -race` on every PR
- Must pass before merge
- Include `golangci-lint` checks
- Include security tests with `gosec`

### Observability Requirements
- **Tracing**: OpenTelemetry spans across all service boundaries
- **Metrics**: Prometheus (request latency, throughput, error rate)
- **Logging**: JSON format with trace IDs for correlation

This guide ensures consistent development practices across the STACK service codebase.</content>
</xai:function_call">✅
