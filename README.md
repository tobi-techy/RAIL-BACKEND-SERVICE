# Rail

<p align="center">
  <strong>A rules-based capital engine — money splits itself the moment it arrives.</strong>
</p>

<p align="center">
  <a href="#overview">Overview</a> •
  <a href="#the-problem">Problem</a> •
  <a href="#how-rail-works">How It Works</a> •
  <a href="#market-position">Market Position</a> •
  <a href="#architecture">Architecture</a> •
  <a href="#getting-started">Getting Started</a> •
  <a href="#api-reference">API</a>
</p>

---

## Overview

Rail is a rules-based capital engine for people who want financial progress without the overhead of managing it. When money enters Rail — via bank transfer, mobile money, or stablecoin deposit — it immediately splits 70% to spending and 30% to investing. No configuration. No decisions. The system runs automatically.

**Core Principle**: Depositing funds equals consent to the system. Money starts working the moment it arrives.

Rail is live in production with real users, real deposits, and real yield.

### How Rail Compares

| | Traditional Finance | Crypto Neobanks | Rail |
|---|---|---|---|
| Asset selection | User-driven | User-driven | System-defined |
| Yield | Opt-in | Opt-in | Automatic |
| Setup required | High | Medium | None |
| Financial literacy needed | High | Medium | None |
| Spending + investing | Separate apps | Separate flows | Single unified product |
| Fiat deposit rails | Bank-dependent | USD-only | NGN, GBP, USD, EUR |
| Rebalancing | Manual | Manual | Automated |

---

## The Problem

Most people don't invest — not because they lack access, but because the process demands too much from them:

- **Decision overload** — which asset, which platform, how much, when
- **Fragmented tools** — one app to spend, another to save, another to invest
- **Cognitive cost** — every financial decision requires attention and confidence most people don't have
- **Currency risk** — in markets with structural currency weakness, holding local currency erodes savings passively
- **DeFi complexity** — seed phrases, gas fees, and protocol risk are real barriers for non-technical users

The result: most people leave money idle in bank accounts earning nothing, or spend everything because there's no automatic mechanism pushing capital toward growth.

**Access to financial products was solved years ago. Direction was not.**

Rail removes the decision entirely. The system allocates automatically. Users don't choose — they trust.

### Target User

Rail is built for the 18-30 year old who earns in any currency, wants their money to work, and doesn't want to think about how. This includes:

- Young professionals in emerging markets hedging against local currency devaluation
- Diaspora users sending money home or managing cross-border finances
- Crypto-adjacent users who want yield without DeFi complexity
- Anyone who would invest if the process were invisible

**Design constraint**: Any screen that requires explanation is a failure.

---

## Market Position

Rail operates at the intersection of two converging trends:

**1. Stablecoin-powered consumer finance**
The playbook is established: accept deposits, hold in stablecoins, earn yield, enable spending. What's missing is a product that executes this automatically, without asking users to configure anything. Rail does this with a non-negotiable allocation rule — the 70/30 split is not a feature, it's the product.

**2. Global demand for USD-denominated savings**
In markets with structural currency weakness — Nigeria, Argentina, Turkey, and others — there is organic, growing demand for USD-denominated savings instruments. Stablecoins are the most accessible form of this. Rail routes the stash portion into a yield-bearing position via Reflect Money (backed by US Treasuries), giving users passive USD exposure from the moment they deposit.

**The gap Rail fills**:
Every competing product is opt-in. Users must choose to save, choose to invest, choose a strategy. Rail is opt-out-impossible by design. The split happens automatically. No competitor — among live products, hackathon winners, or accelerator-backed startups — has claimed this combination of rule-enforced allocation, multi-currency fiat rails, and automated yield.

**Competitive landscape** (as of March 2026):

