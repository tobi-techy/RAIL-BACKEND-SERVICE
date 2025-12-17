# Linear Issues for Rail MVP Missing Implementations

## Critical MVP Blockers (P0)

### Issue 1: Implement Apple Sign-In Authentication
```
Title: Implement Apple Sign-In Authentication
Priority: Urgent
Labels: mvp, authentication, p0, ios
Team: Backend
Epic: User Onboarding & Authentication

Description:
Implement Apple Sign-In as the primary authentication method for iOS users. The social auth framework exists but Apple-specific implementation is missing.

Requirements:
- Apple Sign-In SDK integration
- Apple ID token validation server-side
- User profile creation from Apple ID
- Fallback to email/phone for non-Apple users

Acceptance Criteria:
- [ ] Users can sign up using Apple Sign-In
- [ ] Apple ID tokens are validated server-side  
- [ ] User accounts created automatically from Apple profile
- [ ] Seamless onboarding flow completes in < 2 minutes

Technical Notes:
- Extend existing social auth service in internal/domain/services/socialauth/
- Add Apple provider configuration to config.go
- Implement Apple ID token verification
- Update auth handlers to support Apple Sign-In flow

Estimate: 5 story points
```

### Issue 2: Implement Automatic 70/30 Deposit Split Engine
```
Title: Implement Automatic 70/30 Deposit Split Engine
Priority: Urgent
Labels: mvp, funding, automation, p0
Team: Backend
Epic: Funding & Deposits

Description:
Implement the core system rule where every deposit is automatically split 70% to Spend Balance and 30% to Invest Balance without any user interaction.

Requirements:
- Deposit webhook triggers automatic split
- 70% credited to Spend Balance
- 30% credited to Invest Balance  
- Real-time balance updates
- No user configuration options

Acceptance Criteria:
- [ ] All deposits automatically split 70/30
- [ ] Split occurs within 60 seconds of deposit confirmation
- [ ] No user settings or allocation choices shown
- [ ] Balance updates reflect split immediately
- [ ] Works for both crypto and fiat deposits

Technical Notes:
- Enhance funding webhook handlers in internal/workers/funding_webhook/
- Integrate with allocation service in internal/domain/services/allocation/
- Update balance service for real-time updates
- Add split logic to deposit confirmation flow

Estimate: 8 story points
```

### Issue 3: Implement Virtual Debit Card System
```
Title: Implement Virtual Debit Card System
Priority: Urgent
Labels: mvp, debit-card, payments, p0
Team: Backend
Epic: Debit Card

Description:
Implement virtual debit card issuance and management system. Cards should be issued immediately upon first funding and linked directly to Spend Balance.

Requirements:
- Virtual card issuance API integration
- Real-time authorization against Spend Balance
- Card details (number, CVV, expiry) generation
- Transaction processing and balance deduction

Acceptance Criteria:
- [ ] Virtual card issued on first funding
- [ ] Card transactions deduct from Spend Balance in real-time
- [ ] Authorization fails when insufficient balance
- [ ] Card details accessible via API
- [ ] Transaction history tracked

Technical Notes:
- Research and integrate card issuer API (Stripe Issuing, Marqeta, etc.)
- Create card service in internal/domain/services/card/
- Add card handlers in internal/api/handlers/
- Integrate with balance service for real-time authorization
- Add card entities and repositories

Estimate: 13 story points
```

### Issue 4: Complete Automatic Investment Engine
```
Title: Complete Automatic Investment Engine Implementation
Priority: Urgent
Labels: mvp, investing, automation, p0
Team: Backend
Epic: Automatic Investing Engine

Description:
Complete the automatic investment engine to deploy 30% invest balance without user interaction. Alpaca integration exists but auto-deployment logic is missing.

Requirements:
- Auto-deployment of invest balance funds
- Global fallback investment strategy
- Integration with existing Alpaca service
- No trade confirmations or asset visibility
- Position tracking (backend only)

Acceptance Criteria:
- [ ] 30% of deposits auto-invested without user action
- [ ] Default investment strategy defined and implemented
- [ ] No individual trades or assets shown to users
- [ ] Positions tracked in backend systems
- [ ] Investment triggers on balance threshold

Technical Notes:
- Enhance investing service in internal/domain/services/investing/
- Define global fallback strategy (e.g., diversified ETF portfolio)
- Add auto-investment triggers to allocation service
- Integrate with existing Alpaca service
- Ensure trades are invisible to users in API responses

Estimate: 10 story points
```

### Issue 5: Setup USD/GBP Virtual Accounts via Due Network
```
Title: Setup USD/GBP Virtual Accounts via Due Network
Priority: Urgent
Labels: mvp, funding, fiat, p0
Team: Backend
Epic: Funding & Deposits

Description:
Setup dedicated virtual accounts for USD and GBP bank transfers using the existing Due Network integration.

Requirements:
- USD virtual account creation via Due Network
- GBP virtual account creation via Due Network
- Bank transfer routing and processing
- Deposit confirmation and crediting

Acceptance Criteria:
- [ ] Users receive unique USD virtual account details
- [ ] Users receive unique GBP virtual account details
- [ ] Bank transfers credited within standard timeframes
- [ ] Automatic 70/30 split triggered on fiat deposits
- [ ] Virtual account details accessible via API

Technical Notes:
- Extend Due Network adapter in internal/adapters/due/
- Add fiat virtual account creation to onboarding flow
- Update funding handlers to support fiat deposits
- Ensure fiat deposits trigger automatic split
- Add virtual account entities and repositories

Estimate: 8 story points
```

