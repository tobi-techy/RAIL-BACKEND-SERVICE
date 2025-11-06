# Core Workflows

**Version:** v0.2  
**Last Updated:** October 24, 2025

## Navigation
- **Previous:** [Components](./03-components.md)
- **Next:** [Database Schema](./05-database-schema.md)
- **[Index](./README.md)**

---

## 7. Core Workflows

These sequence diagrams illustrate the key interactions between components and external partners for critical user journeys in the Go-based architecture.

### 7.1 Onboarding + Wallet Creation + Passcode Setup

```mermaid
sequenceDiagram
    participant App as React Native App
    participant GW as API Gateway (GraphQL)
    participant ONB as Onboarding Module (Go)
    participant WAL as Wallet Module (Go)
    participant IDP as Auth0/Cognito
    participant KYC as KYC Provider
    participant CIR as Circle API
    participant PG as PostgreSQL

    App->>IDP: User Initiates Sign Up/Login
    IDP-->>App: OIDC Flow / Receives ID Token
    App->>GW: SignUp/Login Mutation (with ID Token)
    GW->>ONB: Verify Token / Create User Request
    ONB->>IDP: Validate Token (Optional, depending on flow)
    ONB->>PG: Create/Fetch User Record
    ONB->>KYC: Initiate KYC Check
    ONB->>WAL: Trigger Wallet Creation (async)
    WAL->>CIR: Create Developer-Controlled Wallet
    CIR-->>WAL: Wallet ID/Details
    WAL->>PG: Store Wallet Info (address, circle_id)
    KYC-->>ONB: KYC Status Webhook/Callback (pass/fail)
    ONB->>PG: Update User KYC Status
    ONB-->>GW: User Profile & Status
    GW-->>App: Return User Profile

    %% Passcode Setup (Separate Flow/Mutation) %%
    App->>GW: Set Passcode Mutation (passcode)
    GW->>ONB: Hash and Store Passcode Request
    ONB->>PG: Store passcode_hash for user
    ONB-->>GW: Success Confirmation
    GW-->>App: Passcode Set
```

### 7.2 Funding Flow (USDC Deposit -> Circle Off-Ramp -> Alpaca Funding)

```mermaid
sequenceDiagram
    participant User
    participant App as React Native App
    participant GW as API Gateway (GraphQL)
    participant WAL as Wallet Module (Go)
    participant FND as Funding Module (Go)
    participant DUE as Due API
    participant DW as Alpaca API
    participant PG as PostgreSQL
    participant Q as SQS Queue

    %% Step 1: Get Deposit Address %%
    App->>GW: Query Deposit Address (chain=ETH/SOL)
    GW->>WAL: GetDepositAddress(userId, chain)
    WAL->>PG: Fetch Wallet Info
    WAL-->>GW: Return Address
    GW-->>App: Display Address

    %% Step 2: User Deposits USDC (On-Chain) %%
    User->>Blockchain: Sends USDC to Address

    %% Step 3: Monitor Deposit & Create Virtual Account %%
    Blockchain->>FND: Deposit Detected (webhook/monitor)
    FND->>PG: Log Deposit (status: confirmed_on_chain)
    FND->>DUE: Create Virtual Account (linked to Alpaca)
    DUE-->>FND: Virtual Account Created
    FND->>Q: Enqueue Off-Ramp Task (depositId)

    %% Step 4: Async Off-Ramp Processing %%
    Q->>FND: Process Off-Ramp Task (depositId)
    FND->>PG: Update Deposit Status (status: off_ramp_initiated)
    FND->>DUE: Initiate Off-Ramp (USDC -> USD to Virtual Account)
    DUE-->>FND: Off-Ramp Accepted/Pending

    %% Step 5: Due Confirms Off-Ramp %%
    DUE->>FND: Off-Ramp Completed Webhook
    FND->>PG: Update Deposit Status (status: off_ramp_complete)
    FND->>Q: Enqueue Broker Funding Task (depositId, usdAmount)

    %% Step 6: Async Broker Funding %%
    Q->>FND: Process Broker Funding Task
    FND->>DW: Initiate Account Funding (USD from Virtual Account)
    DW-->>FND: Funding Accepted/Pending

    %% Step 7: Alpaca Confirms Funding %%
    DW->>FND: Funding Completed Webhook/Callback
    FND->>PG: Update Deposit Status (status: broker_funded)
    FND->>PG: Update User Balance (buying_power_usd)
    FND-->>GW: Notify User (e.g., via WebSocket/Push)
    GW-->>App: Balance Updated Notification
```