| Product | Category | Gap vs Rail |
|---------|----------|-------------|
| FlexVest, MintYield, DeFi Koala | Stablecoin savings | Opt-in yield, no spending layer, no fiat rails |
| NectarFi, Fizen | Crypto super-apps | User-configured, DeFi-native UX, no automation |
| LocalPay, Tsara, FossaPay | Africa payments | Payments only, no wealth automation |
| Robinhood, Acorns | Consumer investing | USD-only, no stablecoin yield, no Africa rails |
| **Rail** | Automated wealth engine | 70/30 split + multi-currency fiat + automated yield + debit card |

---

## How Rail Works

### The Rail Split

Every deposit is automatically divided the moment it clears:

```
                    DEPOSIT ARRIVES
                    (NGN / GBP / USD / USDC)
                          |
                          v
                    SPLIT ENGINE
                  (Automatic 70/30)
                          |
            +-------------+-------------+
            |                           |
            v                           v
      +----------+              +----------+
      | 70% SPEND|              |30% STASH |
      +----------+              +----------+
      | USDC     |              | USDC     |
      | Liquid   |              | Yield    |
      | Card     |              | Auto     |
      +----------+              +----------+
```

| Property | Value |
|----------|-------|
| Ratio | 70/30, system-defined |
| User control | None — ratio is fixed |
| Timing | Within seconds of deposit clearing |
| Spend currency | USDC |
| Stash currency | USDC (earns yield automatically via Reflect Money) |
| Disclosure | Shown before first deposit |

### Funding Methods

#### 1. Virtual Accounts (Fiat)

Users receive dedicated virtual account details per currency. Deposits arrive via standard bank transfer and trigger the split automatically.

```
User's Bank → Virtual Account (NGN / GBP / USD) → Rail Split Engine
```

| Currency | Rail | Settlement |
|----------|------|------------|
| NGN | Bank Transfer | Instant |
| GBP | Faster Payments | Instant |
| USD | ACH / Wire | T+0 to T+1 |
| EUR | SEPA | T+0 to T+1 |

#### 2. Crypto On-Ramp (Stablecoins)

Users can deposit USDC directly from any supported chain. The deposit is credited and split identically to fiat deposits.

```
External Wallet → Bridge Deposit Address → Rail Split Engine
```

| Chain | Token | Confirmation |
|-------|-------|--------------|
| Solana | USDC | ~32 slots |
| Ethereum | USDC | ~12 blocks |
| Base | USDC | ~2 blocks |
| Polygon | USDC | ~128 blocks |
| Arbitrum | USDC | ~1 block |

---

## Core Features

### 1. Spend Layer

The 70% spend wallet functions as a full checking account replacement.

- Real-time spendable balance backed by a double-entry ledger
- Virtual debit card (Visa, powered by Bridge)
- Instant access — no lockups, no withdrawal delays
- Full transaction history
- Round-up automation: card purchases round up to the nearest dollar, with the difference routed to the stash

### 2. Stash (Yield Layer)

The 30% stash wallet holds USDC in a yield-bearing position via Reflect Money, backed by US Treasuries.

- Yield accrues automatically, no staking or claiming required
- ~3-4% APY from Reflect Money (US Treasury-backed)
- Balance grows passively while the user does nothing
- Withdrawable at any time — no lockup

### 3. Invest Engine

The invest engine deploys stash capital into diversified portfolios via Alpaca brokerage.

```
Stash Balance
      |
      v
Strategy Selector
(age, region, deposit size, frequency)
      |
      v
Asset Allocation
(ETFs, equities, stablecoins — weighted by strategy)
      |
      v
Alpaca Brokerage
(trade execution)
```

- No asset visibility to users — positions are abstracted
- No trade confirmations required
- No strategy choices presented
- Global fallback strategy applied by default
- Round-up amounts queue and deploy when threshold is met

### 4. Conductors (Expert-Led Tracks)

Conductors are verified investors who publish portfolio tracks. Users follow a Conductor and their capital automatically mirrors the Conductor's allocation moves.

```
Conductor updates track
        |
        v
   Copy Engine
        |
   +---------+---------+
   |         |         |
Follower A  Follower B  Follower C
   |         |         |
   +---------+---------+
        |
   Alpaca Brokerage
```

