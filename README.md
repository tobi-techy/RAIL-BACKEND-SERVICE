# RAIL - GenZ Web3 Investment Platform

RAIL is a Web3-native investment platform designed specifically for Gen Z users who are underserved by traditional banks and overwhelmed by complex crypto tools. It enables instant wealth-building through a hybrid model: fiat-to-stablecoin on-ramps, seamless investment in stocks/ETFs, and a protective AI CFO.

The platform bridges the gap between traditional finance and Web3 by providing a safe, frictionless investment experience that demystifies Web3 while outperforming legacy banking in speed and fairness.

## 🎯 Mission

To empower the next generation of investors with a platform that combines the accessibility of traditional finance with the innovation of Web3, delivered through an experience that feels designed for Gen Z culture.

## 🎯 Goals & Background

### Business Goals
- Drive rapid adoption with 10,000 Monthly Active Users (MAU) within the first 6 months of launch
- Establish a recurring revenue stream by converting at least 5% of free users into premium subscribers in year one
- Validate market viability by processing $1,000,000 in investments within the first year
- Position RAIL as the first mover in the Gen Z-native hybrid Web3 + traditional finance investment space

### User Goals
- Create a safe, frictionless investment platform that demystifies Web3 while outperforming legacy banking in speed and fairness
- Deliver a product experience that matches the expectations of digital-native Gen Z: fast, social, intuitive, and aligned with values like sustainability and fairness
- Encourage consistent investing behavior through gamification and protective guidance from an AI CFO

## 👥 Target Users

### Primary User Persona: "Taylor" - The Conscious & Connected Investor
- **Age**: 22
- **Profile**: Digitally native, balances part-time work with side hustles. Ambitious but cautious.
- **Digital Habits**: Lives on TikTok, Instagram, Reddit, and Discord. Uses Notion/Pinterest for visual planning. Expects fast, engaging, intuitive experiences that feel like "TikTok-meets-Cash App."
- **Financial Behaviors**: Keeps most funds in savings + P2P apps (Cash App, Venmo). Dabbles on Robinhood but distrusts its business model. Avoids crypto due to complexity.
- **Values/Motivations**: Wants financial independence, safety, and alignment with identity (e.g., sustainability, social impact). Goals: travel fund, apartment savings, safety net.

### Secondary Personas
- **Jordan** - The Banking-Frustrated Beginner (Age 21): Clunky traditional banking, delays (3–5 day ACH transfers), and punitive fees. Feels alienated by outdated systems.
- **Chris** - The Crypto-Curious but Overwhelmed (Age 19): Intimidated by seed phrases, high gas fees, and irreversible mistakes. Tried but abandoned crypto apps after losing money.

## 🚀 Core Features (MVP)

### 1. User Onboarding & Managed Wallet
- Simple sign-up with automatic creation of a secure, managed wallet
- No seed phrase complexity; custody abstracted away
- KYC/AML orchestration for compliance

### 2. Stablecoin Deposits
- Support deposits from at least one EVM chain (e.g., Ethereum) and one non-EVM chain (e.g., Solana)
- Conversion into stablecoins for immediate use as buying power
- Multi-chain wallet support for Ethereum, Polygon, Binance Smart Chain, and more

### 3. Investment Flow
- Automatic conversion of stablecoins into fiat-equivalent buying power
- Ability to invest in curated baskets of stocks/ETFs
- Simple portfolio view with performance tracking

### 4. Curated Investment Baskets
- Launch with 5–10 "expert-curated" investment baskets (e.g., Tech Growth, Sustainability, ETFs)
- Designed to simplify decision-making for new investors
- Balanced for simplicity + diversity

### 5. AI CFO (MVP Version)
- Provides automated weekly performance summaries
- On-demand portfolio analysis to highlight diversification, risk, and potential mistakes
- (Previous 0G-based implementation has been removed; endpoints remain but return NOT_IMPLEMENTED)

### 6. Brokerage Integration
- Secure backend integration for trade execution and custody of traditional assets
- Connection with brokerage partners for stock/ETF trading

## 🏗️ Architecture Overview