### 7.3 Investment Flow (Place Order for Stock/Option via Alpaca)

```mermaid
sequenceDiagram
    participant App as React Native App
    participant GW as API Gateway (GraphQL)
    participant INV as Investing Module (Go)
    participant FND as Funding Module (Go)
    participant DW as Alpaca API
    participant PG as PostgreSQL

    App->>GW: Place Order Mutation (basketId/optionDetails, amountUSD, side)
    GW->>INV: PlaceOrder Request
    INV->>FND: Check Balance (buying_power_usd)
    FND->>PG: Read balance record
    FND-->>INV: Balance Check Result (OK/Insufficient)

    alt Sufficient Balance
        INV->>PG: Create Order Record (status: pending)
        INV->>DW: Submit Order (Stock/ETF/Option)
        DW-->>INV: Order Accepted (brokerRef)
        INV->>PG: Update Order Record (status: accepted_by_broker, brokerRef)
        INV-->>GW: Order Accepted Confirmation
        GW-->>App: Order Accepted
    else Insufficient Balance
        INV-->>GW: Error: Insufficient Funds
        GW-->>App: Show Error Message
    end

    %% Later - Alpaca sends fill confirmation %%
    DW->>INV: Order Fill Webhook (partial/full fill details)
    INV->>PG: Update Order Record (status: partially_filled / filled)
    INV->>PG: Update/Cache Position Data (or trigger portfolio refresh)
    INV-->>GW: Notify User (e.g., via WebSocket/Push)
    GW-->>App: Order Fill Notification
```

### 7.4 Withdrawal Flow (Alpaca USD -> Circle On-Ramp -> USDC Transfer)

```mermaid
sequenceDiagram
    participant App as React Native App
    participant GW as API Gateway (GraphQL)
    participant FND as Funding Module (Go)
    participant DW as Alpaca API
    participant DUE as Due API
    participant WAL as Wallet Module (Go)
    participant PG as PostgreSQL
    participant Q as SQS Queue

    App->>GW: Initiate Withdrawal Mutation (amountUSD, targetChain, targetAddress)
    GW->>FND: InitiateWithdrawal Request
    FND->>PG: Check Balance (buying_power_usd)
    alt Sufficient Balance
        FND->>PG: Log Withdrawal Request (status: pending)
        FND->>Q: Enqueue Broker Withdrawal Task
        FND-->>GW: Withdrawal Initiated Confirmation
        GW-->>App: Withdrawal Initiated
    else Insufficient Balance
        FND-->>GW: Error: Insufficient Funds
        GW-->>App: Show Error Message
    end

    %% Async Broker Withdrawal %%
    Q->>FND: Process Broker Withdrawal Task
    FND->>DW: Request USD Withdrawal to Virtual Account
    DW-->>FND: Withdrawal Accepted/Pending

    %% Alpaca Confirms USD Sent %%
    DW->>FND: Withdrawal Completed Webhook/Callback (e.g., ACH settled)
    FND->>PG: Update Withdrawal Status (status: broker_withdrawal_complete)
    FND->>Q: Enqueue Due On-Ramp Task

    %% Async Due On-Ramp %%
    Q->>FND: Process Due On-Ramp Task
    FND->>DUE: Initiate On-Ramp (USD from Virtual Account -> USDC)
    DUE-->>FND: On-Ramp Accepted/Pending

    %% Due Confirms USDC Ready %%
    DUE->>FND: On-Ramp Completed Webhook
    FND->>PG: Update Withdrawal Status (status: on_ramp_complete)
    FND->>Q: Enqueue On-Chain Transfer Task

    %% Async On-Chain Transfer %%
    Q->>FND: Process On-Chain Transfer Task
    FND->>WAL: Get User Wallet Details (Circle Wallet ID)
    FND->>DUE: Initiate Transfer (Send USDC from Virtual Account to targetAddress)
    DUE-->>FND: Transfer Accepted/Pending (txHash)
    FND->>PG: Update Withdrawal Status (status: transfer_initiated, txHash)

    %% Due Confirms Transfer %%
    DUE->>FND: Transfer Completed Webhook
    FND->>PG: Update Withdrawal Status (status: complete)
    FND-->>GW: Notify User
    GW-->>App: Withdrawal Complete Notification
```

---

**Next:** [Database Schema](./05-database-schema.md)
