# Information Security Policy

**Rail Technology Concept**
**Version:** 1.0
**Effective Date:** March 10, 2026
**Owner:** Engineering Lead

---

## 1. Purpose

This policy establishes the information security requirements for the Rail Money platform. It defines controls to protect the confidentiality, integrity, and availability of customer data, financial records, and system infrastructure.

---

## 2. Scope

This policy applies to:
- All Rail platform systems, APIs, and databases
- All personnel with access to production systems or customer data
- All third-party service providers processing Rail customer data

---

## 3. Data Classification

| Class | Description | Examples |
|-------|-------------|---------|
| **Critical** | Regulated financial and identity data | KYC documents, bank details, SSN/NIN, private keys |
| **Confidential** | Internal business data | API keys, internal configs, audit logs |
| **Internal** | Non-public operational data | System metrics, anonymised analytics |
| **Public** | Intentionally public | Marketing content, public API docs |

Critical and Confidential data must be encrypted at rest and in transit at all times.

---

## 4. Encryption Standards

### 4.1 Data at Rest
- All PII fields encrypted using **AES-256-GCM**
- Encryption keys stored separately from encrypted data
- Database-level encryption enabled on all PostgreSQL instances (Neon)

### 4.2 Data in Transit
- All external API communication uses **TLS 1.3** minimum
- Internal service communication uses mTLS where applicable
- HTTP is not permitted; all endpoints enforce HTTPS

### 4.3 Key Management
- Encryption keys rotated at minimum annually
- Keys never committed to source control
- Secrets managed via environment variables and secret management tooling

---

## 5. Access Control

### 5.1 Principle of Least Privilege
- All system accounts granted minimum permissions required for their function
- Production database access restricted to application service accounts only
- No direct production database access for developers without explicit approval

### 5.2 Authentication
- All user accounts require strong passwords (minimum 12 characters, complexity enforced)
- Two-factor authentication (2FA) available and encouraged for all users
- JWT access tokens expire after 15 minutes; refresh tokens after 7 days
- Passcode authentication available as secondary factor

### 5.3 API Security
- All authenticated endpoints require valid JWT Bearer token
- CSRF protection enforced via `X-Requested-With` header validation
- Rate limiting applied per IP and per user on all endpoints
- Webhook endpoints validate cryptographic signatures before processing

---

## 6. Infrastructure Security

### 6.1 Cloud Infrastructure
- Production hosted on SOC 2 Type II compliant infrastructure
- Database: Neon (PostgreSQL) with encryption at rest enabled
- Cache: Redis with authentication required
- No public-facing database ports; all DB access via private networking

### 6.2 Network Security
- All inbound traffic routed through reverse proxy / load balancer
- DDoS protection at network edge
- Outbound connections to third-party APIs (Bridge, Alpaca) use allowlisted endpoints only

### 6.3 Secrets Management
- No secrets committed to source control (enforced via pre-commit hooks and `.gitignore`)
- Environment-specific secrets stored in environment variables
- API keys rotated immediately upon suspected compromise

---

## 7. Application Security

### 7.1 Secure Development
- All code changes reviewed via pull request before merging to main
- Dependency vulnerability scanning on every build
- SQL injection prevented via parameterised queries (sqlx)
- Input validation on all API endpoints

### 7.2 Vulnerability Management
- Critical vulnerabilities patched within 24 hours of discovery
- High severity vulnerabilities patched within 7 days
- Dependency updates reviewed weekly

### 7.3 Logging and Monitoring
- All authentication events logged (login, logout, failed attempts, 2FA)
- All financial transactions logged with full audit trail
- All API requests logged with request ID, user ID, timestamp, and response code
- Logs retained for 90 days minimum; financial audit logs for 7 years
- Anomaly detection alerts configured for unusual access patterns

---

## 8. Third-Party Security

All third-party processors handling customer data must:
- Maintain SOC 2 Type II or equivalent certification
- Sign a Data Processing Agreement (DPA) with Rail
- Notify Rail within 72 hours of any security incident affecting Rail customer data

Current processors: Bridge Network, Alpaca Securities LLC, Twilio/SendGrid, Neon.

---

## 9. Incident Response

### 9.1 Classification
| Severity | Definition | Response Time |
|----------|-----------|---------------|
| P1 — Critical | Data breach, system compromise, financial fraud | Immediate (< 1 hour) |
| P2 — High | Service outage, suspected unauthorised access | < 4 hours |
| P3 — Medium | Degraded performance, non-critical vulnerability | < 24 hours |
| P4 — Low | Minor issues, informational alerts | < 7 days |

### 9.2 Response Steps
1. **Detect** — automated monitoring or manual report
2. **Contain** — isolate affected systems, revoke compromised credentials
3. **Assess** — determine scope and impact
4. **Notify** — inform affected users and regulators within required timeframes
5. **Remediate** — patch root cause
6. **Review** — post-incident review and policy update

### 9.3 Breach Notification
- Affected users notified within 72 hours of confirmed breach
- Regulatory notification per applicable law (NDPR for Nigeria, GDPR where applicable)

---

## 10. Business Continuity

- Database backups taken daily with 30-day retention
- Point-in-time recovery enabled on production database
- Recovery Time Objective (RTO): 4 hours
- Recovery Point Objective (RPO): 1 hour
- Runbook maintained at `docs/operations/RUNBOOK.md`

---

## 11. Policy Review

This policy is reviewed annually or following any significant security incident. The Engineering Lead is responsible for maintaining and updating this policy.

---

**Approved by:** Rail Technology Concept Engineering Lead
**Date:** March 10, 2026
