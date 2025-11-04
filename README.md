# STACK - GenZ Web3 Investment Platform

> **The safe, fun, no-BS way for Gen Z to grow money without banks or crypto headaches.**

STACK is a Web3-native investment platform that empowers Gen Z users to build wealth instantly through a hybrid TradFi-Web3 model. By abstracting away the complexity of both legacy banking and decentralized finance, STACK offers a borderless, instant funding mechanism via stablecoins, seamlessly bridged into a regulated environment for investing in stocks and ETFs.

## 🌟 The Problem We Solve

Gen Z investors are stuck between two flawed systems:

1. **Traditional Banking (Jordan's Pain)**: Slow 3-5 day ACH transfers, clunky interfaces built for older generations, and punitive minimum balance fees that feel greedy and alienating.

2. **Pure Web3 (Chris's Pain)**: Intimidating seed phrases, irreversible mistakes, shockingly high gas fees, and a technical barrier that makes crypto feel "not worth it."

**STACK bridges this gap** by combining the transparency and accessibility banks lack with the real-world asset integration crypto apps miss, wrapped in a Gen Z-native experience that feels like "TikTok-meets-Cash App."

## 💡 Our Solution

A **Go-based modular monolith** deployed on AWS that provides:
- **Instant, borderless funding** via USDC deposits from multiple chains (Ethereum, Solana, Polygon, BSC)
- **Seamless Web3-to-TradFi bridge** through Circle Developer-Controlled Wallets and Alpaca brokerage integration
- **Curated investment baskets** for simplified decision-making
- **AI CFO protection** powered by 0G to guide users away from common financial mistakes
- **Social investing features** like copy trading and user-curated baskets (post-MVP)

## 🔗 Related Repositories

- **Frontend (React Native Mobile App)**: [https://github.com/Sketchyjo/Stacks-frontend](https://github.com/Sketchyjo/Stacks-frontend)

## 🎯 Mission

To empower the next generation of investors with a platform that combines the accessibility of traditional finance with the innovation of Web3, delivered through an experience that feels designed for Gen Z culture.

## 🎯 Goals & Background

### Business Goals
- Drive rapid adoption with 10,000 Monthly Active Users (MAU) within the first 6 months of launch
- Establish a recurring revenue stream by converting at least 5% of free users into premium subscribers in year one
- Validate market viability by processing $1,000,000 in investments within the first year
- Position STACK as the first mover in the Gen Z-native hybrid Web3 + traditional finance investment space

### User Goals
- Create a safe, frictionless investment platform that demystifies Web3 while outperforming legacy banking in speed and fairness
- Deliver a product experience that matches the expectations of digital-native Gen Z: fast, social, intuitive, and aligned with values like sustainability and fairness
- Encourage consistent investing behavior through gamification and protective guidance from an AI CFO

## 👥 Target Users

### Primary User Persona: "Taylor" - The Conscious & Connected Investor

**Profile:**
- **Age**: 22
- **Occupation**: Part-time job + creative side hustle (e.g., Etsy)
- **Tech Savvy**: Digitally native multi-tasker who is smart, ambitious, and seeks financial empowerment on her own terms

**Digital Habits & UX Expectations:**
- Lives on **TikTok, Instagram, Reddit**
- Active member of at least one **Discord** community
- Uses visual tools like **Pinterest** or **Notion** for planning
- Expects UX to feel like **"TikTok-meets-Cash App"**: fast, visually clean, engaging, and intuitive

**Current Financial Behaviors:**
- Cautious and underserved by current options
- Money sits in low-interest savings accounts or P2P apps (Cash App, Venmo)
- Dabbles on Robinhood with small amounts but distrusts their business model (PFOF scandals)
- Avoids crypto due to complexity and fear of irreversible mistakes

**Values & Motivations:**
- Primary driver: **Financial breathing room and independence**
- Tangible goals: Building a travel fund, saving for an apartment, establishing a safety net
- Investing is an act of taking control
- Must align with identity values: **sustainability, social impact, fairness**

### Secondary Personas

**Jordan (Age 21) - The Banking-Frustrated Beginner:**
- Frustrated by clunky traditional banking interfaces built for older generations
- Tired of 3-5 day ACH transfer delays that cause missed investment opportunities
- Hit with unexpected minimum balance fees ($12 for dropping below $300)
- Feels the system is slow, greedy, and alienating

**Chris (Age 19) - The Crypto-Curious but Overwhelmed:**
- Intimidated by 12-word seed phrase warnings and fear of losing access
- Lost $50 by accidentally sending crypto to the wrong network
- Shocked by $28 gas fees that wiped out half of a $50 trade on Ethereum
- Concluded "Crypto's a scam. Not worth it." and deleted wallet apps

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
- Uses 0G for inference and storage capabilities

### 6. Brokerage Integration
- Secure backend integration for trade execution and custody of traditional assets
- Connection with brokerage partners for stock/ETF trading

## 🏗️ Architecture Overview

### Architectural Style

**Modular Monolith** - A single Go application with strongly defined internal boundaries organized by domain modules. This architecture:
- Enables faster MVP development compared to microservices
- Maintains clear separation of concerns for future service extraction
- Reduces operational complexity while preserving modularity

### System Architecture

```mermaid
graph TD
    U[Gen Z User Mobile] --> RN[React Native App]
    
    subgraph "STACK Backend - Go Modular Monolith on AWS Fargate"
        RN --> GW[API Gateway - GraphQL]
        GW --> ONB[Onboarding Module]
        GW --> WAL[Wallet Module]
        GW --> FND[Funding Module]
        GW --> INV[Investing Module]
        GW --> AIC[AI CFO Module]
        
        ONB --> PG[(PostgreSQL)]
        WAL --> PG
        FND --> PG
        INV --> PG
        AIC --> PG
        FND --> SQS[AWS SQS Queue]
    end
    
    subgraph "External Partners"
        ONB --> IDP[Auth0/Cognito]
        ONB --> KYC[KYC/AML Provider]
        WAL --> CIR[Circle API]
        FND --> CIR
        INV --> ALP[Alpaca Brokerage]
        AIC --> ZG[0G AI/Storage]
    end
```

### Key Architectural Patterns

1. **Repository Pattern**: All database interactions abstracted via Go interfaces
2. **Adapter Pattern**: External partner integrations (Circle, Alpaca, 0G) use interfaces with concrete implementations
3. **Asynchronous Orchestration (Sagas)**: Complex multi-step flows (funding, withdrawal) managed via SQS event-driven approach
4. **Circuit Breaker Pattern**: Using `gobreaker` for all critical external dependencies
5. **Interface-Driven Development**: All public functions interact with interfaces, not concrete types

### Project Structure

```
stack-monorepo/
├── cmd/api/                    # Main application entrypoint (main.go)
├── internal/                   # Private application code
│   ├── api/                    # GraphQL handlers, resolvers, middleware
│   ├── core/                   # Business logic modules
│   │   ├── onboarding/         # User sign-up, KYC, passcode
│   │   ├── wallet/             # Circle wallet management
│   │   ├── funding/            # USDC deposits, withdrawals, off/on-ramps
│   │   ├── investing/          # Baskets, orders, portfolio, Alpaca integration
│   │   └── aicfo/              # AI-powered summaries and analysis
│   ├── adapters/               # External service integrations
│   │   ├── circle/             # Circle Developer Wallets, USDC on/off-ramps
│   │   ├── alpaca/             # Alpaca brokerage trading & custody
│   │   ├── authprovider/       # Auth0/Cognito client
│   │   ├── kycprovider/        # KYC/AML service integration
│   │   └── zerog/              # 0G AI inference & storage
│   ├── persistence/            # Database repositories
│   │   ├── postgres/           # PostgreSQL implementations
│   │   └── migrations/         # Database schema migrations
│   └── config/                 # Configuration management
├── pkg/common/                 # Shared utilities, types, errors
├── infrastructure/aws/         # Terraform IaC for AWS resources
├── api/graph/                  # GraphQL schema files
└── scripts/                    # Build, test, deployment scripts
```

### Data Flow

**Funding Flow (USDC → USD → Alpaca):**
1. User deposits USDC to Circle wallet (on-chain)
2. Circle webhook notifies backend → log deposit
3. **Async**: Initiate Circle off-ramp (USDC → USD)
4. Circle confirms → enqueue broker funding task
5. **Async**: Fund Alpaca account with USD
6. Alpaca confirms → update balance, notify user

**Withdrawal Flow (Alpaca → USD → USDC):**
1. User requests withdrawal
2. **Async**: Alpaca USD withdrawal
3. Alpaca confirms → enqueue Circle on-ramp
4. **Async**: Circle converts USD → USDC
5. **Async**: Transfer USDC on-chain to user address
6. Circle confirms → update status, notify user

**Investment Flow:**
1. User places order (basket/option)
2. Check balance at Alpaca
3. Create order record → submit to Alpaca
4. Alpaca accepts → update order status
5. Alpaca fill webhook → update order, cache positions

### Core Domain Modules (MVP)

#### Onboarding Module
- User sign-up with Auth0/Cognito integration
- Profile management and passcode setup/verification
- KYC/AML orchestration and status tracking
- Feature flag management
- Triggers wallet creation upon successful onboarding

#### Wallet Module  
- Manages Circle Developer-Controlled Wallets lifecycle
- Multi-chain support (Ethereum, Solana, Polygon, BSC)
- Generates deposit addresses per chain
- Abstracts custody complexity from users
- No seed phrases required

#### Funding Module
- Monitors blockchain deposits via Circle webhooks
- Orchestrates multi-step funding flow:
  - USDC deposit → Circle off-ramp → Alpaca funding
- Handles withdrawal requests:
  - Alpaca withdrawal → Circle on-ramp → on-chain transfer
- Manages state transitions via SQS queues
- Ensures idempotency for all operations

#### Investing Module
- Manages curated investment baskets (5-10 for MVP)
- Places buy/sell orders with Alpaca for stocks/ETFs/options
- Retrieves and caches portfolio positions
- Calculates portfolio performance and P&L
- Handles order status updates via Alpaca webhooks

#### AI CFO Module
- Generates automated weekly performance summaries
- Provides on-demand portfolio analysis
- Highlights diversification, risk, and potential mistakes
- Powered by 0G for AI inference and secure storage
- Builds trust by protecting users from common financial errors

## 🛠️ Technology Stack

### Backend
- **Language**: Go 1.21.x
- **Web Framework**: Gin v1.11.0 (high-performance, minimalist)
- **API**: GraphQL via gqlgen library
- **Database**: PostgreSQL 15.x (RDS managed)
- **DB Driver**: lib/pq (standard Go PostgreSQL driver)
- **Cache**: Redis 7.x (AWS ElastiCache)
- **Queue**: AWS SQS (asynchronous task processing)
- **Authentication**: JWT tokens via Auth0/Cognito

### Observability & Resilience
- **Logging**: Zap (structured JSON logging)
- **Tracing**: OpenTelemetry (distributed tracing)
- **Metrics**: Prometheus Client (Go)
- **Circuit Breaker**: gobreaker (resilience pattern)
- **Monitoring**: AWS CloudWatch / Datadog

### External Partners
- **Identity**: Auth0 or AWS Cognito (OIDC)
- **Wallet/Funding**: Circle API (Developer-Controlled Wallets, USDC on/off-ramps)
- **Brokerage**: Alpaca API (stock/options trading, custody)
- **AI/Storage**: 0G (AI CFO features, secure data storage)
- **KYC/AML**: Third-party KYC provider integration

### Infrastructure
- **Cloud**: AWS (Fargate/ECS, RDS, ElastiCache, SQS, Secrets Manager, S3, CloudFront, WAF)
- **IaC**: Terraform 1.6.x (infrastructure as code)
- **CI/CD**: GitHub Actions
- **Containerization**: Docker
- **API Documentation**: gin-swagger (OpenAPI/Swagger generation)

### Frontend
- **Framework**: React Native 0.72.x (cross-platform mobile)
- **Language**: TypeScript 5.x
- **Repository**: [Stacks-frontend](https://github.com/Sketchyjo/Stacks-frontend)

### Blockchain Support
- **EVM Chains**: Ethereum, Polygon, Binance Smart Chain
- **Non-EVM**: Solana
- **Stablecoin**: USDC (primary)

## 📊 Success Metrics

### Business Objectives
- **User Acquisition**: Acquire **10,000 Monthly Active Users (MAU)** within 6 months of public launch
- **Monetization**: Achieve **5% conversion rate** from active free users to premium subscription tier within first year
- **Platform Validation**: Process **$1,000,000 in total investment volume** through the platform in first year
- **Market Position**: Establish STACK as first mover in Gen Z-native hybrid Web3 + TradFi investment space

### User Success Metrics
- **Empowerment**: Users feel more in control of their financial future (measured via user surveys)
- **Confidence**: Users feel protected and safe (measured via Net Promoter Score and retention rates)
- **Habit Formation**: Users successfully establish regular investment habits (measured by % of users with recurring investments)

### Key Performance Indicators (KPIs)
- **Engagement**: Daily Active Users (DAU), Monthly Active Users (MAU)
- **Retention**: Week 1, Month 1, Month 3 retention rates
- **Conversion**: Sign-up → Funded Account rate; Free → Premium subscription rate
- **Financial**: Total Assets Under Management (AUM), Average account size, Transaction volume

### MVP Success Criteria
- Onboard **100 users** who fund their accounts and make at least one investment
- Validate end-to-end technical flow with **>99% success rate**
- Achieve user satisfaction score of **7/10 or higher** on core experience

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
git clone https://github.com/your-org/stack_service.git
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
export DATABASE_URL="postgres://postgres:postgres@localhost:5432/stack_service_dev?sslmode=disable"
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
- Rate limiting and request throttling
- CORS protection and security headers
- Input validation and sanitization
- Comprehensive audit logging
- Session management with secure cookies
- KYC/AML integration for compliance
- Row-level security (RLS) for database access
- Database encryption at rest
- AWS Secrets Manager integration
- API key rotation and HMAC request signing
- Extended role-based access control (RBAC)
- IP whitelisting for sensitive operations
- Automated security scanning (gosec + Trivy)

### Security Best Practices
- All sensitive data is encrypted at rest using AES-256-GCM
- Private keys are encrypted before storage in AWS Secrets Manager
- API rate limiting prevents abuse and DoS attacks
- Comprehensive audit trails for all financial operations
- Two-factor authentication support with passcode system
- IP whitelisting for admin and financial operations
- Secure custody abstraction via Circle managed wallets
- Request signing for critical financial endpoints
- Automated dependency vulnerability scanning
- Database row-level security ensures users only access their data
- API keys are automatically rotated with AWS Secrets Manager

### Security Scanning
The CI/CD pipeline includes automated security scanning:
- **Gosec**: Static analysis for Go security issues
- **Trivy**: Container and filesystem vulnerability scanning
- **Dependency checking**: Automated scans for vulnerable dependencies

### Secrets Management
Sensitive configuration is managed through AWS Secrets Manager:
- Database passwords, API keys, and encryption keys
- Automatic rotation with configurable TTL
- Cached secrets with secure in-memory storage
- Audit logging for all secret access

### Access Control
- **User Roles**: user, premium, trader, admin, super_admin
- **Hierarchical Permissions**: Higher roles inherit lower role permissions
- **IP Whitelisting**: Critical operations restricted to approved IP ranges
- **Request Signing**: HMAC signatures required for funding operations
- **API Key Management**: Secure key generation and automatic rotation

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

# 0G Integration
zerog:
  storage:
    endpoint: "https://storage.0g.ai"
    access_key: "${ZEROG_STORAGE_ACCESS_KEY}"
    secret_key: "${ZEROG_STORAGE_SECRET_KEY}"
    bucket: "stack-platform"
  compute:
    endpoint: "https://compute.0g.ai"
    api_key: "${ZEROG_COMPUTE_API_KEY}"
    model: "gpt-4"

# Circle Integration
circle:
  api_key: "${CIRCLE_API_KEY}"
  base_url: "https://api.circle.com"
```

## 🏗️ Infrastructure & Deployment

### Deployment Strategy

**Blue/Green Deployments** on AWS Fargate (ECS) enable zero-downtime releases:
- Traffic shifting managed via Application Load Balancer (ALB) target groups
- Automated rollback if health checks fail on new deployment
- AWS CodeDeploy integration for orchestration

### Environments

| Environment | Purpose | Infrastructure | Partner APIs |
|-------------|---------|----------------|-------------|
| **Local** | Development | Docker Compose (Postgres, Redis, mocks) | Mocked services |
| **Staging** | QA & E2E Testing | AWS (scaled down) | Partner sandbox APIs |
| **Production** | Live system | AWS (HA configured) | Partner production APIs |

### Rollback Strategy

**Primary Method**: ECS Blue/Green deployment capabilities
- If "green" deployment fails health checks, traffic stays on "blue" (stable) version
- Manual rollback via ALB traffic redirection to blue target group

**Trigger Conditions**:
- Failed health checks post-deployment
- Critical error rate spikes detected by monitoring
- Failed automated post-deployment smoke tests

**Database Rollbacks**:
- Handled via migration tooling (e.g., goose down)
- Critical schema changes must be backward compatible
- Non-reversible migrations require careful planning

### CI/CD Pipeline

**Platform**: GitHub Actions (`.github/workflows/`)

**Workflow**:
```
[Feature Branch PR] → [CI Tests + Lint] → [Merge to main] → [Staging Deploy] 
  → [Manual QA] → [Production Deploy Trigger] → [Blue/Green Production Deploy]
```

**Quality Gates**:
- `go test ./... -race` must pass
- `golangci-lint` checks must pass
- `gosec` security scanning must pass
- Test coverage >80% for core modules

### Docker Deployment

1. **Build production image**
```bash
docker build -t stack_service:latest .
```

2. **Run container**
```bash
docker run -p 8080:8080 \
  -e DATABASE_URL="postgres://..." \
  -e JWT_SECRET="..." \
  -e CIRCLE_API_KEY="..." \
  -e ZEROG_STORAGE_ACCESS_KEY="..." \
  -e ZEROG_COMPUTE_API_KEY="..." \
  stack_service:latest
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
