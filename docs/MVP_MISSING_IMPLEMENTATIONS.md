# Rail MVP - Missing Implementations

**Document Version**: 1.0  
**Last Updated**: December 15, 2025  
**Status**: Active  

## Overview

This document outlines all missing implementations required to achieve MVP status for Rail. Each item has been analyzed against the MVP epics and represents a critical gap that must be addressed before launch.

---

## Critical MVP Blockers (P0 - Must Ship)

### 1. Apple Sign-In Integration
**Epic**: 1.1 - User Onboarding & Authentication  
**Priority**: P0  
**Status**: Not Implemented  

**Description**: Primary authentication method for iOS users. Social auth framework exists but Apple-specific implementation missing.

**Requirements**:
- Apple Sign-In SDK integration
- Apple ID token validation
- User profile creation from Apple ID
- Fallback to email/phone for non-Apple users

**Acceptance Criteria**:
- [ ] Users can sign up using Apple Sign-In
- [ ] Apple ID tokens are validated server-side
- [ ] User accounts created automatically from Apple profile
- [ ] Seamless onboarding flow < 2 minutes

---

### 2. Automatic 70/30 Split Engine
**Epic**: 2.4 - Funding & Deposits  
**Priority**: P0  
**Status**: Not Implemented  

**Description**: Core system rule - every deposit must automatically split 70% to Spend Balance, 30% to Invest Balance without user interaction.

**Requirements**:
- Deposit webhook triggers automatic split
- 70% credited to Spend Balance
- 30% credited to Invest Balance
- Real-time balance updates
- No user configuration options

**Acceptance Criteria**:
- [ ] All deposits automatically split 70/30
- [ ] Split occurs within 60 seconds of deposit confirmation
- [ ] No user settings or allocation choices shown
- [ ] Balance updates reflect split immediately

---

### 3. Virtual Debit Card System
**Epic**: 4.1-4.3 - Debit Card  
**Priority**: P0  
**Status**: Not Implemented  

**Description**: Virtual debit card issued immediately upon first funding, linked directly to Spend Balance.

**Requirements**:
- Virtual card issuance API integration
- Real-time authorization against Spend Balance
- Card details (number, CVV, expiry) generation
- Transaction processing and balance deduction

**Acceptance Criteria**:
- [ ] Virtual card issued on first funding
- [ ] Card transactions deduct from Spend Balance in real-time
- [ ] Authorization fails when insufficient balance
- [ ] Card details accessible in app

---

### 4. Automatic Investment Engine
**Epic**: 5.1-5.3 - Automatic Investing Engine  
**Priority**: P0  
**Status**: Partially Implemented  

**Description**: Automatically deploy 30% invest balance without user interaction. Alpaca integration exists but auto-deployment missing.

**Requirements**:
- Auto-deployment of invest balance funds
- Global fallback investment strategy
- Integration with existing Alpaca service
- No trade confirmations or asset visibility
- Position tracking (backend only)

**Acceptance Criteria**:
- [ ] 30% of deposits auto-invested without user action
- [ ] Default investment strategy for all users
- [ ] No individual trades or assets shown to users
- [ ] Positions tracked in backend systems

---

### 5. USD/GBP Virtual Accounts
**Epic**: 2.1 - Funding & Deposits  
**Priority**: P0  
**Status**: Partially Implemented  

**Description**: Dedicated virtual accounts for fiat bank transfers. Due Network integration exists but fiat setup missing.

**Requirements**:
- USD virtual account creation via Due Network
- GBP virtual account creation via Due Network
- Bank transfer routing and processing
- Deposit confirmation and crediting

**Acceptance Criteria**:
- [ ] Users receive unique USD virtual account details
- [ ] Users receive unique GBP virtual account details
- [ ] Bank transfers credited within standard timeframes
- [ ] Automatic 70/30 split triggered on fiat deposits

---

## Important Features (P1 - Ship Shortly After MVP)

### 6. Physical Debit Card
**Epic**: 4.4 - Debit Card  
**Priority**: P1  
**Status**: Not Implemented  

**Description**: Physical card shipping and management for users who request it.

**Requirements**:
- Physical card ordering system
- Shipping address management
- Card activation flow
- Link to existing virtual card system

---

