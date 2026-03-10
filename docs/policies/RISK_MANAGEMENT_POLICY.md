# Risk Management Policy

**Rail Technology Concept**
**Version:** 1.0
**Effective Date:** March 10, 2026
**Owner:** Engineering Lead / Compliance

---

## 1. Purpose

This policy establishes Rail's framework for identifying, assessing, mitigating, and monitoring risks associated with operating a technology-enabled financial services platform. It covers operational, financial, regulatory, and technology risks.

---

## 2. Scope

This policy applies to all aspects of the Rail Money platform, including:
- Technology infrastructure and software systems
- Financial operations (deposits, allocations, investments, withdrawals)
- Customer onboarding and KYC/AML processes
- Third-party service provider relationships
- Personnel with access to systems or customer data

---

## 3. Risk Categories

### 3.1 Operational Risk
Risk of loss from inadequate or failed internal processes, systems, or external events.

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|-----------|
| System outage | Medium | High | Redundant infrastructure, health checks, runbook |
| Data loss | Low | Critical | Daily backups, point-in-time recovery, 30-day retention |
| Webhook processing failure | Medium | High | Idempotent processing, retry queues, reconciliation worker |
| Deposit not allocated | Low | High | Deposit allocation recovery worker (15s polling, 24h window) |
| Third-party API downtime (Bridge/Alpaca) | Medium | High | Circuit breakers, graceful degradation, error logging |

### 3.2 Financial Risk
Risk of financial loss to customers or the business.

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|-----------|
| Double-processing a deposit | Low | Critical | Idempotency keys on all deposit records; unique DB constraints |
| Incorrect 70/30 split | Very Low | High | Decimal arithmetic (shopspring/decimal), automated tests |
| Unauthorised withdrawal | Low | Critical | KYC gate, withdrawal cooling period, 2FA for large amounts |
| Insufficient Alpaca buying power | Medium | Medium | Journal transfer before order placement; pre-flight balance check |
| Order execution failure | Medium | Medium | Fallback to SPY; error logged; stash balance preserved |

### 3.3 Regulatory / Compliance Risk
Risk of violating applicable laws, regulations, or licensing requirements.

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|-----------|
| KYC/AML non-compliance | Low | Critical | Bridge Network KYC; no funding without active KYC status |
| Operating without required licences | Medium | Critical | Technology Partner model via Alpaca (licensed broker); Bridge for custody |
| Data protection violation (NDPR/GDPR) | Low | High | Privacy Policy, data minimisation, encryption, retention limits |
| Sanctions screening failure | Low | Critical | Bridge Network handles OFAC/sanctions screening |
| Undisclosed investment risk | Low | High | No investment advice language; 70/30 split disclosed pre-deposit |

### 3.4 Technology / Cybersecurity Risk
Risk from system vulnerabilities, attacks, or failures.

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|-----------|
| API credential compromise | Low | Critical | Secrets stored in environment variables (never committed to version control); immediate rotation on compromise; pre-commit hooks enforce secret detection |
| Webhook spoofing | Low | High | Cryptographic signature verification on all webhooks |
| SQL injection | Very Low | Critical | Parameterised queries (sqlx); no raw SQL string interpolation |
| Brute force / credential stuffing | Medium | High | Rate limiting; account lockout; 2FA |
| DDoS attack | Low | Medium | Network-edge protection; rate limiting middleware |
| Dependency vulnerability | Medium | Medium | Weekly dependency review; automated scanning |

### 3.5 Third-Party / Concentration Risk
Risk from over-reliance on specific service providers.

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|-----------|
| Bridge Network outage | Low | High | Circuit breakers; graceful error handling; user notification |
| Alpaca outage | Low | High | Orders queued; stash balance preserved; retry on recovery |
| Alpaca licence revocation | Very Low | Critical | Monitor regulatory status; contingency plan for alternative broker |
| Bridge licence revocation | Very Low | Critical | Monitor regulatory status; customer funds held in custody |

---

## 4. Risk Assessment Process

### 4.1 Risk Scoring
Risks are scored on two dimensions:

**Likelihood:** Very Low (1) / Low (2) / Medium (3) / High (4) / Very High (5)
**Impact:** Low (1) / Medium (2) / High (3) / Critical (4)

**Risk Score = Likelihood × Impact**

| Score | Rating | Action Required |
|-------|--------|----------------|
| 1–4 | Low | Monitor; review annually |
| 5–8 | Medium | Mitigate; review quarterly |
| 9–12 | High | Immediate mitigation; escalate |
| 13–20 | Critical | Stop operations until resolved |

### 4.2 Risk Review Cadence
- **Monthly:** Review of operational incidents and near-misses
- **Quarterly:** Full risk register review; update mitigations
- **Annually:** Complete policy review and risk reassessment
- **Ad hoc:** Following any significant incident, regulatory change, or new product feature

---

## 5. Key Controls

### 5.1 Financial Controls
- All deposit processing is idempotent — duplicate webhooks cannot create duplicate records
- Ledger uses double-entry bookkeeping — every credit has a corresponding debit
- Allocation split uses exact decimal arithmetic — no floating-point rounding errors
- Withdrawal limits enforced per user tier and regulatory requirements
- Reconciliation worker runs every 10 minutes to detect and recover stuck deposits

### 5.2 Operational Controls
- Health check endpoints monitored continuously (`/health`, `/health/ready`, `/health/live`)
- All financial transactions logged with full audit trail
- Wallet provisioning scheduler retries failed jobs automatically
- KYC sync worker polls for status updates every 5 seconds

### 5.3 Access Controls
- Production access restricted to authorised personnel only
- All access events logged
- API keys rotated on a defined schedule or immediately upon suspected compromise

---

## 6. Regulatory Compliance Framework

Rail operates as a **Technology Partner** under Alpaca Securities LLC's broker-dealer licence. Under this model:

- **Alpaca Securities LLC** is the licensed broker-dealer responsible for trade execution, custody of securities, and regulatory reporting to FINRA/SEC
- **Bridge Network** is the licensed money transmitter responsible for fiat-to-crypto conversion, virtual account management, and OFAC screening
- **Rail Technology Concept** provides the technology layer connecting users to these licensed services

Rail does not hold customer funds directly. All customer assets are held by licensed custodians (Bridge for crypto/fiat, Alpaca for securities).

---

## 7. Business Continuity and Disaster Recovery

| Scenario | RTO | RPO | Response |
|----------|-----|-----|---------|
| Application server failure | 15 min | 0 | Auto-restart; health check triggers alert |
| Database failure | 4 hours | 1 hour | Restore from backup; point-in-time recovery |
| Third-party API outage | N/A | N/A | Circuit breaker; queue for retry; user notification |
| Complete infrastructure loss | 24 hours | 1 hour | Redeploy from IaC; restore from backup |

Detailed recovery procedures are documented in `docs/operations/RUNBOOK.md`.

---

## 8. Incident Reporting

All risk events must be documented including:
- Date and time of discovery
- Nature and scope of the event
- Immediate actions taken
- Root cause analysis
- Remediation steps and timeline
- Lessons learned

Incidents affecting customer funds or data must be escalated immediately to the Engineering Lead and reported to relevant regulators within required timeframes.

---

## 9. Policy Ownership and Review

| Role | Responsibility |
|------|---------------|
| Engineering Lead | Technical risk identification and mitigation |
| Compliance | Regulatory risk monitoring and reporting |
| All staff | Reporting identified risks promptly |

This policy is reviewed annually and updated following any material change to the business, technology stack, or regulatory environment.

---

**Approved by:** Rail Technology Concept
**Date:** March 10, 2026
**Next Review:** March 10, 2027
