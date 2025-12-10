# RAIL Security Incident Response Playbook

## Overview

This document outlines the procedures for detecting, responding to, and recovering from security incidents in the RAIL platform.

## Incident Severity Levels

| Level | Description | Response Time | Examples |
|-------|-------------|---------------|----------|
| **Critical** | Immediate threat to user funds or data | < 15 minutes | Active breach, data exfiltration, account takeover in progress |
| **High** | Significant security risk | < 1 hour | Credential stuffing attack, suspicious admin access |
| **Medium** | Potential security concern | < 4 hours | Multiple failed logins, unusual transaction patterns |
| **Low** | Minor security event | < 24 hours | Single failed login, minor policy violation |

## Automated Detection

The system automatically detects and responds to the following incidents:

### 1. Credential Stuffing / Brute Force Attack

**Detection Criteria:**
- >20 failed login attempts from single IP in 1 hour
- >5 unique users targeted from single IP in 1 hour

**Automated Response:**
1. Block source IP for 24 hours
2. Enable enhanced monitoring
3. Alert security team
4. Create incident record

**Manual Follow-up:**
- Review blocked IP for false positives
- Check if any accounts were compromised
- Update IP reputation database

### 2. Account Takeover

**Detection Criteria:**
- High fraud score (>0.8) combined with:
  - Recent password change
  - New device
  - Unusual location

**Automated Response:**
1. Lock affected account
2. Revoke all active sessions
3. Force password reset
4. Freeze withdrawals
5. Notify user via alternate channel (SMS/email)

**Manual Follow-up:**
- Contact user to verify identity
- Review recent account activity
- Restore access after verification
- Document incident details

### 3. Fraud Detection

**Detection Criteria:**
- Transaction fraud score >0.7
- Velocity anomalies
- Amount anomalies
- Device/location anomalies

**Automated Response:**
1. Freeze account transactions
2. Flag for manual review
3. Collect evidence (logs, patterns)

**Manual Follow-up:**
- Review flagged transactions
- Contact user if needed
- Approve or reject transactions
- Update fraud models

## Manual Incident Response Procedures

### Phase 1: Detection & Triage (0-15 minutes)

1. **Acknowledge Alert**
   - Log into security dashboard
   - Review incident details
   - Assess severity level

2. **Initial Assessment**
   - Identify affected systems/users
   - Determine scope of impact
   - Check for ongoing activity

3. **Escalation**
   - Critical/High: Notify security lead immediately
   - Medium: Notify within 1 hour
   - Low: Handle during business hours

### Phase 2: Containment (15-60 minutes)

1. **Isolate Affected Systems**
   ```bash
   # Block suspicious IP
   curl -X POST /api/v1/admin/security/blocked-ips \
     -d '{"ip": "x.x.x.x", "duration": "24h", "reason": "Incident #123"}'
   ```

2. **Preserve Evidence**
   - Export relevant logs
   - Take database snapshots
   - Document timeline

3. **Limit Damage**
   - Disable compromised accounts
   - Revoke suspicious sessions
   - Freeze high-risk transactions

### Phase 3: Eradication (1-4 hours)

1. **Remove Threat**
   - Reset compromised credentials
   - Patch vulnerabilities
   - Update security rules

2. **Verify Removal**
   - Scan for persistence mechanisms
   - Check for backdoors
   - Validate system integrity

### Phase 4: Recovery (4-24 hours)

1. **Restore Services**
   - Re-enable affected accounts
   - Unfreeze transactions
   - Remove temporary blocks

2. **Monitor**
   - Enhanced logging for 72 hours
   - Watch for recurrence
   - Track user reports

### Phase 5: Post-Incident (24-72 hours)

1. **Documentation**
   - Complete incident report
   - Update runbooks
   - Document lessons learned

2. **Communication**
   - Notify affected users (if required)
   - Update stakeholders
   - File regulatory reports (if required)

3. **Improvement**
   - Update detection rules
   - Enhance monitoring
   - Schedule security review

## API Endpoints for Incident Response

### View Security Dashboard
```bash
GET /api/v1/admin/security/dashboard
```

### List Open Incidents
```bash
GET /api/v1/admin/security/incidents
```

### Get Incident Details
```bash
GET /api/v1/admin/security/incidents/{id}
```

### Update Incident Status
```bash
PUT /api/v1/admin/security/incidents/{id}/status
{
  "status": "investigating|contained|resolved|false_positive",
  "notes": "Investigation notes..."
}
```

### Execute Automated Playbook
```bash
POST /api/v1/admin/security/incidents/{id}/playbook
```

### Block IP Address
```bash
POST /api/v1/admin/security/blocked-ips
{
  "ip": "x.x.x.x",
  "duration": "24h",
  "reason": "Incident response"
}
```

### Lock User Account
```bash
POST /api/v1/admin/users/{id}/lock
{
  "reason": "Security incident",
  "incident_id": "uuid"
}
```

### Revoke User Sessions
```bash
POST /api/v1/admin/users/{id}/revoke-sessions
```

## Contact Information

| Role | Contact | Escalation Time |
|------|---------|-----------------|
| Security On-Call | security-oncall@rail.app | Immediate |
| Security Lead | security-lead@rail.app | 15 minutes |
| Engineering Lead | eng-lead@rail.app | 30 minutes |
| Legal/Compliance | legal@rail.app | 1 hour |

## Regulatory Reporting Requirements

### Data Breach Notification
- **GDPR**: 72 hours to supervisory authority
- **CCPA**: "Without unreasonable delay"
- **State Laws**: Varies by jurisdiction

### Financial Reporting
- **FinCEN SAR**: Within 30 days of detection
- **State Money Transmitter**: Per license requirements

## Security Drill Schedule

| Drill Type | Frequency | Last Conducted | Next Scheduled |
|------------|-----------|----------------|----------------|
| Tabletop Exercise | Quarterly | - | TBD |
| Red Team Exercise | Annually | - | TBD |
| Phishing Simulation | Monthly | - | TBD |
| Incident Response Drill | Quarterly | - | TBD |

## Appendix: Incident Classification

### Incident Types
- `breach_attempt` - Attempted unauthorized access
- `account_takeover` - Compromised user account
- `fraud` - Fraudulent transaction activity
- `data_leak` - Unauthorized data exposure
- `ddos` - Denial of service attack
- `malware` - Malicious software detected
- `unauthorized_access` - Unauthorized system access
- `suspicious_activity` - Unclassified suspicious behavior

### Response Actions
- `block_ip` - Block IP address
- `lock_account` - Lock user account
- `revoke_sessions` - Revoke all user sessions
- `force_password_reset` - Require password change
- `freeze_withdrawals` - Freeze withdrawal capability
- `freeze_transactions` - Freeze all transactions
- `notify_user` - Send security notification
- `notify_user_alternate` - Notify via alternate channel
- `alert_security_team` - Alert security personnel
- `enable_monitoring` - Enable enhanced monitoring
- `collect_evidence` - Gather forensic evidence
- `flag_for_review` - Flag for manual review