### 7. Enhanced KYC Flow Optimization
**Epic**: 1.3 - User Onboarding & Authentication  
**Priority**: P1  
**Status**: Needs Optimization  

**Description**: Current KYC exists but needs optimization for < 2 minute onboarding target.

**Requirements**:
- Streamlined document upload
- Faster verification processing
- Reduced friction points
- Progress indicators

---

### 8. Copy Trading System (Conductors)
**Epic**: 8.1-8.9 - Copy Trading  
**Priority**: P1 (Post-MVP)  
**Status**: Not Implemented  

**Description**: Allow users to follow professional investors and mirror their portfolios.

**Requirements**:
- Conductor application system
- Track creation and management
- Copy engine for trade mirroring
- Discovery and follow system

---

## Infrastructure Improvements (P2 - Future)

### 9. Advanced Portfolio Analytics
**Epic**: Future Enhancement  
**Priority**: P2  
**Status**: Basic Implementation  

**Description**: Enhanced analytics beyond basic balance tracking.

**Requirements**:
- Performance metrics calculation
- Risk analysis
- Historical tracking
- Reporting dashboards

---

### 10. Mobile App API Optimization
**Epic**: Future Enhancement  
**Priority**: P2  
**Status**: Needs Mobile-Specific Endpoints  

**Description**: Mobile-optimized API endpoints for better app performance.

**Requirements**:
- Batch API endpoints
- Reduced payload sizes
- Offline capability support
- Push notification integration

---

## Implementation Priority Matrix

| Feature | Priority | Effort | Impact | Dependencies |
|---------|----------|--------|--------|--------------|
| Apple Sign-In | P0 | Medium | High | None |
| 70/30 Split Engine | P0 | Medium | Critical | Deposit webhooks |
| Virtual Debit Card | P0 | High | Critical | Card provider integration |
| Auto Investment Engine | P0 | Medium | Critical | Alpaca integration |
| USD/GBP Virtual Accounts | P0 | Medium | High | Due Network setup |
| Physical Debit Card | P1 | High | Medium | Virtual card system |
| KYC Optimization | P1 | Low | Medium | Current KYC system |
| Copy Trading | P1 | Very High | High | Investment engine |

---

## Success Metrics

### MVP Launch Criteria
- [ ] User can sign up with Apple ID in < 2 minutes
- [ ] All deposits automatically split 70/30
- [ ] Virtual debit card works for spending
- [ ] 30% invest balance auto-deployed
- [ ] Fiat deposits supported (USD/GBP)

### Post-MVP Metrics
- [ ] Physical card ordering available
- [ ] Copy trading system operational
- [ ] Advanced analytics accessible

---

## Technical Dependencies

### External Integrations Required
1. **Apple Sign-In SDK** - iOS authentication
2. **Card Issuer API** - Virtual/physical card management
3. **Due Network Fiat Setup** - USD/GBP virtual accounts
4. **Investment Strategy Definition** - Default allocation rules

### Internal Dependencies
1. **Deposit Webhook Enhancement** - Trigger automatic split
2. **Balance Service Integration** - Real-time updates
3. **Alpaca Service Enhancement** - Auto-investment triggers
4. **Notification System** - User communication

---

## Risk Assessment

### High Risk Items
- **Debit Card Integration**: Complex regulatory and technical requirements
- **Auto Investment Engine**: Financial regulations and compliance
- **Apple Sign-In**: iOS ecosystem dependencies

### Medium Risk Items
- **Fiat Virtual Accounts**: Banking partner integration complexity
- **70/30 Split Engine**: Real-time processing requirements

### Low Risk Items
- **KYC Optimization**: Incremental improvements to existing system
- **Copy Trading**: Post-MVP feature with flexible timeline

---

## Next Steps

1. **Immediate (Week 1-2)**:
   - Apple Sign-In implementation
   - 70/30 split engine development

2. **Short Term (Week 3-4)**:
   - Virtual debit card integration
   - Auto investment engine completion

3. **Medium Term (Week 5-6)**:
   - USD/GBP virtual account setup
   - System integration testing

4. **Long Term (Post-MVP)**:
   - Physical card system
   - Copy trading implementation
   - Advanced analytics

---

**Document Prepared By**: System Analysis  
**Review Required By**: Product & Engineering Teams  
**Implementation Timeline**: 5-7 weeks to MVP