| Attribute | Description |
|-----------|-------------|
| Track | Named strategy (e.g., "Tech Growth", "Dividend Income") |
| Assets | Curated list with target weights |
| Risk level | Low / Medium / High |
| Performance | Historical returns visible to followers |
| Mirror latency | Target < 5 minutes |

### 5. The Station (Home Screen)

The Station answers one question: *Is my money working?*

```
+---------------------------------------+
|            THE STATION                |
+---------------------------------------+
|         Total Balance                 |
|           $2,450.00                   |
|                                       |
|  +-----------+    +-----------+       |
|  |   SPEND   |    |  STASH    |       |
|  | $1,715.00 |    |  $735.00  |       |
|  |   (70%)   |    |   (30%)   |       |
|  +-----------+    +-----------+       |
|                                       |
|         Status: ● ACTIVE              |
+---------------------------------------+
```

**System states:**

| State | Meaning |
|-------|---------|
| `ALLOCATING` | Deposit received, split in progress |
| `ACTIVE` | System running normally |
| `PAUSED` | User or compliance hold |

**Intentionally excluded from the UI:**
- Individual asset positions
- Charts or performance timelines
- Trade history
- Percentage returns

Rail shows direction, not detail.

---

## Architecture

### System Overview

```
+------------------------------------------------------------------+
|                         RAIL PLATFORM                             |
+------------------------------------------------------------------+
|                                                                   |
|  +----------+    +------------+    +-----------+                  |
|  | iOS App  | -> | API Gateway| -> | Backend   |                  |
|  | (Client) |    | (Gin/Go)   |    | Services  |                  |
|  +----------+    +------------+    +-----+-----+                  |
|                                          |                        |
|         +-----------+-----------+-----------+-----------+         |
|         |           |           |           |           |         |
|         v           v           v           v           v         |
|    +--------+  +--------+  +--------+  +--------+  +--------+    |
|    | Spend  |  | Invest |  |  Card  |  | Wallet |  |Conductor|   |
|    | Engine |  | Engine |  | Service|  | Service|  | Engine  |   |
|    +--------+  +--------+  +--------+  +--------+  +--------+    |
|                                                                   |
|  +---------------------------------------------------------------+|
|  |                 DATA & INFRASTRUCTURE LAYER                   ||
|  |  +----------+  +-------+  +--------+  +--------+  +------+   ||
|  |  |PostgreSQL|  | Redis |  | Bridge |  | Alpaca |  | Due  |   ||
|  |  | (Ledger) |  |(Cache)|  |(Wallet)|  |(Broker)|  |(NGN) |   ||
|  |  +----------+  +-------+  +--------+  +--------+  +------+   ||
|  +---------------------------------------------------------------+|
+------------------------------------------------------------------+
```

### Service Decomposition

| Service | Responsibility | External Dependencies |
|---------|---------------|----------------------|
| **Onboarding** | Registration, KYC orchestration, wallet provisioning | Bridge |
| **Funding** | Virtual accounts, multi-chain USDC deposits, 70/30 split execution | Bridge, Due, Blockchain RPCs |
| **Spending** | Card transactions, round-ups, balance management, ledger | Bridge Cards |
| **Investing** | Auto-allocation, trade execution, portfolio management | Alpaca |
| **Wallet** | Multi-chain wallet management, address generation, custody | Bridge |
| **Conductor** | Copy trading, track management, follower trade mirroring | Alpaca |
| **Yield** | Yield distribution, stash balance reconciliation | Reflect Money |

### Workers

Background workers handle async operations that cannot block the request path:

| Worker | Purpose |
|--------|---------|
| `allocation` | Executes 70/30 split after deposit confirmation |
| `autoinvest` | Deploys stash capital to Alpaca strategies |
| `yield_distribution` | Distributes yield to stash balances |
| `reconciliation` | Reconciles Bridge wallet balances against ledger |
| `conductor_copy` | Mirrors Conductor trades to follower accounts |
| `recovery` | Retries failed allocations and stuck deposits |

