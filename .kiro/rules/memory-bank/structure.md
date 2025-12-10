# Project Structure

## Directory Organization

### Root Structure
```
stack_service/
├── cmd/                    # Application entry points
├── internal/               # Private application code
├── pkg/                    # Public reusable libraries
├── migrations/             # Database schema migrations
├── configs/                # Configuration files
├── docs/                   # Documentation
├── test/                   # Test suites
└── scripts/                # Build and maintenance scripts
```

## Core Components

### cmd/ - Application Entry
- `main.go`: Main application entry point with server initialization, worker startup, and graceful shutdown

### internal/ - Private Application Code

#### internal/api/
- `handlers/`: HTTP request handlers for all endpoints
- `middleware/`: Authentication, logging, rate limiting, CORS
- `routes/`: Route definitions and API versioning
- `graphql/`: GraphQL schema and resolvers (future)

#### internal/domain/
- `entities/`: Core business entities (User, Wallet, Deposit, Transaction, etc.)
- `repositories/`: Repository interfaces defining data access contracts
- `services/`: Business logic services organized by domain:
  - `onboarding/`: User registration and KYC orchestration
  - `wallet/`: Wallet lifecycle management
  - `funding/`: Deposit processing and confirmations
  - `investing/`: Portfolio and basket management
  - `aicfo/`: AI-powered financial insights

#### internal/infrastructure/
- `adapters/`: External service integrations (email, notifications)
- `cache/`: Redis caching layer
- `circle/`: Circle API client for wallet infrastructure
- `config/`: Configuration management with Viper
- `database/`: PostgreSQL connection and migrations
- `di/`: Dependency injection container
- `repositories/`: Repository implementations
- `zerog/`: 0G integration for AI and storage

#### internal/adapters/
- `alpaca/`: Alpaca brokerage integration for stock/ETF trading
- `due/`: Due Network integration for virtual accounts and off-ramping

#### internal/workers/
- `aicfo_scheduler/`: Scheduled AI CFO analysis jobs
- `funding_webhook/`: Webhook event processing for deposits
- `onboarding_processor/`: Async onboarding task processing
- `wallet_provisioning/`: Automated wallet creation and provisioning

#### internal/zerog/
- `clients/`: 0G API clients
- `compute/`: AI compute integration
- `inference/`: AI inference engine
- `prompts/`: AI prompt templates
- `storage/`: Secure object storage

### pkg/ - Public Libraries

#### Reusable Utilities
- `auth/`: JWT token generation and validation
- `crypto/`: AES-256-GCM encryption for sensitive data
- `logger/`: Structured logging with Zap
- `metrics/`: Prometheus metrics collection
- `retry/`: Exponential backoff retry logic
- `webhook/`: Webhook signature verification
- `circuitbreaker/`: Circuit breaker pattern for external services
- `errors/`: Custom error types and handling
- `queue/`: SQS queue integration

### migrations/
Sequential database migrations with up/down scripts:
- `001_initial_tables`: Core user and auth tables
- `002_stack_mvp_schema`: Investment and portfolio tables
- `003_onboarding_wallet_management`: Onboarding flow tables
- `004_create_wallet_tables`: Multi-chain wallet tables
- `005_add_auth_fields_to_users`: Passcode authentication
- `006_funding_event_jobs`: Webhook processing jobs
- `015_create_virtual_accounts_table`: Due virtual accounts
- `016_add_due_account_fields`: Due integration fields
- `019_add_offramp_fields_to_deposits`: Off-ramp tracking
- `021_create_transactions_table`: Transaction history
- `022_create_withdrawals_table`: Withdrawal tracking

### test/
- `unit/`: Unit tests for individual components
- `integration/`: Integration tests for service interactions
- `config/`: Test environment configuration

### docs/
- `architecture/`: System architecture documentation
- `prd/`: Product requirements documents
- `stories/`: User story specifications
- `api/`: API endpoint documentation

## Architectural Patterns

### Clean Architecture
- **Domain Layer**: Business entities and logic independent of frameworks
- **Application Layer**: Use cases and service orchestration
- **Infrastructure Layer**: External concerns (database, APIs, cache)
- **Presentation Layer**: HTTP handlers and API routes

### Dependency Injection
- Centralized DI container in `internal/infrastructure/di/`
- Constructor injection for all services
- Interface-based dependencies for testability

### Repository Pattern
- Interface definitions in `internal/domain/repositories/`
- Implementations in `internal/infrastructure/repositories/`
- Abstracts data access from business logic

### Worker Pattern
- Background workers for async processing
- Job queue with retry logic
- Graceful shutdown support

### Circuit Breaker Pattern
- Protects against cascading failures
- Automatic recovery and fallback
- Used for all external API calls

## Key Relationships

### User → Wallet → Deposit Flow
1. User registers via onboarding service
2. Wallet provisioning worker creates multi-chain wallets
3. Funding service processes deposits and converts to buying power

### Investment Flow
1. User selects investment basket
2. Investing service validates and creates order
3. Alpaca adapter executes trades
4. Portfolio service tracks positions and P&L

### Off-Ramp Flow
1. User initiates withdrawal
2. Due adapter creates transfer to virtual account
3. USDC converted to USD
4. Funds available in virtual account

### AI CFO Flow
1. Scheduler triggers weekly analysis
2. 0G compute generates insights
3. Results stored in 0G storage
4. User retrieves via API

## Configuration Management
- YAML-based configuration in `configs/config.yaml`
- Environment variable overrides
- Separate configs for development, staging, production
- Sensitive values loaded from environment