```
rail_service/
├── cmd/                    # Application entry points
│   └── main.go
├── internal/               # Private application code
│   ├── api/               # API layer
│   │   ├── handlers/      # HTTP request handlers
│   │   ├── middleware/    # HTTP middleware
│   │   └── routes/        # Route definitions
│   ├── domain/            # Business domain
│   │   ├── entities/      # Domain entities/models
│   │   ├── repositories/  # Repository interfaces
│   │   └── services/      # Business logic services
│   ├── infrastructure/    # External concerns
│   │   ├── adapters/      # External service adapters
│   │   ├── circle/        # Circle API integration
│   │   ├── config/        # Configuration management
│   │   ├── database/      # Database connections
│   │   │   ├── di/            # Dependency injection
│   │   │   ├── repositories/  # Repository implementations
│   │   ├── workers/           # Background workers
│   │   │   ├── funding_webhook/ # Funding webhook processor
│   │   │   └── wallet_provisioning/ # Wallet provisioning worker
├── pkg/                   # Public libraries
│   ├── auth/              # Authentication utilities
│   ├── crypto/            # Cryptographic functions
│   ├── logger/            # Logging utilities
│   ├── retry/             # Retry utilities
│   └── webhook/           # Webhook security
├── migrations/            # Database migrations
├── configs/               # Configuration files
├── deployments/           # Deployment configurations
├── scripts/               # Build and deployment scripts
└── tests/                 # Test files
    ├── unit/              # Unit tests
    ├── integration/       # Integration tests
    └── e2e/               # End-to-end tests
```

### Domain Services (MVP)
- **Onboarding Service**: sign-up, profile, KYC/AML orchestration, feature flags
- **Wallet Service**: managed wallet lifecycle, address issuance, custody abstraction
- **Funding Service**: deposit address generation, webhook listeners, confirmations, auto-convert → buying power
- **Investing Service**: basket catalog, orders (buy/sell), portfolio & positions, P&L calc, brokerage adapter
- **AI-CFO Service (Lite)**: weekly summaries, on-demand analysis, insight templates, uses 0G for inference & storage

## 🛠️ Technology Stack

- **Language**: Go 1.21
- **Framework**: Gin (HTTP router)
- **Database**: PostgreSQL 15
- **Cache**: Redis 7
- **Authentication**: JWT tokens
- **Blockchain**: Ethereum, Polygon, BSC, Solana
- **Containerization**: Docker & Docker Compose
- **Documentation**: Swagger/OpenAPI
- **Testing**: Go testing, Testify
- **Monitoring**: Prometheus, Grafana
- **AI/Storage**: (integration currently disabled; 0G support removed)
- **Wallet Infrastructure**: Circle for stablecoins and wallet infrastructure

## 📊 Success Metrics

### Business Objectives
- **User Acquisition**: 10,000 Monthly Active Users (MAU) within 6 months post-launch
- **Monetization**: 5% conversion from free users to premium tier in Year 1
- **Validation**: $1,000,000 in processed investment volume in Year 1

### User Success Metrics
- **Empowerment**: Users feel more in control of their financial future (via surveys)
- **Confidence**: Users feel safe and protected (via NPS and retention)
- **Habit Formation**: % of users with recurring investments increases steadily

### Key Performance Indicators (KPIs)
- **Engagement**: Daily Active Users (DAU), Monthly Active Users (MAU)
- **Retention**: Week 1, Month 1, Month 3 retention rates
- **Conversion**: Sign-up → Funded Account rate; Free → Premium rate
- **Financial**: Total Assets Under Management (AUM)

## 🚀 Quick Start

### Prerequisites

- Go 1.21+
- Docker & Docker Compose
- PostgreSQL 15
- Redis 7
- Git

### Installation

1. **Clone the repository**
```bash
git clone https://github.com/your-org/rail_service.git
cd stack_service
```

2. **Copy configuration**
```bash
cp configs/config.yaml.example configs/config.yaml
```

3. **Edit configuration**
Update the configuration file with your settings:
- Database credentials
- JWT secret
- Encryption key
- Blockchain RPC endpoints
- API keys for external services (Circle, 0G, brokerage)

