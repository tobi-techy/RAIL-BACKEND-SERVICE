# Rail User Flows & Navigation Guide

## Document Information
- **Version**: 1.0
- **Date**: December 15, 2025
- **Purpose**: User flow documentation for Rail app development and design

---

## Table of Contents

1. [Introduction](#introduction)
2. [Core User Principles](#core-user-principles)
3. [Primary User Flows](#primary-user-flows)
4. [Navigation Structure](#navigation-structure)
5. [Screen-by-Screen Flows](#screen-by-screen-flows)
6. [User Journey Maps](#user-journey-maps)
7. [Edge Cases & Error Flows](#edge-cases--error-flows)
8. [Success Metrics](#success-metrics)

---

## Introduction

Rail is an automated wealth system designed for Gen Z users who want their money to work without the burden of financial decision-making. This document outlines how users navigate through the app and the key flows that enable Rail's core promise: **money starts working the moment it arrives**.

### Core System Rule
Every deposit into Rail is automatically split:
- **70% → Spend Balance** (everyday expenses)
- **30% → Invest Engine** (automatic allocation)

This split is system-defined, always on, and not user-editable in MVP.

---

## Core User Principles

### Design Philosophy
Rail's user experience is built on these principles:

1. **Zero Decisions Increase Confidence**
   - Users don't allocate, configure, or optimize
   - The system acts with predetermined rules

2. **Speed Creates Belief**
   - Deposit → Split → Deploy in < 60 seconds
   - Instant visual feedback on all actions

3. **Defaults Outperform Settings**
   - No configuration screens in MVP
   - System intelligence replaces user choices

4. **State Matters More Than Detail**
   - Show "Is my money working?" not "What is it doing?"
   - Direction over detail

### User Success Criteria
- Users feel money is working without their intervention
- Spending feels normal (like a checking account)
- Investing feels invisible (no trade confirmations)
- Users rarely pause or intervene with automation

---

## Primary User Flows

### Flow 1: First-Time User Onboarding
**Goal**: Get from download to funded account in < 2 minutes

```
Download App → Apple Sign-In → KYC → Auto Account Creation → Ready to Fund
```

**Key Screens**:
1. Welcome/Landing
2. Apple Sign-In
3. Basic Info Collection
4. KYC Document Upload
5. Processing/Verification
6. Account Ready

**Success Metrics**:
- Onboarding completion rate > 80%
- Time to completion < 2 minutes
- Drop-off points identified and minimized

### Flow 2: Loading Money (Funding)
**Goal**: Enable funding and trigger automatic 70/30 split

```
Choose Funding Method → Load Money → Automatic Split → Money Working
```

**Funding Options**:
- Virtual Account (USD/GBP bank transfer)
- Multi-chain USDC (Ethereum, Polygon, BSC, Solana)

**Key Screens**:
1. Funding Method Selection
2. Virtual Account Details / Crypto Deposit Address
3. Deposit Confirmation
4. Split Visualization
5. Updated Balances

**Success Metrics**:
- Deposit → split completion < 60 seconds
- First funding completion rate > 70%
- Repeat funding behavior

### Flow 3: Daily Spending
**Goal**: Seamless spending from Spend Balance

```
Card Transaction → Real-time Authorization → Balance Update → Optional Round-up
```

**Key Interactions**:
1. Card swipe/tap (external)
2. Authorization check
3. Balance deduction
4. Round-up calculation (if enabled)
5. Transaction confirmation

**Success Metrics**:
- Authorization success rate > 99%
- Balance update latency < 1 second
- Round-up adoption rate

### Flow 4: Monitoring Progress (Station)
**Goal**: Answer "Is my money working?" at a glance

```
Open App → Station View → Check Status → Confidence in System
```

**Key Elements**:
1. Total balance (prominent)
2. Spend/Invest split view
3. System status indicator
4. Recent activity (minimal)

**Success Metrics**:
- Daily app opens
- Time spent on Station screen
- User confidence surveys

---

## Navigation Structure

### App Architecture
Rail uses a minimal navigation structure to reduce cognitive load:

```
┌─────────────────────────────────────────┐
│                STATION                   │  ← Primary screen
│            (Home/Dashboard)              │
├─────────────────────────────────────────┤
│  Balance  │  Card  │  Load  │  Profile  │  ← Bottom tabs
└─────────────────────────────────────────┘
```

### Primary Navigation Tabs

#### 1. Station (Home) 🏠
- **Purpose**: Answer "Is my money working?"
- **Content**: Total balance, spend/invest split, system status
- **Frequency**: Most visited screen

#### 2. Balance 💰
- **Purpose**: Detailed balance view and transaction history
- **Content**: Spend balance, recent transactions, round-up status
- **Frequency**: Regular checking

#### 3. Card 💳
- **Purpose**: Card management and spending controls
- **Content**: Virtual card details, freeze/unfreeze, round-up toggle
- **Frequency**: Occasional management

#### 4. Load 📥
- **Purpose**: Add money to Rail
- **Content**: Funding methods, deposit addresses, recent deposits
- **Frequency**: Periodic funding

#### 5. Profile ⚙️
- **Purpose**: Account settings and support
- **Content**: Personal info, security, help, legal
- **Frequency**: Rare access

### Navigation Principles
- **No nested navigation** in MVP
- **Single tap access** to all primary functions
- **Clear visual hierarchy** with system status always visible
- **Consistent bottom tab bar** across all screens

---

## Screen-by-Screen Flows

### Onboarding Flow

#### Screen 1: Welcome
**Purpose**: Introduce Rail's value proposition
**Content**:
- Rail logo and tagline
- "Money starts working the moment it arrives"
- "Get Started" CTA button
- Legal disclaimers (minimal)

**User Actions**:
- Tap "Get Started" → Apple Sign-In

**Design Notes**:
- No feature explanations or tutorials
- Focus on outcome, not process
- Clean, confident visual design

#### Screen 2: Apple Sign-In
**Purpose**: Secure, frictionless authentication
**Content**:
- Apple Sign-In button (primary)
- "Continue with Email" (fallback)
- Privacy statement

**User Actions**:
- Tap Apple Sign-In → Face ID/Touch ID → Basic Info
- Tap Email option → Email form

**Design Notes**:
- Apple Sign-In prominently featured
- Email as clear secondary option
- No password creation in primary flow

#### Screen 3: Basic Information
**Purpose**: Collect minimal required data
**Content**:
- First name, last name
- Phone number
- Date of birth
- "Continue" button

**User Actions**:
- Fill required fields → KYC verification

**Design Notes**:
- Only essential fields shown
- Clear field validation
- Progress indicator (optional)

#### Screen 4: Identity Verification
**Purpose**: KYC compliance with minimal friction
**Content**:
- "Verify your identity" heading
- Document type selection (ID, Passport)
- Camera capture interface
- "Processing" state

**User Actions**:
- Select document type
- Capture front/back photos
- Submit for verification

**Design Notes**:
- Clear photo guidelines
- Real-time capture feedback
- Processing state with estimated time

#### Screen 5: Account Ready
**Purpose**: Confirm successful onboarding
**Content**:
- "Your Rail account is ready" message
- Account summary (balances start at $0)
- "Load Money" CTA button

**User Actions**:
- Tap "Load Money" → Funding flow
- Tap "Explore" → Station screen

**Design Notes**:
- Celebration moment
- Clear next step guidance
- Immediate funding option

### Funding Flow

#### Screen 1: Choose Funding Method
**Purpose**: Select how to add money
**Content**:
- "Load Money" heading
- Virtual Account option (USD/GBP)
- Crypto Deposit option (USDC)
- Method comparison (speed, limits)

**User Actions**:
- Tap Virtual Account → Bank details
- Tap Crypto → Chain selection

**Design Notes**:
- Equal visual weight for both options
- Clear speed/limit indicators
- No preference implied

#### Screen 2A: Virtual Account Details
**Purpose**: Provide bank transfer information
**Content**:
- Dedicated account number
- Routing information
- Transfer instructions
- "Copy Details" buttons

**User Actions**:
- Copy account details
- Share details to banking app
- Return to Rail to monitor

**Design Notes**:
- Easy copy/share functionality
- Clear transfer instructions
- Expected timing information

#### Screen 2B: Crypto Deposit
**Purpose**: Provide deposit address for USDC
**Content**:
- Chain selection (ETH, Polygon, BSC, Solana)
- Deposit address (QR + text)
- Network warnings
- "Copy Address" button

**User Actions**:
- Select chain
- Copy address or scan QR
- Send USDC from external wallet

**Design Notes**:
- Clear network selection
- Prominent address display
- Network-specific warnings

#### Screen 3: Deposit Confirmation
**Purpose**: Confirm deposit received and split applied
**Content**:
- "Deposit received" confirmation
- Amount deposited
- 70/30 split visualization
- Updated balance display

**User Actions**:
- View updated balances
- Return to Station
- Load more money (optional)

**Design Notes**:
- Clear split visualization
- Immediate balance updates
- Positive confirmation messaging
