# Functional Requirements

**Version:** v0.2  
**Last Updated:** October 24, 2025

## Navigation
- **Previous:** [User Personas](./01-user-personas.md)
- **Next:** [Success Metrics](./03-success-metrics.md)
- **[Index](./README.md)**

---

## Functional Requirements

### Core MVP Features

#### 1. User Onboarding & Managed Wallet
* Simple sign-up with email/phone.
* **NEW:** Support for **passcode-based** app access.
* Automatic creation of a secure, managed wallet using **Circle Developer-Controlled Wallets**.
* No seed phrase complexity; custody abstracted away.

#### 2. Stablecoin Funding Flow (Deposit & Off-Ramp)
* Support deposits of USDC from at least one EVM chain (e.g., Ethereum) and one non-EVM chain (e.g., Solana) into the user's Circle wallet.
* **NEW:** Create and manage virtual accounts linked to user Alpaca brokerage accounts.
* **NEW:** Orchestrate an immediate **USDC-to-USD off-ramp** via **Due API**.
* **NEW:** Transfer off-ramped USD directly into the user's linked Alpaca brokerage account to create "buying power" for instant trading.
* **NEW:** Integrate KYC verification via **Sumsub** for regulatory compliance.
* **NEW:** Implement recipient management for managing withdrawal destinations.

#### 3. Investment Flow (Stocks, ETFs, & Options)
* Ability to invest in curated baskets of stocks/ETFs.
* **NEW:** Ability to trade **options**.
* Simple portfolio view with performance tracking (pulling data from Alpaca).

#### 4. Curated Investment Baskets
* Launch with 5–10 "expert-curated" investment baskets (e.g., Tech Growth, Sustainability, ETFs).
* Designed to simplify decision-making for new investors.

#### 5. AI CFO (MVP Version)
* Provides automated weekly performance summaries.
* On-demand portfolio analysis to highlight diversification, risk, and potential mistakes.
* (Implementation: Built in Go, pulls data from Alpaca via Investing Service).

#### 6. Brokerage & Withdrawal Flow
* Secure backend integration with **Alpaca** for trade execution and custody of traditional assets.
* **NEW:** Support for withdrawals, orchestrating a **USD-to-USDC on-ramp** from Alpaca via **Due API** to the user's selected chain.

---

### Out of Scope for MVP
- Advanced AI CFO with conversational nudges.
- Full social/gamified features (profiles, following, leaderboards, copy investing).
- User-curated baskets, debit card, P2P payments, time-lock investments.

---

### Post-MVP Roadmap
- **Phase 2:** Full AI CFO, advanced social suite, user-curated baskets.
- **1–2 Years:** Expansion into debit card, P2P payments, business accounts, startup launchpad.

---

**Next:** [Success Metrics](./03-success-metrics.md)
