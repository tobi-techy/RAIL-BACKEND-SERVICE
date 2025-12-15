# Rail System Design Document

## Document Information
- **Version**: 1.0
- **Last Updated**: December 14, 2025
- **Status**: Active

---

## 1. Executive Summary

Rail is an automated wealth system that eliminates financial decision-making for Gen Z users. The system automatically splits every deposit (70% Spend / 30% Invest) and deploys capital without user intervention.

**Core Principle**: Money starts working the moment it arrives.

---

## 2. System Overview

### 2.1 High-Level Architecture

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

### 2.2 Design Principles

| Principle | Implementation |
|-----------|----------------|
| **Zero Decisions** | System-defined 70/30 split, no user configuration |
| **Instant Action** | Deposit → Split → Deploy in < 60 seconds |
| **Trust Through Motion** | Capital moves immediately, no confirmations |
| **Invisible Complexity** | Users see balances, not trades or assets |

---

## 3. Core Domain Architecture

### 3.1 Domain Model

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
│  │                                                                      │    │
│  │  ┌──────────────────────────────────────────────────────────────┐   │    │
│  │  │                      RAIL ACCOUNT                             │   │    │
│  │  │  ┌────────────────┐              ┌────────────────┐          │   │    │
│  │  │  │  SPEND BALANCE │              │ INVEST BALANCE │          │   │    │
│  │  │  │     (70%)      │              │     (30%)      │          │   │    │
│  │  │  │                │              │                │          │   │    │
│  │  │  │  • Liquid      │              │  • Deployed    │          │   │    │
│  │  │  │  • Card-linked │              │  • Auto-managed│          │   │    │
│  │  │  │  • Real-time   │              │  • Rules-based │          │   │    │
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

### 3.2 Bounded Contexts

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          BOUNDED CONTEXTS                                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐              │
│  │   ONBOARDING    │  │    FUNDING      │  │    SPENDING     │              │
│  │    CONTEXT      │  │    CONTEXT      │  │    CONTEXT      │              │
│  ├─────────────────┤  ├─────────────────┤  ├─────────────────┤              │
│  │ • Registration  │  │ • Deposits      │  │ • Card Txns     │              │
│  │ • Apple Sign-In │  │ • Split Engine  │  │ • Round-ups     │              │
│  │ • KYC/AML       │  │ • Confirmations │  │ • Balance Mgmt  │              │
│  │ • Wallet Setup  │  │ • Webhooks      │  │ • Ledger Ops    │              │
│  └────────┬────────┘  └────────┬────────┘  └────────┬────────┘              │
│           │                    │                    │                        │
│           └────────────────────┼────────────────────┘                        │
│                                │                                             │
│                                ▼                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                       INVESTING CONTEXT                              │    │
│  ├─────────────────────────────────────────────────────────────────────┤    │
│  │  • Auto-allocation    • Strategy Engine    • Portfolio Tracking     │    │
│  │  • Trade Execution    • Position Mgmt      • Performance Calc       │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 4. Service Architecture

