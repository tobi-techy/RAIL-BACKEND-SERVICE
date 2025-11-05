# STACK Architecture Overview

**Version:** v0.2 - Go/Alpaca  
**Last Updated:** October 24, 2025

## Navigation
- **Current:** Overview
- **Next:** [Tech Stack](./01-tech-stack.md)
- **[Index](./README.md)**

---

## 1. Introduction

This document outlines the overall project architecture for STACK, refactored for a **Go** backend. It details the integration of **Circle Developer-Controlled Wallets** for wallet management/funding and **Alpaca** as the brokerage partner. Its primary goal is to serve as the guiding architectural blueprint for AI-driven development.

**Relationship to Frontend Architecture:**
This document covers the backend services. A separate Frontend Architecture Document (for React Native) MUST be used in conjunction with this one.

* **Starter Template or Existing Project:** N/A - Greenfield project. We are defining the structure from scratch.

* **Change Log:**
  | Date | Version | Description | Author |
  | :--- | :--- | :--- | :--- |
  | Sept 27, 2025 | v0.1 | Initial NestJS architecture. | Winston |
  | Oct 24, 2025 | v0.2 | Complete rewrite for Go, Alpaca, and Circle pivot. | Winston |

---

## 2. High Level Architecture

### 2.1 Technical Summary

This architecture is a **Go-based modular monolith** deployed on AWS Fargate (ECS). The system exposes a unified GraphQL API (via an API Gateway) to the React Native mobile app. It integrates five critical external partners: **Auth0/Cognito** (Identity), **Circle** (Wallets only), **Due** (USDC On/Off-Ramps, Virtual Accounts, Recipient Management), **Sumsub** (KYC/AML), and **Alpaca** (Brokerage, Custody). Data is persisted in **PostgreSQL**. This design supports the MVP goal of providing a hybrid Web3-to-TradFi investment flow.

### 2.2 High Level Overview

* **Architectural Style:** Modular Monolith (to start).
* **Repository Structure:** Monorepo (recommended).
* **Service Architecture:** A single Go application separated into logical domain modules (Onboarding, Wallet, Funding, Investing, AI-CFO) that communicate internally.
* **Data Flow (Funding):** User deposits USDC (on-chain) -> Monitored by Funding Service -> Due Off-Ramp (USDC to USD) -> Virtual Account -> Alpaca Deposit (USD) -> "Buying Power" updated.
* **Data Flow (Withdrawal):** User requests USD withdrawal -> Alpaca debits account -> Virtual Account -> Due On-Ramp (USD to USDC) -> Funding Service sends USDC (on-chain) to user.

### 2.3 High Level Project Diagram

```mermaid
graph TD
    U[Gen Z User (Mobile)] --> RN[React Native App]

    subgraph "STACK Backend (Go Modular Monolith on AWS Fargate)"
        RN --> GW[API Gateway (GraphQL)]
        GW --> ONB[Onboarding Service]
        GW --> WAL[Wallet Service]
        GW --> FND[Funding Service]
        GW --> INV[Investing Service]
        GW --> AIC[AI CFO Service]

        ONB --> PG[(PostgreSQL)]
        WAL --> PG
        FND --> PG
        INV --> PG
        AIC --> PG
    end

    subgraph "External Partners"
        ONB --> IDP[Auth0 / Cognito]
        ONB --> KYC[Sumsub (KYC/AML)]
        WAL --> CIR[Circle API (Developer Wallets)]
        FND --> DUE[Due API (Off-Ramp/On-Ramp)]
        FND --> DW[Alpaca API (Brokerage)]
        INV --> DW
        AIC --> OG[0G (AI/Storage)]
    end
```

### 2.4 Architectural and Design Patterns

* **Modular Monolith:** A single Go application with strongly defined internal boundaries (Go modules) for each domain (Onboarding, Funding, etc.). This allows for faster MVP development while making future extraction into microservices straightforward.
* **Repository Pattern:** Used to abstract all database interactions. Go interfaces will define data access methods, with concrete implementations for PostgreSQL, ensuring testability and separation of concerns.
* **Adapter Pattern:** Used extensively for all external partner integrations (Circle, Alpaca, 0G). Go interfaces will define the *required* functionality (e.g., `BrokerageAdapter`), and concrete structs will implement those interfaces by calling the specific partner APIs.
* **Asynchronous Orchestration (Sagas):** The complex funding (USDC -> USD -> Broker) and withdrawal (Broker -> USD -> USDC) flows will be managed using an asynchronous, event-driven approach (even within the monolith) to handle multi-step processes and failures.

---

**Next:** [Tech Stack](./01-tech-stack.md)