## Important Features (P1)

### Issue 6: Implement Physical Debit Card System
```
Title: Implement Physical Debit Card System
Priority: High
Labels: p1, debit-card, physical-card
Team: Backend
Epic: Debit Card

Description:
Implement physical card ordering, shipping, and management system for users who request physical cards.

Requirements:
- Physical card ordering system
- Shipping address management
- Card activation flow
- Link to existing virtual card system

Acceptance Criteria:
- [ ] Users can order physical cards
- [ ] Shipping address collection and validation
- [ ] Card activation via app
- [ ] Physical card linked to same Spend Balance as virtual card

Dependencies: Virtual Debit Card System (#3)

Estimate: 8 story points
```

### Issue 7: Optimize KYC Flow for Sub-2-Minute Onboarding
```
Title: Optimize KYC Flow for Sub-2-Minute Onboarding
Priority: High
Labels: p1, kyc, onboarding, optimization
Team: Backend
Epic: User Onboarding & Authentication

Description:
Optimize existing KYC flow to achieve the < 2 minute onboarding target while maintaining compliance.

Requirements:
- Streamlined document upload
- Faster verification processing
- Reduced friction points
- Progress indicators

Acceptance Criteria:
- [ ] KYC completion time reduced to < 2 minutes
- [ ] Document upload optimized for mobile
- [ ] Real-time progress feedback
- [ ] Maintain compliance standards

Estimate: 5 story points
```

### Issue 8: Implement Copy Trading System (Conductors)
```
Title: Implement Copy Trading System (Conductors)
Priority: Medium
Labels: p1, copy-trading, post-mvp
Team: Backend
Epic: Copy Trading

Description:
Implement the copy trading system allowing users to follow professional investors (Conductors) and mirror their portfolios.

Requirements:
- Conductor application system
- Track creation and management
- Copy engine for trade mirroring
- Discovery and follow system

Acceptance Criteria:
- [ ] Conductor application and approval workflow
- [ ] Track creation with asset allocation
- [ ] Automatic trade mirroring for followers
- [ ] Track discovery and follow functionality

Dependencies: Automatic Investment Engine (#4)

Estimate: 21 story points
```

## Infrastructure Improvements (P2)

### Issue 9: Enhance Portfolio Analytics
```
Title: Enhance Portfolio Analytics Beyond Basic Tracking
Priority: Low
Labels: p2, analytics, future
Team: Backend
Epic: Future Enhancement

Description:
Implement advanced portfolio analytics beyond basic balance tracking for better user insights.

Requirements:
- Performance metrics calculation
- Risk analysis
- Historical tracking
- Reporting dashboards

Estimate: 13 story points
```

### Issue 10: Optimize APIs for Mobile App
```
Title: Optimize APIs for Mobile App Performance
Priority: Low
Labels: p2, mobile, api-optimization
Team: Backend
Epic: Future Enhancement

Description:
Create mobile-optimized API endpoints for better app performance and user experience.

Requirements:
- Batch API endpoints
- Reduced payload sizes
- Offline capability support
- Push notification integration

Estimate: 8 story points
```

## Epic Creation Commands

### Epic 1: MVP Critical Path
```
Title: MVP Critical Path - Core Automation Features
Description: Critical features required for MVP launch including automatic splitting, investment engine, and debit card system.
Issues: #1, #2, #3, #4, #5
Target Date: 6 weeks from start
```

### Epic 2: Post-MVP Enhancements
```
Title: Post-MVP Feature Enhancements
Description: Important features to ship shortly after MVP including physical cards and copy trading.
Issues: #6, #7, #8
Target Date: 3 months post-MVP
```

### Epic 3: Future Platform Improvements
```
Title: Future Platform Improvements
Description: Long-term platform improvements for scalability and user experience.
Issues: #9, #10
Target Date: 6 months post-MVP
```

## Implementation Timeline

**Week 1-2**: Issues #1, #2 (Apple Sign-In, 70/30 Split)
**Week 3-4**: Issues #3, #4 (Debit Card, Investment Engine)  
**Week 5-6**: Issue #5 (Fiat Virtual Accounts)
**Post-MVP**: Issues #6, #7, #8 (Physical Cards, KYC Optimization, Copy Trading)
**Future**: Issues #9, #10 (Analytics, Mobile Optimization)

## Notes for Linear Setup

1. Create the epics first, then assign issues to them
2. Set up proper labels for filtering (mvp, p0, p1, p2, etc.)
3. Assign story point estimates for sprint planning
4. Set dependencies between issues where noted
5. Use the provided acceptance criteria as issue requirements
6. Link issues to the MVP_MISSING_IMPLEMENTATIONS.md document for reference
