# STACK MVP Epics

Date: 2025-11-03
Author: Tobi
Version: v0.2 (Pivot to Go + Alpaca)

---

## Epic 1: Onboarding & Wallet Management
**Summary:** Deliver a smooth sign-up and wallet creation process that hides Web3 complexity.

**In-Scope Features:**
- Simple, mobile-first sign-up flow.
- **NEW:** **Passcode support** for app login.
- Managed wallet creation using **Circle Developer-Controlled Wallets** (no seed phrases).
- Security + custody abstraction.

**Success Criteria:**
- 90%+ of new users complete onboarding successfully.
- Wallet creation works 99%+ of the time.

**Stories:**
- 1-1-user-registration-flow
- 1-2-passcode-authentication
- 1-3-wallet-creation-and-management
- 1-4-onboarding-completion-and-error-handling

---

## Epic 2: Stablecoin Funding Flow
**Summary:** Enable users to fund their brokerage accounts instantly with stablecoins using Due for off-ramp/on-ramp functionality.

**In-Scope Features:**
- Support deposits from Ethereum (EVM) and Solana (non-EVM) into the user's Circle wallet.
- **NEW:** Create virtual accounts linked to Alpaca brokerage accounts for each user.
- **NEW:** Orchestrate an immediate **USDC-to-USD off-ramp** via **Due API**.
- **NEW:** Securely transfer the resulting USD into the user's linked Alpaca brokerage account.
- **NEW:** Handle the reverse flow: **USD (Alpaca) -> USDC (Due)** for withdrawals.
- **NEW:** Integrate KYC verification via **Sumsub** for compliance.
- **NEW:** Implement recipient management for withdrawal destinations.

**Success Criteria:**
- Users can fund their brokerage account within minutes of a confirmed stablecoin deposit.
- At least 2 supported deposit pathways at launch.
- Virtual accounts successfully linked to Alpaca accounts.
- KYC verification completed via Sumsub integration.
- End-to-end funding/withdrawal success rate of >99%.

**Stories:**
- 2-1-virtual-account-creation
- 2-2-due-off-ramp-integration
- 2-3-alpaca-account-funding
- 2-4-due-withdrawal-integration
- 2-5-sumsub-kyc-integration
- 2-6-recipient-management

---

## Epic 3: Curated Baskets
**Summary:** Provide beginner-friendly, expert-curated investment options.

**In-Scope Features:**
- 5–10 prebuilt baskets (Tech Growth, Sustainability, ETFs, etc.) from real data from the alpaca api.
- Balanced for simplicity + diversity.

**Success Criteria:**
- 80%+ of first investments made via curated baskets.
- Positive user feedback on basket clarity (≥7/10 rating).

---

## Epic 4: Investment Flow (Stocks & Options & crypto)
**Summary:** Allow users to invest seamlessly in traditional assets with clear portfolio visibility.

**In-Scope Features:**
- Ability to invest in curated baskets (from Epic 3).
- **NEW:** Ability to trade **options**.
- Simple portfolio view (holdings + performance) by pulling data from Alpaca.

**Success Criteria:**
- ≥100 funded accounts make at least one investment.
- Portfolio updates within seconds of trade execution from Alpaca.

---

## Epic 5: AI CFO (MVP Version)
**Summary:** Give users a lightweight AI financial guide for trust and protection.

**In-Scope Features:**
- Automated weekly performance summaries.
- On-demand portfolio analysis (basic insights).
- (Implementation: Built in **Go**, pulls data from Alpaca).

**Success Criteria:**
- ≥70% of active users read at least one AI CFO summary.
- >60% of users report increased confidence in investing.

---

## Epic 6: Brokerage Integration
**Summary:** Connect with **Alpaca** for trade execution and asset custody.

**In-Scope Features:**
- Secure backend integration (in **Go**) with **Alpaca APIs** for trade execution.
- Custody of stocks/ETFs/options via partner integration.

**Success Criteria:**
- ≥99% trade execution success rate with Alpaca.
- Integration passes security and compliance checks.