4. **Start with Docker Compose**
```bash
# Basic services
docker-compose up -d

# With admin tools (pgAdmin, RedisInsight)
docker-compose --profile admin up -d

# With monitoring (Prometheus, Grafana)
docker-compose --profile monitoring up -d
```

5. **Run database migrations**
```bash
# Migrations run automatically on startup
# To run manually:
go run cmd/main.go migrate
```

### Development Setup

1. **Install dependencies**
```bash
go mod download
```

2. **Set environment variables**
```bash
export DATABASE_URL="postgres://postgres:postgres@localhost:5432/rail_service_dev?sslmode=disable"
export JWT_SECRET="your-super-secret-jwt-key"
export ENCRYPTION_KEY="your-32-byte-encryption-key"
```

3. **Run the application**
```bash
go run cmd/main.go
```

4. **Access the API**
- API: http://localhost:8080
- Health: http://localhost:8080/health
- Swagger: http://localhost:8080/swagger/index.html
- Metrics: http://localhost:8080/metrics

### Database Maintenance

The helper scripts in `scripts/` expect `DATABASE_URL` to point at the target Postgres instance (they will read `.env` automatically if present).

- **Wipe all data** without running migrations:
  ```bash
  ./scripts/db_wipe.sh
  ```
  Add `--force` to skip the confirmation prompt.

- **Reset the schema** by wiping data and reapplying migrations:
  ```bash
  ./scripts/db_reset.sh
  ```
  Optional flags: `--force` to skip confirmation, `--skip-migrate` to leave the database empty, `--seed` to run `go run scripts/seed.go` after migrations.

## 📚 API Documentation

### Authentication

All protected endpoints require a JWT token in the Authorization header:

```bash
Authorization: Bearer <your-jwt-token>
```

### Key Endpoints

#### Authentication
- `POST /api/v1/auth/register` - User registration
- `POST /api/v1/auth/login` - User login
- `POST /api/v1/auth/refresh` - Refresh access token
- `POST /api/v1/auth/logout` - Logout

#### Onboarding
- `POST /api/v1/onboarding/start` - Start onboarding process
- `GET /api/v1/onboarding/status` - Get onboarding status
- `POST /api/v1/kyc/submit` - Submit KYC documents

#### Wallets
- `GET /api/v1/wallets` - Get user wallets
- `POST /api/v1/wallets` - Create new wallet
- `GET /api/v1/wallets/{id}/balance` - Get wallet balance
- `GET /api/v1/wallet/addresses?chain=eth|sol` - Get deposit addresses

#### Funding
- `POST /api/v1/funding/deposit/address` - Generate deposit address
- `POST /api/v1/funding/webhooks/chain` - Chain webhook endpoints
- `GET /api/v1/funding/confirmations` - Get deposit confirmations

#### Investment Baskets
- `GET /api/v1/baskets` - Get user baskets
- `POST /api/v1/baskets` - Create custom basket
- `GET /api/v1/curated/baskets` - Get curated baskets
- `POST /api/v1/baskets/{id}/invest` - Invest in basket
- `GET /api/v1/portfolio` - Get user portfolio

#### AI CFO
- `GET /api/v1/ai/summary/latest` - Get latest AI summary
- `POST /api/v1/ai/analyze` - Perform on-demand analysis

#### Copy Trading
- `GET /api/v1/copy/traders` - Get top traders
- `POST /api/v1/copy/traders/{id}/follow` - Follow trader

#### Cards
- `GET /api/v1/cards` - Get user cards
- `POST /api/v1/cards` - Create physical card
- `POST /api/v1/cards/{id}/freeze` - Freeze card

### Complete API documentation is available at `/swagger/index.html` when running the server.

## 🧪 Testing

### Unit Tests
```bash
go test ./...
```

### Integration Tests
```bash
go test ./tests/integration/...
```

### End-to-End Tests
```bash
go test ./tests/e2e/...
```

