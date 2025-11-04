# Product Requirements Document (PRD)

**Product Name:** STACK
**Prepared By:** Product Management (John)
**Date:** October 24, 2025
**Version:** v0.2 (Pivot to Go + Alpaca)

**Summary:**
STACK is a Web3-native investment app designed for Gen Z users who are underserved by traditional banks and overwhelmed by complex crypto tools. It enables instant wealth-building through a hybrid model: fiat-to-stablecoin on-ramps, seamless investment in stocks/ETFs/options, and a protective AI CFO. The MVP focuses on onboarding, wallet management, stablecoin deposits (with off-ramping to a broker), curated baskets, and a simplified investing flow. The long-term vision is to expand into a social, gamified financial hub.

---

## Goals & Background

### Business Goals
- Drive rapid adoption with 10,000 Monthly Active Users (MAU) within the first 6 months of launch.
- Establish a recurring revenue stream by converting at least 5% of free users into premium subscribers in year one.
- Validate market viability by processing $1,000,000 in investments within the first year.
- Position STACK as the first mover in the Gen Z-native hybrid Web3 + traditional finance investment space.

### User Goals
- Create a safe, frictionless investment platform that demystifies Web3 while outperforming legacy banking in speed and fairness.
- Deliver a product experience that matches the expectations of digital-native Gen Z: fast, social, intuitive, and aligned with values like sustainability and fairness.
- Encourage consistent investing behavior through gamification and protective guidance from an AI CFO.

### Background Context
The financial market for Gen Z is underserved. Traditional banking alienates younger users with fees, delays, and outdated UX, while Web3 remains a minefield of technical risks and hidden costs. Competitors like Cash App, Robinhood, and Coinbase capture fragments of the opportunity but fail to fully address the **trust + usability gap**. STACK bridges this gap with a hybrid model and an experience that feels designed for Gen Z culture.

---

## User Personas

### Taylor – The Conscious & Connected Investor (Primary Persona)
- **Age:** 22
- **Profile:** Digitally native, balances part-time work with side hustles (e.g., Etsy). Ambitious but cautious.
- **Digital Habits:** Lives on TikTok, Instagram, Reddit, and Discord. Uses Notion/Pinterest for visual planning. Expects fast, engaging, intuitive experiences that feel like “TikTok-meets-Cash App.”
- **Financial Behaviors:** Keeps most funds in savings + P2P apps (Cash App, Venmo). Dabbles on Robinhood but distrusts its business model. Avoids crypto due to complexity.
- **Values/Motivations:** Wants financial independence, safety, and alignment with identity (e.g., sustainability, social impact). Goals: travel fund, apartment savings, safety net.

### Jordan – The Banking-Frustrated Beginner
- **Age:** 21
- **Pain Point:** Clunky traditional banking, delays (3–5 day ACH transfers), and punitive fees. Feels alienated by outdated systems.

### Chris – The Crypto-Curious but Overwhelmed
- **Age:** 19
- **Pain Point:** Intimidated by seed phrases, high gas fees, and irreversible mistakes. Tried but abandoned crypto apps after losing money.

---

## Functional Requirements

### Core MVP Features
1.  **User Onboarding & Managed Wallet**
    * Simple sign-up with email/phone.
    * **NEW:** Support for **passcode-based** app access.
    * Automatic creation of a secure, managed wallet using **Circle Developer-Controlled Wallets**.
    * No seed phrase complexity; custody abstracted away.

2.   **Stablecoin Funding Flow (Deposit & Off-Ramp)**
    * Support deposits of USDC from at least one EVM chain (e.g., Ethereum) and one non-EVM chain (e.g., Solana) into the user's Circle wallet.
    * **NEW:** Create and manage virtual accounts linked to user Alpaca brokerage accounts.
    * **NEW:** Orchestrate an immediate **USDC-to-USD off-ramp** via **Due API**.
    * **NEW:** Transfer off-ramped USD directly into the user's linked Alpaca brokerage account to create \"buying power\" for instant trading.
    * **NEW:** Integrate KYC verification via **Sumsub** for regulatory compliance.
    * **NEW:** Implement recipient management for managing withdrawal destinations.

3.  **Investment Flow (Stocks, ETFs, & Options)**
    * Ability to invest in curated baskets of stocks/ETFs.
    * **NEW:** Ability to trade **options**.
    * Simple portfolio view with performance tracking (pulling data from Alpaca).

4.  **Curated Investment Baskets**
    * Launch with 5–10 "expert-curated" investment baskets (e.g., Tech Growth, Sustainability, ETFs).
    * Designed to simplify decision-making for new investors.

5.  **AI CFO (MVP Version)**
    * Provides automated weekly performance summaries.
    * On-demand portfolio analysis to highlight diversification, risk, and potential mistakes.
    * (Implementation: Built in Go, pulls data from Alpaca via Investing Service).

6.  **Brokerage & Withdrawal Flow**
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

## Success Metrics

### Business Objectives
- **User Acquisition:** 10,000 Monthly Active Users (MAU) within 6 months post-launch.
- **Monetization:** 5% conversion from free users to premium tier in Year 1.
- **Validation:** $1,000,000 in processed investment volume in Year 1.

### User Success Metrics
- **Empowerment:** Users feel more in control of their financial future (via surveys).
- **Confidence:** Users feel safe and protected (via NPS and retention).
- **Habit Formation:** % of users with recurring investments increases steadily.

### Key Performance Indicators (KPIs)
- **Engagement:** Daily Active Users (DAU), Monthly Active Users (MAU).
- **Retention:** Week 1, Month 1, Month 3 retention rates.
- **Conversion:** Sign-up → Funded Account rate; Free → Premium rate.
- **Financial:** Total Assets Under Management (AUM).