### 4.1 Service Decomposition

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         SERVICE ARCHITECTURE                                 │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│                           ┌──────────────────┐                               │
│                           │   API GATEWAY    │                               │
│                           │   (Gin Router)   │                               │
│                           └────────┬─────────┘                               │
│                                    │                                         │
│         ┌──────────────────────────┼──────────────────────────┐              │
│         │                          │                          │              │
│         ▼                          ▼                          ▼              │
│  ┌──────────────┐          ┌──────────────┐          ┌──────────────┐       │
│  │  Onboarding  │          │   Funding    │          │   Spending   │       │
│  │   Service    │          │   Service    │          │   Service    │       │
│  ├──────────────┤          ├──────────────┤          ├──────────────┤       │
│  │ • Auth       │          │ • Deposits   │          │ • Card Ops   │       │
│  │ • KYC        │          │ • Split      │          │ • Round-ups  │       │
│  │ • Profile    │          │ • Webhooks   │          │ • Ledger     │       │
│  └──────────────┘          └──────────────┘          └──────────────┘       │
│         │                          │                          │              │
│         │                          ▼                          │              │
│         │                  ┌──────────────┐                   │              │
│         │                  │   Investing  │                   │              │
│         │                  │   Service    │                   │              │
│         │                  ├──────────────┤                   │              │
│         │                  │ • Allocation │                   │              │
│         │                  │ • Execution  │                   │              │
│         │                  │ • Portfolio  │                   │              │
│         │                  └──────────────┘                   │              │
│         │                          │                          │              │
│         └──────────────────────────┼──────────────────────────┘              │
│                                    │                                         │
│                                    ▼                                         │
│                           ┌──────────────┐                                   │
│                           │   Wallet     │                                   │
│                           │   Service    │                                   │
│                           ├──────────────┤                                   │
│                           │ • Multi-chain│                                   │
│                           │ • Custody    │                                   │
│                           │ • Addresses  │                                   │
│                           └──────────────┘                                   │
└─────────────────────────────────────────────────────────────────────────────┘
```


### 4.2 Service Responsibilities

| Service | Responsibility | External Dependencies |
|---------|---------------|----------------------|
| **Onboarding** | User registration, KYC, wallet provisioning | KYC Provider, Circle |
| **Funding** | Deposit processing, 70/30 split execution | Circle, Blockchain RPCs |
| **Spending** | Card transactions, round-ups, balance management | Card Issuer |
| **Investing** | Auto-allocation, trade execution, portfolio | Alpaca |
| **Wallet** | Multi-chain wallet management, custody | Circle |

---

## 5. Data Flow Architecture

### 5.1 Core User Flow: Deposit → Split → Deploy

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    DEPOSIT → SPLIT → DEPLOY FLOW                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  USER                                                                        │
│    │                                                                         │
│    │ 1. Load Money                                                           │
│    ▼                                                                         │
│  ┌──────────────┐                                                            │
│  │   Funding    │  2. Receive deposit notification                           │
│  │   Service    │◀─────────────────────────────────────┐                     │
│  └──────┬───────┘                                      │                     │
│         │                                              │                     │
│         │ 3. Validate & Confirm                  ┌─────┴─────┐               │
│         │                                        │  Circle   │               │
│         ▼                                        │  Webhook  │               │
│  ┌──────────────┐                                └───────────┘               │
│  │    Split     │                                                            │
│  │   Engine     │  4. Apply 70/30 Rule                                       │
│  └──────┬───────┘                                                            │
│         │                                                                    │
│    ┌────┴────┐                                                               │
│    │         │                                                               │
│    ▼         ▼                                                               │
│  ┌────┐   ┌────┐                                                             │
│  │70% │   │30% │                                                             │
│  └──┬─┘   └──┬─┘                                                             │
│     │        │                                                               │
│     ▼        ▼                                                               │
│  ┌──────────────┐    ┌──────────────┐                                        │
│  │    Spend     │    │   Invest     │                                        │
│  │   Balance    │    │   Engine     │                                        │
│  └──────────────┘    └──────┬───────┘                                        │
│                             │                                                │
│                             │ 5. Auto-allocate                               │
│                             ▼                                                │
│                      ┌──────────────┐                                        │
│                      │   Strategy   │                                        │
│                      │   Selector   │                                        │
│                      └──────┬───────┘                                        │
│                             │                                                │
│                             │ 6. Execute trades                              │
│                             ▼                                                │
│                      ┌──────────────┐                                        │
│                      │    Alpaca    │                                        │
│                      │  (Brokerage) │                                        │
│                      └──────────────┘                                        │
│                                                                              │
│  ⏱️ Target: < 60 seconds end-to-end                                          │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 5.2 Card Transaction Flow with Round-ups

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


---

## 6. Infrastructure Architecture

### 6.1 Deployment Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        DEPLOYMENT ARCHITECTURE                               │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│                              ┌─────────────┐                                 │
│                              │   AWS ALB   │                                 │
│                              │  (Gateway)  │                                 │
│                              └──────┬──────┘                                 │
│                                     │                                        │
│                                     ▼                                        │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │                         ECS / EKS CLUSTER                             │   │
│  │  ┌────────────────────────────────────────────────────────────────┐  │   │
│  │  │                      APPLICATION TIER                           │  │   │
│  │  │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐        │  │   │
│  │  │  │   API    │  │   API    │  │   API    │  │  Worker  │        │  │   │
│  │  │  │ Pod (1)  │  │ Pod (2)  │  │ Pod (n)  │  │   Pods   │        │  │   │
│  │  │  └──────────┘  └──────────┘  └──────────┘  └──────────┘        │  │   │
│  │  └────────────────────────────────────────────────────────────────┘  │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                     │                                        │
│         ┌───────────────────────────┼───────────────────────────┐            │
│         │                           │                           │            │
│         ▼                           ▼                           ▼            │
│  ┌──────────────┐           ┌──────────────┐           ┌──────────────┐     │
│  │  PostgreSQL  │           │    Redis     │           │     S3       │     │
│  │   (RDS)      │           │ (ElastiCache)│           │  (Storage)   │     │
│  │              │           │              │           │              │     │
│  │  • Ledger    │           │  • Sessions  │           │  • Logs      │     │
│  │  • Users     │           │  • Cache     │           │  • Backups   │     │
│  │  • Txns      │           │  • Rate Lim  │           │              │     │
│  └──────────────┘           └──────────────┘           └──────────────┘     │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 6.2 External Integrations

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
│  │     CIRCLE       │  │     ALPACA       │  │   CARD ISSUER    │           │
│  │  (Wallet Infra)  │  │   (Brokerage)    │  │   (Payments)     │           │
│  ├──────────────────┤  ├──────────────────┤  ├──────────────────┤           │
│  │ • Wallet Create  │  │ • Trade Execute  │  │ • Virtual Cards  │           │
│  │ • Deposit Addr   │  │ • Market Data    │  │ • Physical Cards │           │
│  │ • USDC Custody   │  │ • Positions      │  │ • Authorizations │           │
│  │ • Webhooks       │  │ • Account Mgmt   │  │ • Settlements    │           │
│  └──────────────────┘  └──────────────────┘  └──────────────────┘           │
│                                                                              │
│    ┌─────────────────────────────────────────────────────────────┐          │
│    │                    BLOCKCHAIN NETWORKS                       │          │
│    │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐     │          │
│    │  │ Ethereum │  │ Polygon  │  │   BSC    │  │  Solana  │     │          │
│    │  └──────────┘  └──────────┘  └──────────┘  └──────────┘     │          │
│    └─────────────────────────────────────────────────────────────┘          │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 7. Database Schema (Core Entities)

### 7.1 Entity Relationship Diagram

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


---

## 8. Security Architecture

### 8.1 Security Layers

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
│  │  • JWT Tokens (Access + Refresh)                                     │    │
│  │  • Passcode Authentication                                           │    │
│  │  • Session Management via Redis                                      │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                    │                                         │
│                                    ▼                                         │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                      AUTHORIZATION LAYER                             │    │
│  │  • Resource-based access control                                     │    │
│  │  • User-scoped data isolation                                        │    │
│  │  • KYC status gating for features                                    │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                    │                                         │
│                                    ▼                                         │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                        DATA SECURITY                                 │    │
│  │  • AES-256-GCM encryption at rest                                    │    │
│  │  • TLS 1.3 in transit                                                │    │
│  │  • Encrypted wallet keys                                             │    │
│  │  • PII masking in logs                                               │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 9. API Architecture

### 9.1 API Endpoint Structure

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           API ENDPOINT MAP                                   │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  /api/v1                                                                     │
│  │                                                                           │
│  ├── /auth                          [PUBLIC]                                 │
│  │   ├── POST /register             Create account                           │
│  │   ├── POST /login                Authenticate                             │
│  │   ├── POST /refresh              Refresh token                            │
│  │   └── POST /logout               End session                              │
│  │                                                                           │
│  ├── /onboarding                    [AUTHENTICATED]                          │
│  │   ├── POST /start                Begin onboarding                         │
│  │   ├── GET  /status               Check progress                           │
│  │   └── POST /kyc/submit           Submit KYC docs                          │
│  │                                                                           │
│  ├── /account                       [AUTHENTICATED + KYC]                    │
│  │   ├── GET  /                     Get account summary                      │
│  │   ├── GET  /balances             Get spend/invest balances                │
│  │   └── GET  /station              Home screen data                         │
│  │                                                                           │
│  ├── /funding                       [AUTHENTICATED + KYC]                    │
│  │   ├── POST /deposit/address      Generate deposit address                 │
│  │   ├── GET  /deposits             List deposits                            │
│  │   └── POST /webhooks/chain       Blockchain webhooks                      │
│  │                                                                           │
│  ├── /spending                      [AUTHENTICATED + KYC]                    │
│  │   ├── GET  /transactions         Transaction history                      │
│  │   ├── POST /roundups/toggle      Enable/disable round-ups                 │
│  │   └── GET  /roundups/status      Round-up settings                        │
│  │                                                                           │
│  ├── /cards                         [AUTHENTICATED + KYC]                    │
│  │   ├── GET  /                     List cards                               │
│  │   ├── POST /virtual              Create virtual card                      │
│  │   └── POST /{id}/freeze          Freeze card                              │
│  │                                                                           │
│  └── /investing                     [AUTHENTICATED + KYC]                    │
│      ├── GET  /balance              Invest balance                           │
│      ├── GET  /status               Allocation status                        │
│      └── GET  /portfolio            Portfolio summary (minimal)              │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 10. Component Interaction Diagram

### 10.1 Onboarding Flow

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

### 10.2 Investment Engine Flow

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      INVESTMENT ENGINE SEQUENCE                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Split Engine      Invest Service     Strategy Engine      Alpaca            │
│       │                  │                  │                 │              │
│       │  1. 30% Allocated│                  │                 │              │
│       │─────────────────▶│                  │                 │              │
│       │                  │                  │                 │              │
│       │                  │  2. Get Strategy │                 │              │
│       │                  │─────────────────▶│                 │              │
│       │                  │                  │                 │              │
│       │                  │  3. Allocation   │                 │              │
│       │                  │◀─────────────────│                 │              │
│       │                  │  [ETF: 60%,      │                 │              │
│       │                  │   Tech: 25%,    │                 │              │
│       │                  │   Stable: 15%]  │                 │              │
│       │                  │                  │                 │              │
│       │                  │  4. Execute Trades                 │              │
│       │                  │────────────────────────────────────▶              │
│       │                  │                  │                 │              │
│       │                  │  5. Confirmations                  │              │
│       │                  │◀────────────────────────────────────              │
│       │                  │                  │                 │              │
│       │  6. Update Portfolio               │                 │              │
│       │◀─────────────────│                  │                 │              │
│                                                                              │
│  ⏱️ Allocation happens automatically, no user interaction                    │
└─────────────────────────────────────────────────────────────────────────────┘
```


