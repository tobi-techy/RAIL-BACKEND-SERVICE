# Technology Stack

## Programming Languages
- **Go 1.24.0**: Primary backend language
- **SQL**: Database migrations and queries

## Core Frameworks & Libraries

### Web Framework
- **Gin v1.11.0**: HTTP router and middleware
- **Swagger/OpenAPI**: API documentation via gin-swagger

### Database
- **PostgreSQL 15**: Primary relational database
- **sqlx v1.4.0**: SQL extensions for Go
- **golang-migrate v4.19.0**: Database migration management
- **lib/pq v1.10.9**: PostgreSQL driver

### Caching
- **Redis 7**: In-memory cache and session store
- **go-redis v8.11.5**: Redis client

### Authentication & Security
- **JWT v5.3.0**: Token-based authentication
- **bcrypt**: Password hashing (via golang.org/x/crypto)
- **AES-256-GCM**: Encryption for sensitive data

### Blockchain Integration
- **Ethereum/EVM Chains**: go-ethereum v1.14.12
- **Solana**: openweb3/web3go v0.2.9
- **Circle API**: Wallet infrastructure and stablecoin management
- **Multi-chain support**: Ethereum, Polygon, BSC, Solana

### External Integrations
- **0G Labs v1.0.0**: AI inference and secure storage
- **Alpaca**: Stock/ETF brokerage integration
- **Due Network**: Virtual accounts and off-ramping
- **SendGrid v3.16.1**: Email notifications

### Utilities
- **Viper v1.21.0**: Configuration management
- **Zap v1.27.0**: Structured logging
- **UUID v1.6.0**: Unique identifier generation
- **Decimal v1.4.0**: Precise financial calculations
- **Cron v3.0.1**: Scheduled job execution
- **gobreaker v1.0.0**: Circuit breaker pattern

### Monitoring & Observability
- **Prometheus v1.23.2**: Metrics collection
- **OpenTelemetry v1.38.0**: Distributed tracing
- **Grafana**: Metrics visualization (via Docker)

### Testing
- **Testify v1.11.1**: Test assertions and mocking
- **go test**: Built-in testing framework
- **go-mock v0.5.0**: Mock generation

## Development Tools

### Containerization
- **Docker**: Container runtime
- **Docker Compose**: Multi-container orchestration
- **Dockerfile**: Multi-stage builds for production

### Build System
- **Makefile**: Build automation
- **go mod**: Dependency management

### Database Tools
- **pgAdmin**: PostgreSQL administration (optional)
- **RedisInsight**: Redis monitoring (optional)

## Development Commands

### Setup & Installation
```bash
# Install dependencies
go mod download

# Copy configuration
cp configs/config.yaml.example configs/config.yaml

# Start services with Docker Compose
docker-compose up -d

# Start with admin tools
docker-compose --profile admin up -d

# Start with monitoring
docker-compose --profile monitoring up -d
```

### Running the Application
```bash
# Run locally
go run cmd/main.go

# Build binary
go build -o stack_service cmd/main.go

# Run binary
./stack_service
```

### Database Management
```bash
# Run migrations (automatic on startup)
go run cmd/main.go migrate

# Wipe database (development only)
./scripts/db_wipe.sh

# Reset database with migrations
./scripts/db_reset.sh

# Reset with seed data
./scripts/db_reset.sh --seed
```

### Testing
```bash
# Run all tests
go test ./...

# Run unit tests
go test ./test/unit/...

# Run integration tests
go test ./test/integration/...

# Run with coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Run specific test
go test -v -run TestName ./path/to/package
```

### Code Quality
```bash
# Format code
go fmt ./...

# Lint code
golangci-lint run

# Vet code
go vet ./...

# Generate mocks
mockgen -source=path/to/interface.go -destination=path/to/mock.go
```

### Docker Operations
```bash
# Build image
docker build -t stack_service:latest .

# Run container
docker run -p 8080:8080 stack_service:latest

# View logs
docker-compose logs -f

# Stop services
docker-compose down

# Clean volumes
docker-compose down -v
```

### API Testing
```bash
# Test wallet API
./scripts/test_wallet_api.sh

# Test Due flow
./scripts/test_due_flow.sh

# Test balance API
./test_balance_api.sh
```

## Environment Variables

### Required
- `DATABASE_URL`: PostgreSQL connection string
- `JWT_SECRET`: Secret key for JWT signing
- `ENCRYPTION_KEY`: 32-byte key for AES encryption
- `CIRCLE_API_KEY`: Circle API authentication
- `ZEROG_STORAGE_ACCESS_KEY`: 0G storage access
- `ZEROG_COMPUTE_API_KEY`: 0G compute access

### Optional
- `LOG_LEVEL`: Logging level (debug, info, warn, error)
- `ENVIRONMENT`: Runtime environment (development, staging, production)
- `PORT`: Server port (default: 8080)
- `REDIS_URL`: Redis connection string
- `SENDGRID_API_KEY`: Email service API key

## API Endpoints

### Health & Metrics
- `GET /health`: Application health check
- `GET /metrics`: Prometheus metrics
- `GET /swagger/index.html`: API documentation

### Authentication
- `POST /api/v1/auth/register`: User registration
- `POST /api/v1/auth/login`: User login
- `POST /api/v1/auth/refresh`: Refresh access token

### Wallets
- `GET /api/v1/wallets`: Get user wallets
- `POST /api/v1/wallets`: Create wallet
- `GET /api/v1/wallet/addresses`: Get deposit addresses

### Funding
- `POST /api/v1/funding/deposit/address`: Generate deposit address
- `POST /api/v1/funding/webhooks/chain`: Chain webhook endpoint

### Investment
- `GET /api/v1/baskets`: Get investment baskets
- `POST /api/v1/baskets/{id}/invest`: Invest in basket
- `GET /api/v1/portfolio`: Get portfolio

### AI CFO
- `GET /api/v1/ai/summary/latest`: Get latest AI summary
- `POST /api/v1/ai/analyze`: Perform analysis

## Configuration Files

### configs/config.yaml
Main application configuration with sections for:
- Server settings (port, timeouts, rate limits)
- Database connection
- JWT configuration
- Blockchain networks
- 0G integration
- Circle integration
- Redis cache
- Email service

### docker-compose.yml
Multi-service orchestration:
- PostgreSQL database
- Redis cache
- Application service
- pgAdmin (admin profile)
- RedisInsight (admin profile)
- Prometheus (monitoring profile)
- Grafana (monitoring profile)

### Dockerfile
Multi-stage build:
1. Builder stage: Compile Go binary
2. Runtime stage: Minimal Alpine image with binary
