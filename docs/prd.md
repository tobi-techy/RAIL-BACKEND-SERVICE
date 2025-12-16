# Rail — Product Requirements Document (MVP)

## 1. Product Objective

Rail’s MVP must prove one core behavior:

> Users will trust Rail enough to make it their **primary spending account** while allowing the system to automatically invest part of their money without ongoing input.

The MVP is not about returns, optimization, or feature depth.
It is about **default adoption, trust, and habit formation**.

Success = money flows in, spending happens normally, investing happens automatically, and users do not interfere.

---

## 2. Target User

**Persona:** The Indecisive Optimist (18–26)

**User Jobs-To-Be-Done:**

* Spend money without friction
* Avoid financial decision-making
* Make progress toward wealth passively

**Design Constraint:**
User attention is limited. Any screen requiring explanation is a failure.

---

## 3. Core System Rule (Non-Negotiable)

Every deposit into Rail is automatically split:

* **70% → Spend Balance** (everyday expenses)
* **30% → Invest Engine** (automatic allocation)

This rule is:

* System-defined
* Always on in MVP
* Not user-editable
* Clearly disclosed before first deposit

Depositing funds equals consent to system behavior.

---

## 4. MVP Feature Prioritization

### Tier 0 — Existential (Must Ship)

These features define Rail. Without them, the product is invalid.

---

## 4.1 Account & Onboarding

**Requirements:**

* iOS-only
* Apple Sign-In (primary)
* Email / phone fallback
* Lightweight KYC via partner
* Auto-generated account and ledger

**Acceptance Criteria:**

* Onboarding completed in < 2 minutes
* No finance or investing explanations

---

## 4.2 Funding & Deposits

**Requirements:**

* Virtual accounts for fiat deposits (USD, GBP)
* Multi-chain USDC deposits (Ethereum, Polygon, BSC, Solana)
* Instant or near-instant confirmation
* Deposit triggers automatic split

**Funding Methods:**

* **Virtual Accounts**: Bank transfer to user's dedicated USD or GBP virtual account
* **Crypto On-Ramp**: USDC deposits from any supported chain

**UX Rules:**

* Use language: "Load money"
* No deposit settings
* No allocation choices

**Acceptance Criteria:**

* Deposit → split → system state update in < 60 seconds
* Virtual accounts support USD and GBP
* USDC accepted from Ethereum, Polygon, BSC, and Solana

---

## 4.3 Spend Balance (Primary Surface)

**Requirements:**

* Real-time spendable balance
* Ledger-backed accuracy
* Fully liquid

**Non-Goals:**

* Budgeting tools
* Spend limits
* Categories in MVP

This balance must feel like a checking account replacement.

---

## 4.4 Debit Card (Virtual First)

**Requirements:**

* Virtual debit card at launch
* Physical card queued post-MVP
* Card linked directly to Spend Balance

**Acceptance Criteria:**

* Card usable immediately after funding

---

## 4.5 Automatic Investing Engine

**Requirements:**

* Receives 30% of every deposit
* Capital deploys automatically
* No user interaction required

**Implementation Reality (v1):**

* Rules-based allocation
* Limited internal strategy buckets
* Global fallback strategy

**UX Rules:**

* No asset visibility
* No trade confirmations

---

## 4.6 Conductors — Expert-Led Tracks

For users who want guided growth without self-directed decisions, Rail offers Conductors.

**What is a Conductor?**

A Conductor is a verified professional investor who creates and manages Tracks — curated portfolios of assets that followers can automatically mirror.

**The Metaphor**

* A **Conductor** leads the investment journey
* A **Track** is the path they create (a portfolio of assets like Apple, ETFs, etc.)
* **Followers** ride the track — their capital automatically mirrors the Conductor's moves

**How It Works**

1. Conductors apply, get reviewed, and are approved based on credentials and experience
2. Approved Conductors create Tracks with specific asset allocations
3. Users browse Tracks and choose to follow one or more
4. A portion of the user's Active Rail is allocated to the Track
5. When the Conductor adjusts the Track, followers' positions update automatically

**Conductor Application Flow**

* Submit application with investment experience and credentials
* Admin review for background check and compliance
* Approval grants Conductor status
* Conductor creates profile and first Track

**Track Characteristics**

* Named strategy (e.g., "Tech Growth", "Dividend Income")
* Curated assets with target weights
* Risk level indicator
* Performance history visible to potential followers

**User Experience**

* Discover Conductors and their Tracks
* View performance, risk level, and follower count
* Follow with one tap — allocate a portion of Active Rail
* Unfollow anytime — positions liquidate back to Active Rail

**Why Conductors Fit Rail's Philosophy**

Conductors extend Rail's core principle: users delegate decisions to systems (or experts) that act on their behalf. Following a Track requires no ongoing input — the Conductor leads, the system mirrors, the user benefits.

This is not self-directed investing. This is choosing who to trust, then letting go.

Rail’s experience is designed around trust through action, not education.

**Core principles:**

* Fewer decisions increase confidence
* Speed creates belief
* Defaults outperform settings
* State matters more than detail

If a screen requires explanation, it’s wrong.
If a user hesitates, something failed.


## 4.7 Round-Ups Automation

**Requirements:**

* Optional round-ups on card transactions
* Round-up amounts routed to Invest Engine

**UX Rules:**

* Simple ON/OFF toggle
* No configuration granularity

---

## 4.8 Home Screen (Station)

**Requirements:**

* Total balance
* Spend balance
* Invest balance
* System status: Allocating / Active / Paused

**Explicitly Excluded:**

* Charts
* Asset breakdowns
* Performance history

---

## 5. Tier 1 — Important (Post-MVP, Not Day 1)

These features increase retention but are not required to validate the model.

* Physical debit card shipping
* Cashback rewards
* Push notifications
* Transaction categorization
* Basic spend history
* Conductors (Copy Trading)

## 6. Tier 2 — Future Expansion (Explicitly Out of Scope)

* Adjustable allocation ratios
* Manual investing
* Goal setting
* Credit or lending
* Tax filing
* Accounting tools

These features are strategic extensions, not MVP needs.

---

## 7. Non-Functional Requirements

* iOS launch time < 2 seconds
* Ledger accuracy > 99.9%
* Crash-free sessions > 99.5%
* Secure key management via partners

Reliability beats innovation at MVP.

---

## 8. Compliance & Language Constraints

* No promises of returns
* No investment advice language
* No personalization claims beyond system rules

The system executes predefined behavior.

---

## 9. Success Metrics (MVP)

**Primary Metrics:**

* % of users funding within first session
* % of deposits auto-invested
* % of users who keep automation enabled after 7 days

**Secondary Metrics:**

* Daily spend activity
* Repeat deposits

Vanity metrics are ignored.

---

## 10. Definition of Done

The MVP is complete when:

* A user can sign up, load money, spend, and auto-invest in one session
* The 70/30 split happens without configuration
* Spending feels normal, investing feels invisible

If users feel responsible for decisions, the MVP failed.
