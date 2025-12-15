# Rail Implementation Gap Analysis

**Date**: December 14, 2025  
**Comparison**: System Design vs Current Codebase

---

## Executive Summary

The codebase has strong foundational architecture but has **significant gaps** in implementing the core Rail MVP features defined in the PRD. The 70/30 split mechanism exists but is **opt-in rather than default**. Critical MVP features like the debit card and Station (home screen) are missing.

---

## ✅ Implemented (Aligned with Architecture)

### Core Infrastructure
| Component | Status | Location |
|-----------|--------|----------|
| Go/Gin Framework | ✅ Complete | `cmd/main.go`, `internal/api/routes/` |
| PostgreSQL + Migrations | ✅ Complete | `internal/infrastructure/database/`, `migrations/` |
| Redis Cache | ✅ Complete | `internal/infrastructure/cache/` |
| JWT Authentication | ✅ Complete | `pkg/auth/`, `internal/api/middleware/` |
| Circle Integration | ✅ Complete | `internal/infrastructure/circle/` |
| Alpaca Brokerage | ✅ Complete | `internal/adapters/alpaca/` |
| Due Network | ✅ Complete | `internal/adapters/due/` |

### Domain Services
| Service | Status | Notes |
|---------|--------|-------|
| Onboarding Service | ✅ Complete | KYC, wallet provisioning |
| Wallet Service | ✅ Complete | Multi-chain support |
| Funding Service | ✅ Complete | Deposits, webhooks |
| Investing Service | ✅ Complete | Baskets, orders, portfolio |
| Ledger Service | ✅ Complete | Double-entry accounting |
| Allocation Service | ✅ Complete | 70/30 split logic exists |

### Background Workers
| Worker | Status | Location |
|--------|--------|----------|
| Wallet Provisioning | ✅ Complete | `internal/workers/wallet_provisioning/` |
| Funding Webhook | ✅ Complete | `internal/workers/funding_webhook/` |
| Onboarding Processor | ✅ Complete | `internal/workers/onboarding_processor/` |
| Scheduled Investment | ✅ Complete | `internal/workers/scheduled_investment_worker/` |

### Security
| Feature | Status |
|---------|--------|
| AES-256-GCM Encryption | ✅ |
| Rate Limiting | ✅ |
| CORS Protection | ✅ |
| Input Validation | ✅ |
| Audit Logging | ✅ |

---

## ⚠️ Partially Implemented

### 1. 70/30 Split Engine
**Current State**: Implemented but **OPT-IN**  
**PRD Requirement**: **ALWAYS ON, NON-NEGOTIABLE**

```
Location: internal/domain/services/allocation/service.go
Issue: Users must explicitly enable allocation mode
```

**Gap**:
- Split is triggered only when `SmartAllocationMode.IsActive = true`
- PRD states: "This rule is system-defined, always on in MVP, not user-editable"
- Current flow allows users to bypass the split entirely

**Fix Required**:
- Make 70/30 split the DEFAULT for all new users
- Remove ability to disable in MVP
- Apply split automatically on every deposit

### 2. Round-ups
**Current State**: Implemented but requires card integration  
**PRD Requirement**: Simple ON/OFF toggle

```
Location: internal/domain/services/roundup/service.go
         internal/api/handlers/roundup_handlers.go
```

**Gap**:
- Round-up service exists
- No card transaction source to trigger round-ups
- Currently accepts manual/bank sources only

### 3. Apple Sign-In
**Current State**: Configuration exists, implementation partial

```
Location: internal/domain/services/socialauth/service.go
         internal/infrastructure/config/config.go
```

**Gap**:
- OAuth config for Apple exists
- Full Apple Sign-In flow needs verification
- PRD requires Apple Sign-In as PRIMARY auth method

---

## ❌ Missing (Critical MVP Gaps)

### 1. Debit Card Service
**PRD Requirement**: Virtual debit card at launch, linked to Spend Balance

**Missing Components**:
- [ ] Card issuer integration (no adapter exists)
- [ ] Virtual card creation endpoint
- [ ] Card authorization handler
- [ ] Card-to-Spend-Balance linking
- [ ] Card freeze/unfreeze functionality

**Required Files**:
```
internal/adapters/card_issuer/client.go
internal/domain/services/card/service.go
internal/api/handlers/card_handlers.go
internal/api/routes/card_routes.go
migrations/XXX_create_cards_table.up.sql
```

### 2. Station (Home Screen) API
**PRD Requirement**: Single endpoint returning total balance, spend/invest split, system status