---

## 11. Background Worker Architecture

### 11.1 Worker Components

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

## 12. System State Machine

### 12.1 Account States

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

### 12.2 System Status (Station Display)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      SYSTEM STATUS STATES                                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌─────────────┐     ┌─────────────┐     ┌─────────────┐                    │
│  │ ALLOCATING  │────▶│   ACTIVE    │────▶│   PAUSED    │                    │
│  │             │     │             │     │             │                    │
│  │ Money being │     │ System      │     │ User or     │                    │
│  │ deployed    │     │ operating   │     │ system      │                    │
│  │             │     │ normally    │     │ initiated   │                    │
│  └─────────────┘     └─────────────┘     └─────────────┘                    │
│        ▲                   │                   │                             │
│        │                   │                   │                             │
│        └───────────────────┴───────────────────┘                             │
│                                                                              │
│  Display Rules:                                                              │
│  • ALLOCATING: Shown during deposit processing (< 60s)                       │
│  • ACTIVE: Default state, money is working                                   │
│  • PAUSED: Rare, only for compliance or user request                         │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 13. Monitoring & Observability

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                     OBSERVABILITY STACK                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                          METRICS                                     │    │
│  │  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐           │    │
│  │  │  Prometheus  │───▶│   Grafana    │───▶│   Alerts     │           │    │
│  │  └──────────────┘    └──────────────┘    └──────────────┘           │    │
│  │                                                                      │    │
│  │  Key Metrics:                                                        │    │
│  │  • Deposit → Split latency (target: < 60s)                           │    │
│  │  • Trade execution success rate                                      │    │
│  │  • API response times (p50, p95, p99)                                │    │
│  │  • Active users (DAU/MAU)                                            │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                          LOGGING                                     │    │
│  │  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐           │    │
│  │  │  Zap Logger  │───▶│  CloudWatch  │───▶│   Insights   │           │    │
│  │  └──────────────┘    └──────────────┘    └──────────────┘           │    │
│  │                                                                      │    │
│  │  Log Levels: DEBUG → INFO → WARN → ERROR                             │    │
│  │  Structured JSON format with correlation IDs                         │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                          TRACING                                     │    │
│  │  ┌──────────────┐    ┌──────────────┐                               │    │
│  │  │ OpenTelemetry│───▶│   X-Ray      │                               │    │
│  │  └──────────────┘    └──────────────┘                               │    │
│  │                                                                      │    │
│  │  Distributed tracing across services and external calls              │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 14. Appendix

