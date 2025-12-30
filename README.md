<![CDATA[# Rail

<p align="center">
  <strong>An automated wealth system where money begins working the moment it arrives.</strong>
</p>

<p align="center">
  <a href="#the-problem">Problem</a> •
  <a href="#how-rail-works">How It Works</a> •
  <a href="#architecture">Architecture</a> •
  <a href="#getting-started">Getting Started</a> •
  <a href="#api-reference">API</a>
</p>

---

## Overview

Rail is a rules-based capital engine designed for Gen Z users who want financial progress without the cognitive burden of traditional investing. When money enters Rail, it doesn't wait—it immediately goes to work through an automatic 70/30 split between spending and investing.

**Core Principle**: Money should start working the moment it arrives.

### Key Differentiators

| Traditional Finance | Rail |
|---------------------|------|
| User chooses assets | System allocates automatically |
| Multiple apps for spending/investing | Single unified platform |
| Requires financial literacy | Requires only trust |
| Decision paralysis | Instant deployment |
| Manual rebalancing | Automated management |

---

## The Problem

Modern investing demands too much from users:

- **Choice Overload**: Choose assets they don't understand
- **Risk Assessment**: Assess risk they can't quantify  
- **Market Timing**: Time markets they don't trust
- **Decision Regret**: Live with decisions they constantly second-guess

Modern finance is fragmented across bank accounts, investing apps, and tracking tools. Each demands attention, decisions, and financial literacy. The result isn't empowerment—it's cognitive overload.

**Access was solved years ago. Direction was not.**

Rail exists to eliminate responsibility without eliminating upside.

### Target User: The Indecisive Optimist (18-26)

**Jobs-To-Be-Done:**
- Spend money without friction
- Avoid financial decision-making
- Make progress toward wealth passively

**Design Constraint:** User attention is limited. Any screen requiring explanation is a failure.

---

## How Rail Works

### The Rail Split (Non-Negotiable Core Rule)

Every deposit is automatically divided the moment it clears:

```
┌─────────────────────────────────────────────────────────────┐
│                    DEPOSIT ARRIVES                          │
└─────────────────────────┬───────────────────────────────────┘
                          │
                          ▼
              ┌───────────────────────┐
              │     SPLIT ENGINE      │
              │   (Automatic 70/30)   │
              └───────────┬───────────┘
                          │
            ┌─────────────┴─────────────┐
            │                           │
            ▼                           ▼
   ┌─────────────────┐        ┌─────────────────┐
   │   70% SPEND     │        │   30% INVEST    │
   │                 │        │                 │
   │ • Liquid        │        │ • Auto-deployed │
   │ • Card-linked   │        │ • AI-managed    │
   │ • Real-time     │        │ • Rules-based   │
   └─────────────────┘        └─────────────────┘
```

| Property | Description |
|----------|-------------|
| **System-defined** | Users cannot modify the ratio |
| **Always on** | No opt-out in MVP |
| **Instant** | Split happens within seconds |
| **Transparent** | Disclosed before first deposit |

**Depositing funds equals consent to system behavior.**

### Funding Methods

Rail supports two funding channels, both triggering identical split behavior:

#### 1. Virtual Accounts (Fiat)

```
Bank Account ──► bridge Network Virtual Account ──► Rail
                      (USD or GBP)
```

- Dedicated USD or GBP virtual account per user
- Standard bank transfer (ACH/Wire/Faster Payments)
- Webhook notification on deposit arrival
- Automatic conversion and split

#### 2. Crypto On-Ramp (Stablecoins)

```
External Wallet ──► Circle Deposit Address ──► Rail
                    (USDC on any chain)
```

| Chain | Token | Confirmation Time |
|-------|-------|-------------------|
| Ethereum | USDC | ~12 confirmations |
| Polygon | USDC | ~128 confirmations |
| BSC | USDC | ~15 confirmations |
| Solana | USDC | ~32 confirmations |

---

## Core Features

### 1. Spend Layer

The primary financial surface, designed to fully replace a traditional checking account.

**Capabilities:**
- Real-time spendable balance with ledger-backed accuracy
- Virtual debit card (physical card post-MVP)
- Instant access to funds
- Transaction history

**Round-Up Automation:**
```
Card Transaction: $4.50
Round-up Amount:  $5.00 - $4.50 = $0.50
                           │
                           ▼
                    Invest Engine
                  (Queued for allocation)
```

- Simple ON/OFF toggle
- No configuration granularity
- Spare change automatically routes to investing

### 2. Invest Engine

Capital deploys automatically with zero user interaction.

**How Allocation Works:**

```
30% Deposit Arrives
        │
        ▼
┌───────────────────┐
│ Strategy Selector │
│                   │
│ Inputs:           │
│ • Age             │
│ • Region          │
│ • Deposit size    │
│ • Deposit freq    │
└─────────┬─────────┘
          │
          ▼
┌───────────────────┐
│  Asset Allocation │
│                   │
│ Example:          │
│ • ETFs: 60%       │
│ • Tech: 25%       │
│ • Stable: 15%     │
└─────────┬─────────┘
          │
          ▼
┌───────────────────┐
│  Alpaca Brokerage │
│  (Trade Execution)│
└───────────────────┘
```

**UX Rules:**
- No asset visibility to users
- No trade confirmations
- No strategy choices presented
- Global fallback strategy as default

### 3. Conductors (Expert-Led Tracks)

For users who want guided growth without self-directed decisions.

**The Metaphor:**
- **Conductor**: A verified professional investor who leads the journey
- **Track**: A curated portfolio path (e.g., "Tech Growth", "Dividend Income")
- **Followers**: Users whose capital automatically mirrors the Conductor's moves

**How Copy Trading Works:**

```
┌─────────────────────────────────────────────────────────────┐
│                    CONDUCTOR UPDATES TRACK                   │
│              (Adds AAPL, removes TSLA, reweights)           │
└─────────────────────────┬───────────────────────────────────┘
                          │
                          ▼
              ┌───────────────────────┐
              │     Copy Engine       │
              │                       │
              │ 1. Get all followers  │
              │ 2. Calculate trades   │
              │ 3. Queue execution    │
              └───────────┬───────────┘
                          │
          ┌───────────────┼───────────────┐
          │               │               │
          ▼               ▼               ▼
    ┌──────────┐   ┌──────────┐   ┌──────────┐
    │Follower A│   │Follower B│   │Follower C│
    │ $1,000   │   │ $5,000   │   │ $500     │
    │allocated │   │allocated │   │allocated │
    └──────────┘   └──────────┘   └──────────┘
          │               │               │
          └───────────────┼───────────────┘
                          │
                          ▼
              ┌───────────────────────┐
              │   Alpaca Brokerage    │
              │  (Proportional trades)│
              └───────────────────────┘

⏱️ Target: Trades mirrored within 5 minutes
```

**Conductor Application Flow:**

```
Existing Rail User
        │
        ▼ Submit Application
┌───────────────────┐
│ Application Form  │
│                   │
│ • Experience      │
│ • Credentials     │
│ • Strategy desc   │
└─────────┬─────────┘
          │
          ▼
┌───────────────────┐
│   Admin Review    │
│                   │
│ • Background check│
│ • Credential verify│
│ • Compliance review│
└─────────┬─────────┘
          │
    ┌─────┴─────┐
    │           │
    ▼           ▼
┌───────┐   ┌───────┐
│Approved│   │Rejected│
└───┬───┘   └───────┘
    │
    ▼
┌───────────────────┐
│ Conductor Setup   │
│                   │
│ • Create profile  │
│ • Build 1st Track │
│ • Accept terms    │
└───────────────────┘
```

**Track Characteristics:**

| Attribute | Description |
|-----------|-------------|
| Name | Strategy identifier (e.g., "Tech Growth") |
| Assets | Curated list with target weights |
| Risk Level | Low / Medium / High indicator |
| Performance | Historical returns visible to followers |
| Follower Count | Social proof metric |

### 4. The Station (Home Screen)

Answers one question only: *"Is my money working?"*

```
┌─────────────────────────────────────────┐
│              THE STATION                │
├─────────────────────────────────────────┤
│                                         │
│         Total Balance                   │
│           $2,450.00                     │
│                                         │
│  ┌─────────────┐  ┌─────────────┐      │
│  │   SPEND     │  │   INVEST    │      │
│  │  $1,715.00  │  │   $735.00   │      │
│  │    (70%)    │  │    (30%)    │      │
│  └─────────────┘  └─────────────┘      │
│                                         │
│         Status: ● ACTIVE                │
│                                         │
└─────────────────────────────────────────┘
```

**System States:**

| State | Meaning | Duration |
|-------|---------|----------|
| `ALLOCATING` | Money being deployed | < 60 seconds |
| `ACTIVE` | System operating normally | Default |
| `PAUSED` | User or compliance initiated | Rare |

**Explicitly Excluded:**
- Individual asset positions
- Charts or timelines
- Trade history
- Performance percentages

Rail communicates direction, not detail.

---

## Architecture

### High-Level System Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              RAIL PLATFORM                                   │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐                   │
│  │   iOS App    │    │  API Gateway │    │   Backend    │                   │
│  │   (Client)   │───▶│   (Gin/Go)   │───▶│   Services   │                   │
│  └──────────────┘    └──────────────┘    └──────────────┘                   │
│                                                 │                            │
│                    ┌────────────────────────────┼────────────────────────┐   │
│                    │                            │                        │   │
│                    ▼                            ▼                        ▼   │
│           ┌──────────────┐            ┌──────────────┐          ┌──────────┐│
│           │   Spend      │            │   Invest     │          │   Card   ││
│           │   Engine     │            │   Engine     │          │  Service ││
│           └──────────────┘            └──────────────┘          └──────────┘│
│                    │                            │                        │   │
│                    ▼                            ▼                        ▼   │
│  ┌─────────────────────────────────────────────────────────────────────────┐│
│  │                        DATA & INFRASTRUCTURE LAYER                      ││
│  │  ┌────────────┐  ┌────────────┐  ┌────────────┐  ┌────────────┐        ││
│  │  │ PostgreSQL │  │   Redis    │  │   Circle   │  │   Alpaca   │        ││
│  │  │  (Ledger)  │  │  (Cache)   │  │  (Wallet)  │  │ (Brokerage)│        ││
│  │  └────────────┘  └────────────┘  └────────────┘  └────────────┘        ││
│  └─────────────────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────────────────┘
```

### Domain Model

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           RAIL DOMAIN MODEL                                  │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                            USER DOMAIN                               │    │
│  │  ┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐       │    │
│  │  │   User   │───▶│  Profile │───▶│   KYC    │───▶│ Settings │       │    │
│  │  └──────────┘    └──────────┘    └──────────┘    └──────────┘       │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                    │                                         │
│                                    ▼                                         │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                          ACCOUNT DOMAIN                              │    │
│  │  ┌──────────────────────────────────────────────────────────────┐   │    │
│  │  │                      RAIL ACCOUNT                             │   │    │
│  │  │  ┌────────────────┐              ┌────────────────┐          │   │    │
│  │  │  │  SPEND BALANCE │              │ INVEST BALANCE │          │   │    │
│  │  │  │     (70%)      │              │     (30%)      │          │   │    │
│  │  │  └────────────────┘              └────────────────┘          │   │    │
│  │  └──────────────────────────────────────────────────────────────┘   │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                    │                                         │
│                                    ▼                                         │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                        TRANSACTION DOMAIN                            │    │
│  │  ┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐       │    │
│  │  │ Deposits │    │  Spends  │    │ Round-ups│    │  Trades  │       │    │
│  │  └──────────┘    └──────────┘    └──────────┘    └──────────┘       │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Bounded Contexts

```
┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
│   ONBOARDING    │  │    FUNDING      │  │    SPENDING     │
│    CONTEXT      │  │    CONTEXT      │  │    CONTEXT      │
├─────────────────┤  ├─────────────────┤  ├─────────────────┤
│ • Registration  │  │ • Deposits      │  │ • Card Txns     │
│ • Apple Sign-In │  │ • Split Engine  │  │ • Round-ups     │
│ • KYC/AML       │  │ • Confirmations │  │ • Balance Mgmt  │
│ • Wallet Setup  │  │ • Webhooks      │  │ • Ledger Ops    │
└────────┬────────┘  └────────┬────────┘  └────────┬────────┘
         │                    │                    │
         └────────────────────┼────────────────────┘
                              │
                              ▼
         ┌─────────────────────────────────────────────┐
         │              INVESTING CONTEXT              │
         ├─────────────────────────────────────────────┤
         │ • Auto-allocation    • Strategy Engine      │
         │ • Trade Execution    • Portfolio Tracking   │
         │ • Position Mgmt      • Performance Calc     │
         └─────────────────────────────────────────────┘
```

### Service Decomposition

| Service | Responsibility | External Dependencies |
|---------|---------------|----------------------|
| **Onboarding** | Registration, KYC orchestration, wallet provisioning | KYC Provider, Circle |
| **Funding** | Virtual accounts (USD/GBP), multi-chain USDC deposits, 70/30 split execution | Circle, bridge Network, Blockchain RPCs |
| **Spending** | Card transactions, round-ups, balance management, ledger operations | Card Issuer |
| **Investing** | Auto-allocation, trade execution, portfolio management | Alpaca |
| **Wallet** | Multi-chain wallet management, address generation, custody | Circle |
| **Conductor** | Copy trading, track management, follower trade mirroring | Alpaca |

### Project Structure

```
rail_service/
├── cmd/                            # Application entry points
│   └── main.go                     # Server initialization, graceful shutdown
│
├── internal/                       # Private application code
│   ├── api/
│   │   ├── handlers/               # HTTP request handlers
│   │   │   ├── auth_handlers.go
│   │   │   ├── funding_handlers.go
│   │   │   ├── spending_handlers.go
│   │   │   └── investing_handlers.go
│   │   ├── middleware/             # Cross-cutting concerns
│   │   │   ├── auth.go             # JWT validation
│   │   │   ├── ratelimit.go        # Rate limiting
│   │   │   ├── logging.go          # Request logging
│   │   │   └── cors.go             # CORS handling
│   │   └── routes/                 # Route definitions
│   │       └── router.go
│   │
│   ├── domain/
│   │   ├── entities/               # Core business entities
│   │   │   ├── user.go
│   │   │   ├── account.go
│   │   │   ├── wallet.go
│   │   │   ├── deposit.go
│   │   │   ├── transaction.go
│   │   │   └── position.go
│   │   ├── repositories/           # Repository interfaces
│   │   │   ├── user_repository.go
│   │   │   ├── account_repository.go
│   │   │   └── wallet_repository.go
│   │   └── services/               # Business logic
│   │       ├── onboarding/
│   │       │   └── onboarding_service.go
│   │       ├── wallet/
│   │       │   └── wallet_service.go
│   │       ├── funding/
│   │       │   ├── funding_service.go
│   │       │   └── split_engine.go
│   │       └── investing/
│   │           ├── investing_service.go
│   │           └── strategy_selector.go
│   │
│   ├── infrastructure/
│   │   ├── adapters/               # External service integrations
│   │   │   └── email/
│   │   ├── cache/                  # Redis caching layer
│   │   ├── circle/                 # Circle API client
│   │   │   ├── client.go
│   │   │   ├── wallets.go
│   │   │   └── webhooks.go
│   │   ├── config/                 # Configuration management
│   │   │   └── config.go
│   │   ├── database/               # PostgreSQL connection
│   │   │   ├── postgres.go
│   │   │   └── migrations.go
│   │   ├── di/                     # Dependency injection
│   │   │   └── container.go
│   │   └── repositories/           # Repository implementations
│   │       ├── postgres_user_repo.go
│   │       └── postgres_account_repo.go
│   │
│   ├── adapters/
│   │   ├── alpaca/                 # Brokerage integration
│   │   │   ├── client.go
│   │   │   ├── trading.go
│   │   │   └── positions.go
│   │   └── bridge/                    # Virtual accounts
│   │       ├── client.go
│   │       └── accounts.go
│   │
│   └── workers/                    # Background job processors
│       ├── funding_webhook/        # Deposit processing
│       ├── wallet_provisioning/    # Wallet creation
│       ├── investment_allocator/   # Trade batching
│       └── onboarding_processor/   # Async KYC
│
├── pkg/                            # Public reusable libraries
│   ├── auth/                       # JWT utilities
│   ├── crypto/                     # AES-256-GCM encryption
│   ├── logger/                     # Structured logging (Zap)
│   ├── retry/                      # Exponential backoff
│   └── circuitbreaker/             # Circuit breaker pattern
│
├── migrations/                     # Database migrations
│   ├── 001_initial_tables.up.sql
│   ├── 001_initial_tables.down.sql
│   └── ...
│
├── configs/                        # Configuration files
│   ├── config.yaml
│   └── config.yaml.example
│
├── scripts/                        # Build and maintenance
│   ├── db_wipe.sh
│   ├── db_reset.sh
│   └── test_api.sh
│
├── docs/                           # Documentation
│   ├── architecture/
│   ├── prd.md
│   └── Rail-Brief.md
│
└── test/
    ├── unit/
    └── integration/
```
]]>
<![CDATA[

---

## Data Flow Architecture

### Core Flow: Deposit → Split → Deploy

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    DEPOSIT → SPLIT → DEPLOY FLOW                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  USER                                                                        │
│    │                                                                         │
│    │ 1. Load Money (choose method)                                           │
│    │                                                                         │
│    ├─────────────────────────┬─────────────────────────┐                     │
│    │                         │                         │                     │
│    ▼                         ▼                         │                     │
│  ┌──────────────┐    ┌──────────────┐                  │                     │
│  │   Virtual    │    │  Multi-Chain │                  │                     │
│  │   Account    │    │    USDC      │                  │                     │
│  │  (USD/GBP)   │    │   Deposit    │                  │                     │
│  └──────┬───────┘    └──────┬───────┘                  │                     │
│         │                   │                          │                     │
│         │ Bank Transfer     │ ETH/Polygon/BSC/Solana   │                     │
│         ▼                   ▼                          │                     │
│  ┌──────────────┐    ┌──────────────┐                  │                     │
│  │ bridge Network  │    │   Circle     │                  │                     │
│  │   Webhook    │    │   Webhook    │                  │                     │
│  └──────┬───────┘    └──────┬───────┘                  │                     │
│         │                   │                          │                     │
│         └─────────┬─────────┘                          │                     │
│                   │                                    │                     │
│                   ▼                                    │                     │
│            ┌──────────────┐                            │                     │
│            │   Funding    │  2. Receive notification   │                     │
│            │   Service    │                            │                     │
│            └──────┬───────┘                            │                     │
│                   │                                    │                     │
│                   │ 3. Validate & Confirm              │                     │
│                   ▼                                    │                     │
│            ┌──────────────┐                            │                     │
│            │    Split     │  4. Apply 70/30 Rule       │                     │
│            │   Engine     │                            │                     │
│            └──────┬───────┘                            │                     │
│                   │                                                          │
│              ┌────┴────┐                                                     │
│              │         │                                                     │
│              ▼         ▼                                                     │
│            ┌────┐   ┌────┐                                                   │
│            │70% │   │30% │                                                   │
│            └──┬─┘   └──┬─┘                                                   │
│               │        │                                                     │
│               ▼        ▼                                                     │
│        ┌──────────────┐    ┌──────────────┐                                  │
│        │    Spend     │    │   Invest     │                                  │
│        │   Balance    │    │   Engine     │                                  │
│        └──────────────┘    └──────┬───────┘                                  │
│                                   │                                          │
│                                   │ 5. Auto-allocate                         │
│                                   ▼                                          │
│                            ┌──────────────┐                                  │
│                            │   Strategy   │                                  │
│                            │   Selector   │                                  │
│                            └──────┬───────┘                                  │
│                                   │                                          │
│                                   │ 6. Execute trades                        │
│                                   ▼                                          │
│                            ┌──────────────┐                                  │
│                            │    Alpaca    │                                  │
│                            │  (Brokerage) │                                  │
│                            └──────────────┘                                  │
│                                                                              │
│  ⏱️ Target: < 60 seconds end-to-end                                          │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Card Transaction Flow with Round-ups

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      CARD TRANSACTION + ROUND-UP FLOW                        │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  USER                                                                        │
│    │                                                                         │
│    │ Swipe Card ($4.50)                                                      │
│    ▼                                                                         │
│  ┌──────────────┐                                                            │
│  │    Card      │                                                            │
│  │   Issuer     │                                                            │
│  └──────┬───────┘                                                            │
│         │                                                                    │
│         │ Authorization Request                                              │
│         ▼                                                                    │
│  ┌──────────────┐                                                            │
│  │   Spending   │  1. Check Spend Balance                                    │
│  │   Service    │  2. Authorize $4.50                                        │
│  └──────┬───────┘                                                            │
│         │                                                                    │
│         │ If Round-ups ON                                                    │
│         ▼                                                                    │
│  ┌──────────────┐                                                            │
│  │   Round-up   │  3. Calculate: $5.00 - $4.50 = $0.50                       │
│  │   Engine     │                                                            │
│  └──────┬───────┘                                                            │
│         │                                                                    │
│         │ Route $0.50                                                        │
│         ▼                                                                    │
│  ┌──────────────┐                                                            │
│  │   Invest     │  4. Add to Invest Balance                                  │
│  │   Engine     │  5. Queue for allocation                                   │
│  └──────────────┘                                                            │
│                                                                              │
│  LEDGER UPDATE:                                                              │
│  ├─ Spend Balance: -$5.00                                                    │
│  └─ Invest Balance: +$0.50                                                   │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Onboarding Sequence

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         ONBOARDING SEQUENCE                                  │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  iOS App          API Gateway       Onboarding       KYC Provider    Circle  │
│    │                  │               Service            │             │     │
│    │  1. Apple Sign-In│                 │                │             │     │
│    │─────────────────▶│                 │                │             │     │
│    │                  │  2. Create User │                │             │     │
│    │                  │────────────────▶│                │             │     │
│    │                  │                 │  3. Init KYC   │             │     │
│    │                  │                 │───────────────▶│             │     │
│    │                  │                 │                │             │     │
│    │                  │                 │  4. KYC Result │             │     │
│    │                  │                 │◀───────────────│             │     │
│    │                  │                 │                │             │     │
│    │                  │                 │  5. Create Wallet            │     │
│    │                  │                 │─────────────────────────────▶│     │
│    │                  │                 │                              │     │
│    │                  │                 │  6. Wallet Created           │     │
│    │                  │                 │◀─────────────────────────────│     │
│    │                  │                 │                │             │     │
│    │  7. Onboarding Complete           │                │             │     │
│    │◀─────────────────│◀────────────────│                │             │     │
│    │                  │                 │                │             │     │
│                                                                              │
│  ⏱️ Target: < 2 minutes total                                                │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Database Schema

### Core Entities

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         CORE DATABASE SCHEMA                                 │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌─────────────────┐         ┌─────────────────┐                            │
│  │      users      │         │    profiles     │                            │
│  ├─────────────────┤         ├─────────────────┤                            │
│  │ id (PK)         │────────▶│ user_id (FK)    │                            │
│  │ email           │         │ first_name      │                            │
│  │ phone           │         │ last_name       │                            │
│  │ passcode_hash   │         │ kyc_status      │                            │
│  │ created_at      │         │ kyc_provider_id │                            │
│  └────────┬────────┘         └─────────────────┘                            │
│           │                                                                  │
│           │ 1:N                                                              │
│           ▼                                                                  │
│  ┌─────────────────┐         ┌─────────────────┐                            │
│  │    accounts     │         │    wallets      │                            │
│  ├─────────────────┤         ├─────────────────┤                            │
│  │ id (PK)         │         │ id (PK)         │                            │
│  │ user_id (FK)    │         │ user_id (FK)    │                            │
│  │ spend_balance   │         │ chain           │                            │
│  │ invest_balance  │         │ address         │                            │
│  │ total_balance   │         │ provider_ref    │                            │
│  │ status          │         │ status          │                            │
│  └────────┬────────┘         └─────────────────┘                            │
│           │                                                                  │
│           │ 1:N                                                              │
│           ▼                                                                  │
│  ┌─────────────────┐         ┌─────────────────┐                            │
│  │  transactions   │         │    deposits     │                            │
│  ├─────────────────┤         ├─────────────────┤                            │
│  │ id (PK)         │         │ id (PK)         │                            │
│  │ account_id (FK) │         │ wallet_id (FK)  │                            │
│  │ type            │         │ amount          │                            │
│  │ amount          │         │ chain           │                            │
│  │ balance_type    │         │ tx_hash         │                            │
│  │ reference_id    │         │ status          │                            │
│  │ created_at      │         │ split_executed  │                            │
│  └─────────────────┘         └─────────────────┘                            │
│                                                                              │
│  ┌─────────────────┐         ┌─────────────────┐                            │
│  │   positions     │         │     trades      │                            │
│  ├─────────────────┤         ├─────────────────┤                            │
│  │ id (PK)         │         │ id (PK)         │                            │
│  │ account_id (FK) │         │ position_id(FK) │                            │
│  │ symbol          │         │ side            │                            │
│  │ quantity        │         │ quantity        │                            │
│  │ avg_cost        │         │ price           │                            │
│  │ current_value   │         │ broker_ref      │                            │
│  └─────────────────┘         └─────────────────┘                            │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Conductor Domain Schema

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       CONDUCTOR DATABASE SCHEMA                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌─────────────────┐         ┌─────────────────┐                            │
│  │   conductor_    │         │   conductors    │                            │
│  │  applications   │         ├─────────────────┤                            │
│  ├─────────────────┤         │ id (PK)         │                            │
│  │ id (PK)         │────────▶│ user_id (FK)    │                            │
│  │ user_id (FK)    │         │ application_id  │                            │
│  │ status          │         │ display_name    │                            │
│  │ experience_yrs  │         │ bio             │                            │
│  │ credentials     │         │ total_followers │                            │
│  │ reviewed_by     │         │ total_aum       │                            │
│  │ reviewed_at     │         │ status          │                            │
│  └─────────────────┘         └────────┬────────┘                            │
│                                       │                                      │
│                                       │ 1:N                                  │
│                                       ▼                                      │
│  ┌─────────────────┐         ┌─────────────────┐                            │
│  │  track_assets   │         │     tracks      │                            │
│  ├─────────────────┤         ├─────────────────┤                            │
│  │ id (PK)         │◀────────│ id (PK)         │                            │
│  │ track_id (FK)   │         │ conductor_id(FK)│                            │
│  │ symbol          │         │ name            │                            │
│  │ asset_type      │         │ description     │                            │
│  │ target_weight   │         │ risk_level      │                            │
│  │ current_weight  │         │ status          │                            │
│  └─────────────────┘         │ follower_count  │                            │
│                              │ performance_*   │                            │
│                              └────────┬────────┘                            │
│                                       │                                      │
│                                       │ 1:N                                  │
│                                       ▼                                      │
│  ┌─────────────────┐         ┌─────────────────┐                            │
│  │ follower_trades │         │ track_followers │                            │
│  ├─────────────────┤         ├─────────────────┤                            │
│  │ id (PK)         │◀────────│ id (PK)         │                            │
│  │ follower_id(FK) │         │ track_id (FK)   │                            │
│  │ user_id (FK)    │         │ user_id (FK)    │                            │
│  │ symbol          │         │ allocation_amt  │                            │
│  │ side            │         │ allocation_pct  │                            │
│  │ quantity        │         │ status          │                            │
│  │ status          │         │ started_at      │                            │
│  └─────────────────┘         └─────────────────┘                            │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Account State Machine

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        ACCOUNT STATE MACHINE                                 │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│                          ┌─────────────┐                                     │
│                          │   CREATED   │                                     │
│                          └──────┬──────┘                                     │
│                                 │                                            │
│                                 │ KYC Submitted                              │
│                                 ▼                                            │
│                          ┌─────────────┐                                     │
│                          │ KYC_PENDING │                                     │
│                          └──────┬──────┘                                     │
│                                 │                                            │
│                    ┌────────────┼────────────┐                               │
│                    │            │            │                               │
│                    ▼            │            ▼                               │
│             ┌───────────┐       │     ┌───────────┐                          │
│             │KYC_FAILED │       │     │KYC_REVIEW │                          │
│             └───────────┘       │     └─────┬─────┘                          │
│                                 │           │                                │
│                                 ▼           │                                │
│                          ┌─────────────┐    │                                │
│                          │   ACTIVE    │◀───┘                                │
│                          └──────┬──────┘                                     │
│                                 │                                            │
│                    ┌────────────┼────────────┐                               │
│                    │            │            │                               │
│                    ▼            ▼            ▼                               │
│             ┌───────────┐ ┌───────────┐ ┌───────────┐                        │
│             │  FROZEN   │ │ SUSPENDED │ │  CLOSED   │                        │
│             └───────────┘ └───────────┘ └───────────┘                        │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Background Workers

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       BACKGROUND WORKERS                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                        WORKER POOL                                   │    │
│  │                                                                      │    │
│  │  ┌──────────────────┐    ┌──────────────────┐                       │    │
│  │  │ Wallet           │    │ Funding          │                       │    │
│  │  │ Provisioning     │    │ Webhook          │                       │    │
│  │  ├──────────────────┤    ├──────────────────┤                       │    │
│  │  │ • Create wallets │    │ • Process events │                       │    │
│  │  │ • Multi-chain    │    │ • Confirm txns   │                       │    │
│  │  │ • Retry logic    │    │ • Trigger split  │                       │    │
│  │  └──────────────────┘    └──────────────────┘                       │    │
│  │                                                                      │    │
│  │  ┌──────────────────┐    ┌──────────────────┐                       │    │
│  │  │ Onboarding       │    │ Investment       │                       │    │
│  │  │ Processor        │    │ Allocator        │                       │    │
│  │  ├──────────────────┤    ├──────────────────┤                       │    │
│  │  │ • Async KYC      │    │ • Batch trades   │                       │    │
│  │  │ • Status updates │    │ • Rebalancing    │                       │    │
│  │  │ • Notifications  │    │ • Round-up batch │                       │    │
│  │  └──────────────────┘    └──────────────────┘                       │    │
│  │                                                                      │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                    │                                         │
│                                    ▼                                         │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                         JOB QUEUE (Redis)                            │    │
│  │  • Retry with exponential backoff                                    │    │
│  │  • Dead letter queue for failures                                    │    │
│  │  • Priority queues for critical jobs                                 │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## External Integrations

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       EXTERNAL INTEGRATIONS                                  │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│                           ┌──────────────┐                                   │
│                           │  RAIL CORE   │                                   │
│                           └──────┬───────┘                                   │
│                                  │                                           │
│    ┌─────────────────────────────┼─────────────────────────────┐             │
│    │                             │                             │             │
│    ▼                             ▼                             ▼             │
│  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐           │
│  │     CIRCLE       │  │     ALPACA       │  │   bridge NETWORK    │           │
│  │  (Wallet Infra)  │  │   (Brokerage)    │  │ (Virtual Accts)  │           │
│  ├──────────────────┤  ├──────────────────┤  ├──────────────────┤           │
│  │ • Wallet Create  │  │ • Trade Execute  │  │ • USD/GBP Accts  │           │
│  │ • Deposit Addr   │  │ • Market Data    │  │ • Bank Transfers │           │
│  │ • USDC Custody   │  │ • Positions      │  │ • Off-ramping    │           │
│  │ • Webhooks       │  │ • Account Mgmt   │  │ • Webhooks       │           │
│  └──────────────────┘  └──────────────────┘  └──────────────────┘           │
│                                                                              │
│    ┌─────────────────────────────────────────────────────────────┐          │
│    │                    BLOCKCHAIN NETWORKS                       │          │
│    │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐     │          │
│    │  │ Ethereum │  │ Polygon  │  │   BSC    │  │  Solana  │     │          │
│    │  │  (EVM)   │  │  (EVM)   │  │  (EVM)   │  │(Non-EVM) │     │          │
│    │  └──────────┘  └──────────┘  └──────────┘  └──────────┘     │          │
│    └─────────────────────────────────────────────────────────────┘          │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Integration Details

| Provider | Purpose | Key Operations |
|----------|---------|----------------|
| **Circle** | Wallet infrastructure & stablecoin custody | Create wallets, generate deposit addresses, USDC transfers, webhook notifications |
| **Alpaca** | Stock/ETF brokerage | Execute trades, fetch positions, market data, account management |
| **bridge Network** | Virtual bank accounts | Create USD/GBP accounts, process bank transfers, off-ramp to fiat |
| **KYC Provider** | Identity verification | Document verification, AML screening, compliance checks |
| **Card Issuer** | Payment cards | Virtual/physical cards, authorization, settlements |

---

## Security Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         SECURITY ARCHITECTURE                                │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                        PERIMETER SECURITY                            │    │
│  │  • WAF (Web Application Firewall)                                    │    │
│  │  • DDoS Protection                                                   │    │
│  │  • Rate Limiting (100 req/min default)                               │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                    │                                         │
│                                    ▼                                         │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                      AUTHENTICATION LAYER                            │    │
│  │  • Apple Sign-In (Primary)                                           │    │
│  │  • JWT Tokens (Access: 15min, Refresh: 7 days)                       │    │
│  │  • Passcode Authentication (6-digit)                                 │    │
│  │  • Session Management via Redis                                      │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                    │                                         │
│                                    ▼                                         │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                      AUTHORIZATION LAYER                             │    │
│  │  • Resource-based access control                                     │    │
│  │  • User-scoped data isolation                                        │    │
│  │  • KYC status gating for financial features                          │    │
│  │  • Conductor role verification                                       │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                    │                                         │
│                                    ▼                                         │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                        DATA SECURITY                                 │    │
│  │  • AES-256-GCM encryption at rest                                    │    │
│  │  • TLS 1.3 in transit                                                │    │
│  │  • Encrypted wallet keys (never stored plaintext)                    │    │
│  │  • PII masking in logs                                               │    │
│  │  • Audit trail for all financial transactions                        │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Security Measures

| Layer | Implementation |
|-------|----------------|
| **Transport** | TLS 1.3, certificate pinning on iOS |
| **Authentication** | Apple Sign-In, JWT with RS256, bcrypt password hashing |
| **Encryption** | AES-256-GCM for sensitive data, encrypted database columns |
| **Rate Limiting** | 100 req/min per user, 1000 req/min per IP |
| **Audit** | Full audit trail for financial operations, immutable logs |
| **Compliance** | KYC/AML verification required before funding |
]]>
<![CDATA[

---

## Technology Stack

### Core Technologies

| Layer | Technology | Version | Purpose |
|-------|------------|---------|---------|
| **Language** | Go | 1.24 | Backend services |
| **Framework** | Gin | 1.11 | HTTP routing & middleware |
| **Database** | PostgreSQL | 15 | Primary data store, ledger |
| **Cache** | Redis | 7 | Sessions, rate limiting, job queue |
| **ORM/SQL** | sqlx | 1.4 | SQL extensions for Go |
| **Migrations** | golang-migrate | 4.19 | Database schema management |

### Authentication & Security

| Technology | Purpose |
|------------|---------|
| JWT (v5) | Token-based authentication |
| bcrypt | Password hashing |
| AES-256-GCM | Data encryption at rest |
| TLS 1.3 | Transport encryption |

### External Services

| Service | Provider | Purpose |
|---------|----------|---------|
| Wallet Infrastructure | Circle | Multi-chain wallets, USDC custody |
| Brokerage | Alpaca | Stock/ETF trading |
| Virtual Accounts | bridge Network | USD/GBP bank accounts |
| Email | SendGrid | Transactional emails |

### Observability

| Tool | Purpose |
|------|---------|
| Zap | Structured logging |
| Prometheus | Metrics collection |
| Grafana | Metrics visualization |
| OpenTelemetry | Distributed tracing |

### Development Tools

| Tool | Purpose |
|------|---------|
| Docker | Containerization |
| Docker Compose | Local development orchestration |
| Testify | Testing assertions & mocking |
| Swagger | API documentation |

---

## Getting Started

### Prerequisites

- Go 1.24+
- Docker & Docker Compose
- PostgreSQL 15
- Redis 7

### Quick Start

```bash
# Clone repository
git clone https://github.com/your-org/rail_service.git
cd rail_service

# Copy configuration
cp configs/config.yaml.example configs/config.yaml

# Start infrastructure
docker-compose up -d

# Run the application
go run cmd/main.go
```

### Docker Compose Profiles

```bash
# Basic services (PostgreSQL, Redis, App)
docker-compose up -d

# With admin tools (pgAdmin, RedisInsight)
docker-compose --profile admin up -d

# With monitoring (Prometheus, Grafana)
docker-compose --profile monitoring up -d

# Full stack
docker-compose --profile admin --profile monitoring up -d
```

### Environment Variables

#### Required

```bash
export DATABASE_URL="postgres://postgres:postgres@localhost:5432/rail?sslmode=disable"
export JWT_SECRET="your-256-bit-secret-key"
export ENCRYPTION_KEY="your-32-byte-encryption-key"
export CIRCLE_API_KEY="your-circle-api-key"
```

#### Optional

```bash
export LOG_LEVEL="info"              # debug, info, warn, error
export ENVIRONMENT="development"     # development, staging, production
export PORT="8080"                   # Server port
export REDIS_URL="localhost:6379"    # Redis connection
export SENDGRID_API_KEY="..."        # Email service
```

### Database Management

```bash
# Run migrations (automatic on startup)
go run cmd/main.go migrate

# Wipe database (development only)
./scripts/db_wipe.sh

# Reset with fresh migrations
./scripts/db_reset.sh

# Reset with seed data
./scripts/db_reset.sh --seed
```

### Building

```bash
# Development build
go build -o rail_service cmd/main.go

# Production build (optimized)
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o rail_service cmd/main.go

# Docker build
docker build -t rail_service:latest .
```

---

## API Reference

### Authentication

All protected endpoints require JWT token:

```
Authorization: Bearer <access_token>
```

### Endpoint Categories

#### Public Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/v1/auth/register` | Create new account |
| POST | `/api/v1/auth/login` | Authenticate user |
| POST | `/api/v1/auth/refresh` | Refresh access token |
| POST | `/api/v1/auth/logout` | End session |

#### Authenticated Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/v1/onboarding/start` | Begin onboarding flow |
| GET | `/api/v1/onboarding/status` | Check onboarding progress |
| POST | `/api/v1/onboarding/kyc/submit` | Submit KYC documents |

#### Authenticated + KYC Required

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/v1/account` | Get account summary |
| GET | `/api/v1/account/balances` | Get spend/invest balances |
| GET | `/api/v1/account/station` | Home screen data |
| POST | `/api/v1/funding/deposit/address` | Generate deposit address |
| GET | `/api/v1/funding/deposits` | List deposit history |
| GET | `/api/v1/spending/transactions` | Transaction history |
| POST | `/api/v1/spending/roundups/toggle` | Enable/disable round-ups |
| GET | `/api/v1/cards` | List user's cards |
| POST | `/api/v1/cards/virtual` | Create virtual card |
| POST | `/api/v1/cards/{id}/freeze` | Freeze a card |
| GET | `/api/v1/investing/balance` | Invest balance |
| GET | `/api/v1/investing/status` | Allocation status |

#### Webhook Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/v1/webhooks/circle` | Circle deposit notifications |
| POST | `/api/v1/webhooks/bridge` | bridge Network notifications |
| POST | `/api/v1/webhooks/alpaca` | Alpaca trade notifications |

### Response Format

**Success Response:**
```json
{
  "data": { ... },
  "meta": {
    "request_id": "uuid",
    "timestamp": "2025-01-01T00:00:00Z"
  }
}
```

**Error Response:**
```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "Human readable message",
    "details": { ... }
  },
  "meta": {
    "request_id": "uuid",
    "timestamp": "2025-01-01T00:00:00Z"
  }
}
```

### API Documentation

Interactive documentation available at `/swagger/index.html` when running locally.

---

## Testing

### Running Tests

```bash
# All tests
go test ./...

# Unit tests only
go test ./test/unit/...

# Integration tests only
go test ./test/integration/...

# Specific package
go test -v ./internal/domain/services/funding/...

# With race detection
go test -race ./...
```

### Coverage

```bash
# Generate coverage report
go test -coverprofile=coverage.out ./...

# View in browser
go tool cover -html=coverage.out

# Coverage summary
go tool cover -func=coverage.out
```

### Test Scripts

```bash
# Test wallet API
./scripts/test_wallet_api.sh

# Test funding flow
./scripts/test_due_flow.sh

# Test balance API
./scripts/test_balance_api.sh
```

---

## Monitoring & Observability

### Health Checks

| Endpoint | Description |
|----------|-------------|
| `GET /health` | Application health |
| `GET /health/ready` | Readiness probe |
| `GET /health/live` | Liveness probe |

### Metrics

Prometheus metrics available at `GET /metrics`:

| Metric | Type | Description |
|--------|------|-------------|
| `rail_http_requests_total` | Counter | Total HTTP requests |
| `rail_http_request_duration_seconds` | Histogram | Request latency |
| `rail_deposit_split_duration_seconds` | Histogram | Deposit→Split latency |
| `rail_trade_execution_total` | Counter | Trade executions |
| `rail_active_users` | Gauge | Currently active users |

### Logging

Structured JSON logs with correlation IDs:

```json
{
  "level": "info",
  "ts": "2025-01-01T00:00:00.000Z",
  "caller": "funding/service.go:42",
  "msg": "deposit processed",
  "request_id": "uuid",
  "user_id": "uuid",
  "amount": "100.00",
  "chain": "ethereum",
  "duration_ms": 45
}
```

---

## Non-Functional Requirements

### Performance Targets

| Metric | Target | Measurement |
|--------|--------|-------------|
| Deposit → Split latency | < 60 seconds | P95 |
| API response time | < 200ms | P95 |
| Trade execution | < 5 seconds | P95 |
| iOS app launch | < 2 seconds | Cold start |

### Reliability Targets

| Metric | Target |
|--------|--------|
| Uptime | 99.9% |
| Ledger accuracy | 99.99% |
| Crash-free sessions | 99.5% |

### Scalability

| Component | Scaling Strategy |
|-----------|------------------|
| API Servers | Horizontal (ECS/EKS) |
| Workers | Horizontal with queue partitioning |
| Database | Vertical + Read replicas |
| Cache | Redis Cluster |

---

## Compliance & Constraints

### Regulatory Requirements

- KYC/AML verification required before funding
- No investment advice language in UI/communications
- No return promises or guarantees
- Clear disclosure of 70/30 split before first deposit
- Full audit trail for all financial transactions

### Data Handling

- PII encrypted at rest (AES-256-GCM)
- PII masked in logs
- Data retention policies enforced
- GDPR-compliant data export/deletion

---

## Success Metrics

### Primary KPIs

| Metric | Description |
|--------|-------------|
| First-session funding rate | % of users who fund within first session |
| Auto-invest rate | % of deposits that complete auto-investment |
| 7-day retention | % of users keeping automation enabled |

### Secondary KPIs

| Metric | Description |
|--------|-------------|
| DAU/MAU | Daily/Monthly active user ratio |
| Repeat deposit rate | % of users making 2+ deposits |
| Average deposit size | Mean deposit amount |
| Round-up adoption | % of users with round-ups enabled |

---

## Philosophy

Rail is built on a single belief:

> Money should start working the moment it arrives.

### What Rail Is

- An automated capital engine
- A rules-based wealth system
- A product that replaces decision-making

### What Rail Is Not

- A brokerage
- A trading app
- A robo-advisor
- A crypto exchange

Those products require participation. **Rail requires trust.**

If users feel the need to manage, optimize, or control, the product has failed its mission.

---

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit changes (`git commit -m 'Add amazing feature'`)
4. Push to branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

### Code Standards

- Run `go fmt ./...` before committing
- Run `go vet ./...` for static analysis
- Ensure tests pass: `go test ./...`
- Follow [Development Guidelines](/.kiro/rules/memory-bank/guidelines.md)

---

## License

MIT License - see [LICENSE](LICENSE)

---

## Documentation

| Document | Description |
|----------|-------------|
| [Product Brief](/docs/Rail-Brief.md) | Product philosophy and vision |
| [PRD](/docs/prd.md) | Product requirements |
| [System Design](/docs/architecture/system-design.md) | Technical architecture |
| [Development Guidelines](/.kiro/rules/memory-bank/guidelines.md) | Coding standards |

---

## Support

- **Issues**: GitHub Issues
- **API Docs**: `/swagger/index.html`
- **Metrics**: `/metrics`
]]>
