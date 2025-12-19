# Rail MVP — Epics & Features

## Document Information
- **Version**: 1.0
- **Last Updated**: December 15, 2025
- **Status**: Active

---

## Overview

This document outlines the MVP epics for Rail — an automated wealth system where money begins working the moment it arrives. Each epic contains user stories and acceptance criteria required for launch.

**Core System Rule**: Every deposit is automatically split 70% Spend / 30% Invest.

---

## Epic 1: User Onboarding & Authentication

**Goal**: Get users from download to funded account in under 2 minutes with zero friction.

### Features

| ID | Feature | Priority | Description |
|----|---------|----------|-------------|
| 1.1 | Apple Sign-In | P0 | Primary authentication method |
| 1.2 | Email/Phone Fallback | P0 | Alternative auth for non-Apple users |
| 1.3 | KYC Integration | P0 | Lightweight identity verification via partner |
| 1.4 | Auto Account Creation | P0 | Automatic ledger and account setup on signup |
| 1.5 | Passcode Setup | P0 | Secondary authentication for sensitive actions |

### User Stories

**US-1.1**: As a new user, I can sign up using Apple Sign-In so that I can start quickly without creating new credentials.

**US-1.2**: As a new user, I can complete identity verification in-app so that I can access all features.

**US-1.3**: As a new user, my account and ledger are automatically created so that I can immediately load funds.

### Acceptance Criteria
- [ ] Onboarding completed in < 2 minutes
- [ ] No finance or investing explanations shown
- [ ] KYC status gates funding features
- [ ] Account and ledger created automatically on signup

---

## Epic 2: Funding & Deposits

**Goal**: Enable users to load money and trigger the automatic 70/30 split within 60 seconds.

### Features

| ID | Feature | Priority | Description |
|----|---------|----------|-------------|
| 2.1 | Virtual Accounts (USD/GBP) | P0 | Dedicated virtual accounts for bank transfers |
| 2.2 | Multi-Chain USDC Deposits | P0 | USDC deposits from Ethereum, Polygon, BSC, Solana |
| 2.3 | Deposit Confirmation | P0 | Instant or near-instant confirmation |
| 2.4 | Automatic Split Engine | P0 | 70/30 split on every deposit |
| 2.5 | Deposit Webhooks | P0 | Real-time deposit detection from Due Network and Circle |

### User Stories

**US-2.1**: As a user, I can load money via bank transfer to my virtual account so that I can fund with fiat.

**US-2.2**: As a user, I can deposit USDC from multiple chains so that I can fund with crypto.

**US-2.3**: As a user, my deposit is automatically split 70/30 so that I don't have to make allocation decisions.

**US-2.4**: As a user, I receive confirmation when my deposit is processed so that I know my money arrived.

### Acceptance Criteria
- [ ] Deposit → split → system state update in < 60 seconds
- [ ] Virtual accounts support USD and GBP currencies
- [ ] USDC accepted from Ethereum, Polygon, BSC, and Solana
- [ ] 70% credited to Spend Balance
- [ ] 30% credited to Invest Balance
- [ ] No deposit settings or allocation choices shown
- [ ] Language uses "Load money" not "Deposit"

---

## Epic 3: Spend Balance & Ledger

**Goal**: Provide a real-time, accurate spend balance that feels like a checking account replacement.

### Features

| ID | Feature | Priority | Description |
|----|---------|----------|-------------|
| 3.1 | Real-Time Balance | P0 | Instant balance updates |
| 3.2 | Ledger Accuracy | P0 | 99.9% accuracy requirement |
| 3.3 | Transaction History | P1 | Basic list of transactions |
| 3.4 | Balance Liquidity | P0 | Fully liquid spend balance |

### User Stories

**US-3.1**: As a user, I can see my current spend balance so that I know how much I can spend.

**US-3.2**: As a user, my balance updates in real-time after transactions so that I always have accurate information.