**Missing**:
- [ ] `/api/v1/account/station` endpoint
- [ ] Aggregated balance response
- [ ] System status (Allocating/Active/Paused)

**Required Response Format**:
```json
{
  "total_balance": "1000.00",
  "spend_balance": "700.00",
  "invest_balance": "300.00",
  "system_status": "active"
}
```

### 3. Spend Balance as Primary Surface
**PRD Requirement**: Spend balance must feel like checking account replacement

**Missing**:
- [ ] Dedicated spend balance endpoint
- [ ] Real-time spend balance updates
- [ ] Spend transaction history (separate from invest)

### 4. Automatic Investment Engine (Auto-Deploy)
**PRD Requirement**: 30% deploys automatically with NO user interaction

**Current State**: Investment requires user to select baskets and place orders

**Missing**:
- [ ] Auto-allocation strategy selector
- [ ] Automatic trade execution on deposit
- [ ] Global fallback strategy
- [ ] No-confirmation trade flow

### 5. System Status State Machine
**PRD Requirement**: Display Allocating/Active/Paused status

**Missing**:
- [ ] System status entity
- [ ] Status transition logic
- [ ] Status in API responses

---

## 📋 Implementation Priority (MVP)

### Tier 0 - Must Ship (Blocking MVP)

| Priority | Feature | Effort | Dependencies |
|----------|---------|--------|--------------|
| P0.1 | Make 70/30 split DEFAULT | 2 days | None |
| P0.2 | Station API endpoint | 1 day | Allocation service |
| P0.3 | Virtual Debit Card integration | 2 weeks | Card issuer partner |
| P0.4 | Auto-invest on deposit | 3 days | Allocation + Investing |
| P0.5 | System status state machine | 2 days | None |

### Tier 1 - Important (Post-MVP)

| Priority | Feature | Effort |
|----------|---------|--------|
| P1.1 | Physical card shipping | 2 weeks |
| P1.2 | Card round-ups integration | 3 days |
| P1.3 | Push notifications | 1 week |
| P1.4 | Transaction categorization | 1 week |

---

## 🔧 Recommended Actions

### Immediate (This Sprint)

1. **Modify Allocation Service**
   ```go
   // In onboarding completion, auto-enable allocation mode
   func (s *OnboardingService) CompleteOnboarding(ctx context.Context, userID uuid.UUID) error {
       // ... existing logic ...
       
       // Auto-enable 70/30 allocation mode (MVP default)
       ratios := entities.AllocationRatios{
           SpendingRatio: decimal.NewFromFloat(0.70),
           StashRatio:    decimal.NewFromFloat(0.30),
       }
       if err := s.allocationService.EnableMode(ctx, userID, ratios); err != nil {
           return fmt.Errorf("failed to enable allocation mode: %w", err)
       }
       
       return nil
   }
   ```

2. **Add Station Endpoint**
   ```go
   // GET /api/v1/account/station
   type StationResponse struct {
       TotalBalance  string `json:"total_balance"`
       SpendBalance  string `json:"spend_balance"`
       InvestBalance string `json:"invest_balance"`
       SystemStatus  string `json:"system_status"` // allocating, active, paused
   }
   ```

3. **Auto-Invest Trigger**
   - Modify `funding_webhook/processor.go` to trigger auto-invest after 30% allocation
   - Use default strategy basket for automatic deployment

### Short-term (Next 2 Sprints)

4. **Card Issuer Integration**
   - Select card issuer partner (Marqeta, Stripe Issuing, etc.)
   - Implement adapter pattern similar to Alpaca/Due
   - Create card management endpoints

5. **Connect Round-ups to Cards**
   - Hook card authorization webhook to round-up service
   - Calculate and queue round-ups automatically

---

## Architecture Alignment Score

| Category | Score | Notes |
|----------|-------|-------|
| Infrastructure | 95% | Excellent foundation |
| Domain Services | 80% | Core services exist, need MVP adjustments |
| API Endpoints | 70% | Missing Station, Card endpoints |
| MVP Features | 50% | 70/30 opt-in, no cards, no auto-invest |
| **Overall** | **72%** | Strong base, needs MVP feature completion |

---

## Conclusion

The codebase has a **solid architectural foundation** that aligns well with the system design. However, the implementation diverges from the PRD's core philosophy:

> "Depositing funds equals consent to system behavior"

Currently, users must opt-in to the 70/30 split, which contradicts the MVP's core principle. The highest priority fix is making the allocation mode **default and non-optional**.

The debit card integration is the largest missing piece and will require a partner selection decision before implementation can begin.