### Project Structure

```
rail_service/
├── cmd/
│   └── main.go                     # Server entry point
│
├── internal/
│   ├── api/
│   │   ├── handlers/               # HTTP request handlers
│   │   ├── middleware/             # Auth, logging, rate limiting
│   │   └── routes/                 # Route definitions
│   │
│   ├── domain/
│   │   ├── entities/               # Core business entities
│   │   ├── repositories/           # Repository interfaces
│   │   └── services/               # Business logic
│   │       ├── allocation/         # 70/30 split engine
│   │       ├── autoinvest/         # Invest engine
│   │       ├── funding/            # Deposit handling
│   │       ├── onboarding/         # KYC + wallet provisioning
│   │       ├── spending/           # Card + ledger
│   │       └── yield_distribution/ # Yield distribution
│   │
│   ├── infrastructure/
│   │   ├── adapters/
│   │   │   ├── bridge/             # Bridge API client
│   │   │   └── alpaca/             # Alpaca API client
│   │   ├── cache/                  # Redis
│   │   ├── config/                 # Configuration
│   │   ├── database/               # PostgreSQL
│   │   ├── di/                     # Dependency injection
│   │   └── repositories/           # DB implementations
│   │
│   └── workers/                    # Background job processors
│
├── migrations/                     # Database migrations
├── configs/                        # Config files
├── scripts/                        # Build and maintenance scripts
└── test/                           # Test suites
```

---

## Technology Stack

### Core

| Layer | Technology | Version | Purpose |
|-------|------------|---------|---------|
| Language | Go | 1.24 | Backend services |
| Framework | Gin | 1.11 | HTTP routing & middleware |
| Database | PostgreSQL | 15 | Primary data store, double-entry ledger |
| Cache | Redis | 7 | Sessions, rate limiting, job queue |
| SQL | sqlx | 1.4 | SQL extensions for Go |
| Migrations | golang-migrate | 4.19 | Schema management |

### Auth & Security

| Technology | Purpose |
|------------|---------|
| JWT (v5) | Token-based authentication |
| bcrypt | Password hashing |
| AES-256-GCM | PII encryption at rest |
| TLS 1.3 | Transport encryption |
| Apple Sign-In / Google OAuth | Social authentication |

### External Services

| Service | Provider | Purpose |
|---------|----------|---------|
| Custodial wallets | Bridge | USDC custody, multi-chain |
| Virtual accounts | Bridge | USD, GBP fiat deposit rails |
| NGN virtual accounts | Due Network | NGN bank transfer rails |
| Yield | Reflect Money | Treasury-backed stablecoin yield |
| Debit card | Bridge Cards | Visa card issuance and spending |
| Brokerage | Alpaca | Stock/ETF trade execution |
| KYC | Bridge KYC | Identity verification |
| Email | SendGrid | Transactional notifications |

### Observability

| Tool | Purpose |
|------|---------|
| Zap | Structured JSON logging |
| Prometheus | Metrics collection |
| Grafana | Metrics dashboards |
| OpenTelemetry | Distributed tracing |

---

## Getting Started

### Prerequisites

- Go 1.24+
- Docker & Docker Compose
- PostgreSQL 15
- Redis 7

### Quick Start

```bash
git clone https://github.com/your-org/rail_service.git
cd rail_service
cp configs/config.yaml.example configs/config.yaml
docker-compose up -d
go run cmd/main.go
```

### Docker Compose Profiles

```bash
# Basic (PostgreSQL, Redis, App)
docker-compose up -d

# With admin tools
docker-compose --profile admin up -d

# With monitoring
docker-compose --profile monitoring up -d

# Full stack
docker-compose --profile admin --profile monitoring up -d
```

### Environment Variables

**Required:**