### Acceptance Criteria
- [ ] Balance updates within 1 second of transaction
- [ ] Ledger accuracy > 99.9%
- [ ] Spend balance is fully liquid
- [ ] No budgeting tools, spend limits, or categories in MVP

---

## Epic 4: Debit Card

**Goal**: Provide a virtual debit card linked to Spend Balance, usable immediately after funding.

### Features

| ID | Feature | Priority | Description |
|----|---------|----------|-------------|
| 4.1 | Virtual Card Issuance | P0 | Instant virtual card on funding |
| 4.2 | Card-to-Balance Link | P0 | Direct link to Spend Balance |
| 4.3 | Transaction Authorization | P0 | Real-time auth against balance |
| 4.4 | Physical Card | P1 | Post-MVP physical card shipping |

### User Stories

**US-4.1**: As a user, I receive a virtual debit card so that I can start spending immediately.

**US-4.2**: As a user, my card transactions deduct from my Spend Balance so that spending is seamless.

### Acceptance Criteria
- [ ] Virtual card usable immediately after first funding
- [ ] Card linked directly to Spend Balance
- [ ] Real-time authorization against available balance

---

## Epic 5: Automatic Investing Engine

**Goal**: Deploy 30% of deposits automatically without user interaction.

### Features

| ID | Feature | Priority | Description |
|----|---------|----------|-------------|
| 5.1 | Auto-Allocation | P0 | Automatic capital deployment |
| 5.2 | Strategy Engine | P0 | Rules-based allocation logic |
| 5.3 | Brokerage Integration | P0 | Alpaca integration for trade execution |
| 5.4 | Position Tracking | P0 | Track user positions |
| 5.5 | Global Fallback Strategy | P0 | Default strategy for all users |

### User Stories

**US-5.1**: As a user, my invest balance is automatically deployed so that I don't have to make investment decisions.

**US-5.2**: As a user, I don't see individual trades or assets so that investing feels invisible.

### Acceptance Criteria
- [ ] 30% of deposits auto-allocated
- [ ] No user interaction required
- [ ] No asset visibility in MVP
- [ ] No trade confirmations shown
- [ ] Trades executed via Alpaca

---

## Epic 6: Round-Ups Automation

**Goal**: Enable optional round-ups that route spare change to the Invest Engine.

### Features

| ID | Feature | Priority | Description |
|----|---------|----------|-------------|
| 6.1 | Round-Up Calculation | P0 | Calculate round-up on each transaction |
| 6.2 | Round-Up Toggle | P0 | Simple ON/OFF setting |
| 6.3 | Round-Up Routing | P0 | Route to Invest Balance |

### User Stories

**US-6.1**: As a user, I can enable round-ups so that my spare change automatically invests.

**US-6.2**: As a user, I can toggle round-ups ON/OFF so that I have simple control.

### Acceptance Criteria
- [ ] Simple ON/OFF toggle only
- [ ] No configuration granularity
- [ ] Round-ups routed to Invest Engine
- [ ] Round-up amount = next dollar - transaction amount

---

## Epic 7: Home Screen (Station)

**Goal**: Answer "Is my money working?" with a single glance.

### Features

| ID | Feature | Priority | Description |
|----|---------|----------|-------------|
| 7.1 | Total Balance Display | P0 | Combined balance view |
| 7.2 | Spend/Invest Split View | P0 | Show 70/30 allocation |
| 7.3 | System Status | P0 | Allocating / Active / Paused state |

### User Stories

**US-7.1**: As a user, I can see my total balance so that I know my overall position.

**US-7.2**: As a user, I can see my Spend and Invest balances so that I understand the split.

**US-7.3**: As a user, I can see the system status so that I know if my money is working.

### Acceptance Criteria
- [ ] Total balance displayed prominently
- [ ] Spend balance and Invest balance shown
- [ ] System status visible: Allocating / Active / Paused
- [ ] NO charts, asset breakdowns, or performance history