### A. Technology Stack Summary

| Layer | Technology | Purpose |
|-------|------------|---------|
| Language | Go 1.24 | Backend services |
| Framework | Gin | HTTP routing |
| Database | PostgreSQL 15 | Primary data store |
| Cache | Redis 7 | Sessions, rate limiting |
| Auth | JWT | Token-based auth |
| Encryption | AES-256-GCM | Data at rest |
| Wallet | Circle | Custody, stablecoins |
| Brokerage | Alpaca | Trade execution |
| Monitoring | Prometheus/Grafana | Metrics |
| Logging | Zap | Structured logs |

### B. Non-Functional Requirements

| Requirement | Target | Measurement |
|-------------|--------|-------------|
| Deposit → Split | < 60 seconds | P95 latency |
| API Response | < 200ms | P95 latency |
| Uptime | 99.9% | Monthly |
| Ledger Accuracy | 99.99% | Audit |
| iOS Launch | < 2 seconds | Cold start |

### C. Compliance Constraints

- No investment advice language
- No return promises
- KYC/AML required before funding
- Clear disclosure of 70/30 split before first deposit
- Audit trail for all financial transactions

---

## Document History

| Version | Date | Author | Changes |
|---------|------|--------|---------|
| 1.0 | 2025-12-14 | System | Initial creation |