---

## Technical Considerations

### Target Platforms
- Native mobile applications for **iOS and Android**.

### Technology Stack
- **Frontend:** React Native (cross-platform mobile framework).
- **Backend:** **Go**
- **Database:** PostgreSQL.

### Infrastructure & Integrations
- **0G** for storage and AI capabilities.
- **Circle** for **Developer-Controlled Wallets** (custody only - no off-ramp/on-ramp).
- **Due** for **USDC <-> USD on/off-ramps**, virtual accounts, and recipient management.
- **Sumsub** for KYC verification and compliance.
- **Alpaca** for brokerage, trade execution, and custody of traditional assets.

### Architecture Strategy
- Initial approach: **Modular Monolith** (Go services) within a monorepo for faster MVP delivery.
- Long-term: Designed to evolve into a more distributed architecture as user base scales.

### Constraints
- **Timeline:** 3-week deadline for MVP cycle.
- **Dependencies:** Reliance on 0G, Circle, and Alpaca APIs.

### Assumptions
- Regulatory compliance model is viable.
- Third-party APIs (0G, Circle, Alpaca) are stable and cost-effective at scale.

---

## Risks & Open Questions

### Key Risks
1.  **Regulatory Risk:**
    * Hybrid Web3 + traditional model may face compliance challenges in multiple jurisdictions.
    * Potential impact on stablecoin usage, custody models, and cross-border flows.

2.  **Execution Risk:**
    * Tight 3-week MVP timeline creates potential for scope creep or missed deadlines.
    * Reliance on multiple third-party APIs increases fragility.

3.  **Technical Risk (APIs):**
    * Performance and reliability of 0G, Circle, and Alpaca APIs are critical to user experience.
    * Unknown at-scale costs of API usage.

4.  **Market Risk (User Trust):**
    * Gen Z skepticism toward both banks and crypto means adoption may be slower than expected.
    * Building trust will require careful onboarding and consistent reliability.

---

### Open Questions
- **RESOLVED:** Which brokerage partner will be selected? (Answer: Alpaca)
- **RESOLVED:** What wallet management solution? (Answer: Circle Developer-Controlled Wallets)
- Has a full legal/regulatory review been conducted for the *specific* USDC -> Alpaca flow?
- What are the true at-scale costs of Circle's off-ramp + Alpaca's funding APIs?
- How will fraud prevention and compliance (KYC/AML) be handled in this multi-partner flow?

# STACK MVP Epics (v0.2)

---

## Epic 1: Onboarding & Wallet Management
**Summary:**
Deliver a smooth sign-up and wallet creation process that hides Web3 complexity.

**In-Scope Features:**
- Simple, mobile-first sign-up flow.
- **NEW:** **Passcode support** for app login.
- Managed wallet creation using **Circle Developer-Controlled Wallets** (no seed phrases).
- Security + custody abstraction.

**Success Criteria:**
- 90%+ of new users complete onboarding successfully.
- Wallet creation works 99%+ of the time.

---

## Epic 2: Stablecoin Funding Flow
**Summary:**
Enable users to fund their brokerage accounts instantly with stablecoins.

**In-Scope Features:**
- Support deposits from Ethereum (EVM) and Solana (non-EVM) into the user's Circle wallet.
- **NEW:** Orchestrate an immediate **USDC-to-USD off-ramp** via Circle.
- **NEW:** Securely transfer the resulting USD into the user's **Alpaca** brokerage account.
- **NEW:** Handle the reverse flow: **USD (Alpaca) -> USDC (Circle)** for withdrawals.

**Success Criteria:**
- Users can fund their brokerage account within minutes of a confirmed stablecoin deposit.
- At least 2 supported deposit pathways at launch.
- End-to-end funding/withdrawal success rate of >99%.

---

## Epic 3: Curated Baskets
**Summary:**
Provide beginner-friendly, expert-curated investment options.

**In-Scope Features:**
- 5–10 prebuilt baskets (Tech Growth, Sustainability, ETFs, etc.).
- Balanced for simplicity + diversity.

**Success Criteria:**
- 80%+ of first investments made via curated baskets.
- Positive user feedback on basket clarity (≥7/10 rating).

---

## Epic 4: Investment Flow (Stocks & Options)
**Summary:**
Allow users to invest seamlessly in traditional assets with clear portfolio visibility.

**In-Scope Features:**
- Ability to invest in curated baskets (from Epic 3).
- **NEW:** Ability to trade **options**.
- Simple portfolio view (holdings + performance) by pulling data from Alpaca.

**Success Criteria:**
- ≥100 funded accounts make at least one investment.
- Portfolio updates within seconds of trade execution from Alpaca.

---

## Epic 5: AI CFO (MVP Version)
**Summary:**
Give users a lightweight AI financial guide for trust and protection.

**In-Scope Features:**
- Automated weekly performance summaries.
- On-demand portfolio analysis (basic insights).
- (Implementation: Built in **Go**, pulls data from Alpaca).

**Success Criteria:**
- ≥70% of active users read at least one AI CFO summary.
- >60% of users report increased confidence in investing.

---

## Epic 6: Brokerage Integration
**Summary:**
Connect with **Alpaca** for trade execution and asset custody.

**In-Scope Features:**
- Secure backend integration (in **Go**) with **Alpaca APIs** for trade execution.
- Custody of stocks/ETFs/options via partner integration.

**Success Criteria:**
- ≥99% trade execution success rate with Alpaca.
- Integration passes security and compliance checks.