---

## Epic 8: Conductors (Copy Trading) — Post-MVP

**Goal**: Allow users to follow professional investors and automatically mirror their portfolios.

### Features

| ID | Feature | Priority | Description |
|----|---------|----------|-------------|
| 8.1 | Conductor Application | P1 | Application flow for existing users |
| 8.2 | Application Review | P1 | Admin review and approval workflow |
| 8.3 | Conductor Profile | P1 | Public profile for approved conductors |
| 8.4 | Track Creation | P1 | Create and manage investment tracks |
| 8.5 | Track Assets | P1 | Add/remove/reweight assets in tracks |
| 8.6 | Track Discovery | P1 | Browse and search available tracks |
| 8.7 | Follow Track | P1 | One-tap follow with allocation |
| 8.8 | Copy Engine | P1 | Automatic trade mirroring |
| 8.9 | Unfollow Track | P1 | Exit and liquidate positions |

### User Stories

**US-8.1**: As an existing Rail user, I can apply to become a Conductor so that I can share my investment strategy.

**US-8.2**: As an admin, I can review and approve/reject Conductor applications so that only qualified investors lead tracks.

**US-8.3**: As a Conductor, I can create Tracks with specific assets and weights so that followers can mirror my strategy.

**US-8.4**: As a user, I can browse Conductors and their Tracks so that I can find strategies to follow.

**US-8.5**: As a user, I can follow a Track with one tap so that my Active Rail mirrors the Conductor's positions.

**US-8.6**: As a user, when a Conductor updates their Track, my positions update automatically so that I stay in sync.

**US-8.7**: As a user, I can unfollow a Track anytime so that my positions liquidate back to Active Rail.

### Acceptance Criteria
- [ ] Only existing Rail users can apply to be Conductors
- [ ] Admin can review, approve, or reject applications
- [ ] Approved Conductors can create and manage Tracks
- [ ] Tracks have name, description, risk level, and asset weights
- [ ] Users can discover and follow Tracks
- [ ] Track changes propagate to followers within 5 minutes
- [ ] Users can unfollow and liquidate anytime

---

## Epic 9: Infrastructure & Non-Functional

**Goal**: Ensure reliability, security, and performance meet MVP standards.

### Features

| ID | Feature | Priority | Description |
|----|---------|----------|-------------|
| 9.1 | iOS Performance | P0 | App launch < 2 seconds |
| 9.2 | Ledger Accuracy | P0 | > 99.9% accuracy |
| 9.3 | Crash-Free Sessions | P0 | > 99.5% crash-free |
| 9.4 | Secure Key Management | P0 | Partner-managed custody |
| 9.5 | JWT Authentication | P0 | Secure token-based auth |
| 9.6 | Data Encryption | P0 | AES-256-GCM at rest |
| 9.7 | Rate Limiting | P0 | API abuse prevention |

### Acceptance Criteria
- [ ] iOS launch time < 2 seconds
- [ ] Ledger accuracy > 99.9%
- [ ] Crash-free sessions > 99.5%
- [ ] All sensitive data encrypted at rest
- [ ] Rate limiting on all public endpoints

---

## Priority Legend

| Priority | Meaning |
|----------|---------|
| P0 | Must ship for MVP — product is invalid without it |
| P1 | Important for retention — ship shortly after MVP |
| P2 | Future expansion — explicitly out of scope |

---

## Success Metrics

### Primary (MVP Validation)
- % of users funding within first session
- % of deposits auto-invested
- % of users who keep automation enabled after 7 days

### Secondary
- Daily spend activity
- Repeat deposits
- Time from signup to first funding

---

## Definition of Done

MVP is complete when:
1. User can sign up, load money, spend, and auto-invest in one session
2. 70/30 split happens without configuration
3. Spending feels normal, investing feels invisible
4. User does not feel responsible for decisions