```bash
DATABASE_URL="postgres://postgres:postgres@localhost:5432/rail?sslmode=disable"
JWT_SECRET="your-256-bit-secret-key"
ENCRYPTION_KEY="your-32-byte-encryption-key"
BRIDGE_API_KEY="your-bridge-api-key"
ALPACA_API_KEY="your-alpaca-api-key"
ALPACA_SECRET_KEY="your-alpaca-secret-key"
```

**Optional:**

```bash
LOG_LEVEL="info"              # debug, info, warn, error
ENVIRONMENT="development"     # development, staging, production
PORT="8080"
REDIS_URL="localhost:6379"
SENDGRID_API_KEY="..."
DUE_API_KEY="..."             # NGN virtual accounts
```

### Database Management

```bash
# Migrations run automatically on startup
go run cmd/main.go

# Manual migration
make migrate-up

# Reset (development only)
./scripts/db_reset.sh

# Reset with seed data
./scripts/db_reset.sh --seed
```

### Building

```bash
# Development
go build -o rail_service cmd/main.go

# Production
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o rail_service cmd/main.go

# Docker
docker build -t rail_service:latest .
```

---

## API Reference

### Authentication

All protected endpoints require a JWT bearer token:

```
Authorization: Bearer <access_token>
```

### Response Format

```json
{
  "data": {},
  "meta": {
    "request_id": "uuid",
    "timestamp": "2026-01-01T00:00:00Z"
  }
}
```

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "Human readable message",
    "details": {}
  },
  "meta": {
    "request_id": "uuid",
    "timestamp": "2026-01-01T00:00:00Z"
  }
}
```

Interactive docs at `/swagger/index.html` when running locally.

---

## Testing

```bash
# All tests
go test ./...

# With race detection
go test -race ./...

# Specific package
go test -v ./internal/domain/services/funding/...

# Coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

---

## Monitoring

### Health Checks

| Endpoint | Description |
|----------|-------------|
| `GET /health` | Application health + DB check |
| `GET /health/ready` | Readiness probe |
| `GET /health/live` | Liveness probe |

### Key Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `rail_http_requests_total` | Counter | Total HTTP requests |
| `rail_http_request_duration_seconds` | Histogram | Request latency |
| `rail_deposit_split_duration_seconds` | Histogram | Deposit-to-split latency |
| `rail_trade_execution_total` | Counter | Trade executions |
| `rail_active_users` | Gauge | Active users |

### Performance Targets

| Metric | Target |
|--------|--------|
| Deposit to split | < 60s P95 |
| API response time | < 200ms P95 |
| Trade execution | < 5s P95 |
| Uptime | 99.9% |
| Ledger accuracy | 99.99% |

---

## Compliance

- KYC/AML verification required before any funding
- No investment advice language in UI or communications
- No return guarantees or projections
- 70/30 split disclosed before first deposit
- Full audit trail for all financial transactions
- PII encrypted at rest (AES-256-GCM), masked in logs
- GDPR-compliant data export and deletion

---

## Philosophy

Rail is built on one belief:

> Money should start working the moment it arrives.

**What Rail is:**
- A rules-based capital engine
- A product that replaces financial decision-making
- Infrastructure for passive wealth accumulation

**What Rail is not:**
- A brokerage
- A trading app
- A robo-advisor
- A crypto exchange

Those products require participation. Rail requires trust.

If a user feels the need to manage, optimize, or control their allocation, the product has failed its mission.

---

## Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/your-feature`
3. Commit: `git commit -m 'feat: description'`
4. Push: `git push origin feature/your-feature`
5. Open a Pull Request

**Code standards:**
- `go fmt ./...` before committing
- `go vet ./...` for static analysis
- `go test ./...` must pass
- Follow [Development Guidelines](/.kiro/rules/memory-bank/guidelines.md)

---

## License

MIT License — see [LICENSE](LICENSE)

---

## Support

- Issues: GitHub Issues
- API Docs: `/swagger/index.html`
- Metrics: `/metrics`