### Test Coverage
```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 🔒 Security

### Security Features
- JWT authentication with refresh tokens
- Password hashing with bcrypt
- AES-256-GCM encryption for sensitive data
- Rate limiting
- CORS protection
- Security headers
- Input validation and sanitization
- Audit logging
- Session management
- KYC/AML integration for compliance

### Security Best Practices
- All sensitive data is encrypted at rest
- Private keys are encrypted before storage
- API rate limiting prevents abuse
- Comprehensive audit trails
- Two-factor authentication support
- IP whitelisting for admin endpoints
- Secure custody abstraction via wallet manager

## 🔧 Configuration

Key configuration options in `configs/config.yaml`:

```yaml
# Server configuration
server:
  port: 8080
  host: 0.0.0.0
  rate_limit_per_min: 100

# Database configuration
database:
  host: localhost
  port: 5432
  name: stack_service
  user: postgres
  password: postgres

# JWT configuration
jwt:
  secret: "your-secret-key"
  access_token_ttl: 604800
  refresh_token_ttl: 2592000

# Blockchain networks
blockchain:
  networks:
    ethereum:
      chain_id: 1
      rpc: "https://eth-mainnet.alchemyapi.io/v2/YOUR-API-KEY"
    polygon:
      chain_id: 137
      rpc: "https://polygon-rpc.com"
    bsc:
      chain_id: 56
      rpc: "https://bsc-dataseed.binance.org"
    solana:
      rpc: "https://api.mainnet-beta.solana.com"


# Circle Integration
circle:
  api_key: "${CIRCLE_API_KEY}"
  base_url: "https://api.circle.com"
```

## 🚀 Deployment

### Docker Deployment

1. **Build production image**
```bash
docker build -t rail_service:latest .
```

2. **Run container**
```bash
docker run -p 8080:8080 \
  -e DATABASE_URL="postgres://..." \
  -e JWT_SECRET="..." \
  -e CIRCLE_API_KEY="..." \
  -e ZEROG_STORAGE_ACCESS_KEY="..." \
  -e ZEROG_COMPUTE_API_KEY="..." \
  rail_service:latest
```

### Kubernetes Deployment

Kubernetes manifests are available in the `deployments/` directory:

```bash
kubectl apply -f deployments/k8s/
```

### Cloud Deployment

The application is cloud-ready and can be deployed on:
- AWS ECS/EKS
- Google Cloud Run/GKE  
- Azure Container Instances/AKS
- DigitalOcean App Platform

## 📊 Monitoring & Observability

### Health Checks
- `GET /health` - Application health
- `GET /metrics` - Prometheus metrics

### Logging
- Structured logging with Zap
- Request/response logging
- Error tracking with stack traces
- Audit trail logging

### Metrics
- HTTP request metrics
- Database connection metrics
- Business metrics (transactions, users, etc.)
- Custom application metrics
- 0G storage and compute metrics

### Monitoring Stack
- **Prometheus**: Metrics collection
- **Grafana**: Visualization dashboards
- **AlertManager**: Alert notifications

## 🤝 Contributing

See [CONTRIBUTING.md](./docs/CONTRIBUTING.md) for detailed contribution guidelines.

### Development Workflow
1. Fork the repository
2. Create feature branch (`git checkout -b feature/amazing-feature`)
3. Follow coding standards (see below)
4. Write tests for new functionality
5. Ensure all tests pass
6. Create pull request

### Coding Standards
- Follow Go conventions and best practices
- Use meaningful variable and function names
- Write comprehensive tests
- Document public APIs
- Follow the established project structure
- Use dependency injection
- Handle errors appropriately

## 📝 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🆘 Support

- **Issues**: GitHub Issues
- **Documentation**: `/docs` directory
- **API Docs**: Swagger UI at `/swagger`
- **Community**: GitHub Discussions

## 🛣️ Roadmap

### Phase 1 (Current - MVP)
- [x] Basic authentication and user management
- [x] Multi-chain wallet integration
- [x] Investment baskets foundation
- [x] AI CFO implementation (MVP)
- [ ] Copy trading implementation
- [ ] Debit card integration

### Phase 2
- [ ] Advanced portfolio analytics
- [ ] Mobile app API
- [ ] DeFi protocol integrations
- [ ] Yield farming strategies
- [ ] NFT portfolio tracking

### Phase 3
- [ ] AI-powered investment recommendations
- [ ] Social trading features
- [ ] Institutional features
- [ ] Options and derivatives
- [ ] Cross-chain bridge integration

---

**Built with ❤️ for the GenZ Web3 community**
