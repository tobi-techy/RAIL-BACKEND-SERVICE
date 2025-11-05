# Components

**Version:** v0.2  
**Last Updated:** October 24, 2025

## Navigation
- **Previous:** [Data Models](./02-data-models.md)
- **Next:** [Workflows](./04-workflows.md)
- **[Index](./README.md)**

---

## 5. Components

The STACK backend will start as a **Go-based modular monolith**. Functionality is divided into distinct Go modules, each responsible for a specific domain. These modules communicate internally via Go function calls and interfaces initially.

### 5.1 Component List

* **Onboarding Service Module**

    * **Responsibility:** Handles user sign-up, profile management, KYC/AML orchestration, passcode setup/verification, and feature flag checks.
    * **Key Interfaces:**
        * Internal: `CreateUser`, `GetUserProfile`, `UpdateKYCStatus`, `SetPasscode`, `VerifyPasscode`.
        * External (via GraphQL Gateway): Mutations for sign-up, profile updates; Queries for user status.
    * **Dependencies:** `Auth Provider (IDP)`, `KYC/AML Provider`, `Wallet Service Module` (to trigger wallet creation), `Database (users table)`.
    * **Technology Stack:** Go module.

* **Wallet Service Module**

    * **Responsibility:** Manages the lifecycle of Circle Developer-Controlled Wallets, including creation and association with users. Provides addresses for deposits.
    * **Key Interfaces:**
        * Internal: `CreateUserWallet`, `GetDepositAddress`.
        * External (via GraphQL Gateway): Query for deposit addresses.
    * **Dependencies:** `Circle API`, `Database (users, wallets tables)`.
    * **Technology Stack:** Go module.

* **Funding Service Module**

    * **Responsibility:** Orchestrates the entire funding and withdrawal flow: monitors blockchain deposits, manages virtual accounts, triggers Due off-ramps (USDC->USD), confirms Alpaca funding, handles withdrawal requests (Alpaca->USD->USDC via Due), and manages related state transitions.
    * **Key Interfaces:**
        * Internal: `HandleChainDepositEvent`, `CreateVirtualAccount`, `InitiateOffRamp`, `ConfirmBrokerFunding`, `InitiateWithdrawal`, `HandleWithdrawalCompletion`. Listens for events from blockchain monitors/webhooks.
        * External (via GraphQL Gateway): Query for deposit history/status, Mutation to initiate withdrawal.
    * **Dependencies:** `Blockchain Monitors/Webhooks`, `Due API`, `Alpaca API`, `Database (deposits, balances, virtual_accounts, users, wallets tables)`, `Queueing (SQS)` (for asynchronous steps).
    * **Technology Stack:** Go module, SQS.

* **Investing Service Module**

    * **Responsibility:** Manages the investment lifecycle: provides basket catalog, places orders (baskets, options) with Alpaca, retrieves portfolio positions/performance from Alpaca, calculates basic P&L.
    * **Key Interfaces:**
        * Internal: `GetBaskets`, `PlaceOrder`, `GetPortfolio`.
        * External (via GraphQL Gateway): Queries for baskets, portfolio; Mutations for placing orders.
    * **Dependencies:** `Alpaca API`, `Database (baskets, orders, positions cache, users tables)`.
    * **Technology Stack:** Go module.

* **AI CFO Service Module**

    * **Responsibility:** Generates weekly performance summaries and on-demand portfolio analysis using 0G for AI/storage. Pulls data via the Investing Service Module.
    * **Key Interfaces:**
        * Internal: `GenerateWeeklySummary`, `AnalyzePortfolioOnDemand`. Triggered by scheduler or internal events.
        * External (via GraphQL Gateway): Queries for latest summary, Mutation to trigger on-demand analysis.
    * **Dependencies:** `Investing Service Module` (for data), `0G API`, `Database (ai_summaries, users tables)`, `Scheduler` (e.g., AWS EventBridge).
    * **Technology Stack:** Go module.

### 5.2 Component Diagrams

```mermaid
graph TD
    subgraph "STACK Backend (Go Modular Monolith)"
        GW[API Gateway (GraphQL)] --> ONB[Onboarding Module]
        GW --> WAL[Wallet Module]
        GW --> FND[Funding Module]
        GW --> INV[Investing Module]
        GW --> AIC[AI CFO Module]

        ONB --> WAL   # Triggers wallet creation
        FND <--> WAL  # Needs wallet info
        AIC --> INV   # Needs portfolio data
        FND <--> INV  # Needs balance updates, order info potentially
    end

    subgraph "Datastores"
        PG[(PostgreSQL)]
        Q[(SQS Queue)]
    end

    ONB --> PG
    WAL --> PG
    FND --> PG
    INV --> PG
    AIC --> PG
    FND --> Q # For async funding steps

     subgraph "External Partners"
        IDP[Auth0/Cognito]
        KYC[Sumsub (KYC)]
        CIR[Circle API (Wallets)]
        DUE[Due API (Funding)]
        DW[Alpaca API (Brokerage)]
        OG[0G API]
    end

    ONB --> IDP
    ONB --> KYC
    WAL --> CIR
    FND --> DUE # For off-ramp/on-ramp
    FND --> DW # For funding broker
    INV --> DW # For trading/portfolio
    AIC --> OG

    style PG fill:#lightblue
    style Q fill:#lightblue
    style IDP fill:#lightgrey
    style KYC fill:#lightgrey
    style CIR fill:#lightgrey
    style DW fill:#lightgrey
    style OG fill:#lightgrey
```

---

**Next:** [Workflows](./04-workflows.md)
