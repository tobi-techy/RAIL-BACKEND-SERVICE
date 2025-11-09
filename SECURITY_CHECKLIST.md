# Security Implementation Checklist

## Phase 1: Critical Fixes (Week 1)

### CSRF Protection
- [ ] Import CSRF middleware in routes
- [ ] Create CSRF store instance
- [ ] Apply to all POST routes in `routes.go`
- [ ] Apply to all PUT routes in `routes.go`
- [ ] Apply to all DELETE routes in `routes.go`
- [ ] Apply to all POST routes in `stack_routes.go`
- [ ] Apply to all PUT routes in `stack_routes.go`
- [ ] Apply to all DELETE routes in `stack_routes.go`
- [ ] Test CSRF protection with Postman/curl
- [ ] Update API documentation with CSRF requirements

### SQL Injection Fixes
- [ ] Fix `admin_handlers.go:327` - getAllUsers query
- [ ] Fix `admin_handlers.go:1213` - getAdminWallets query
- [ ] Fix `audit_service.go:610` - audit query
- [ ] Fix `wallet_repository.go:501` - wallet count query
- [ ] Fix `wallet_repository.go:518` - wallet list query
- [ ] Add unit tests for SQL injection prevention
- [ ] Code review of all SQL queries

### Environment Variables
- [ ] Copy `.env.security.example` to `.env`
- [ ] Generate all required secrets
- [ ] Update `config.go` to read from environment
- [ ] Remove hardcoded secrets from `config.go`
- [ ] Update deployment documentation
- [ ] Configure secrets in staging environment
- [ ] Configure secrets in production environment
- [ ] Verify `.env` in `.gitignore`

## Phase 2: High Priority (Week 2)

### XSS Prevention
- [ ] Add sanitize import to all handlers
- [ ] Sanitize output in `admin_handlers.go`
- [ ] Sanitize output in `alpaca_handlers.go`
- [ ] Sanitize output in `due_handlers.go`
- [ ] Sanitize output in `funding_investing_handlers.go`
- [ ] Sanitize output in `onboarding_handlers.go`
- [ ] Sanitize output in `stack_handlers.go`
- [ ] Sanitize output in `wallet_handlers.go`
- [ ] Sanitize output in `withdrawal_handlers.go`
- [ ] Add XSS tests
- [ ] Code review of all handlers

### Log Injection Prevention
- [ ] Add log sanitization to `admin_handlers.go`
- [ ] Add log sanitization to `kyc_provider.go`
- [ ] Add log sanitization to `onboarding/service.go`
- [ ] Add log sanitization to `alpaca/client.go`
- [ ] Add log sanitization to `due/client.go`
- [ ] Add log sanitization to `circle/client.go`
- [ ] Add log sanitization to all repositories
- [ ] Add log sanitization to all services
- [ ] Review all log statements
- [ ] Add log injection tests

### Path Traversal & File Permissions
- [ ] Fix path traversal in `database/database.go:69`
- [ ] Fix file permissions in `zerog/storage_client.go:416`
- [ ] Add path validation tests
- [ ] Add file permission tests
- [ ] Code review of all file operations

## Phase 3: Medium Priority (Week 3)

### Shell Script Error Handling
- [ ] Add `set -euo pipefail` to `test_wallet_api.sh`
- [ ] Add `set -euo pipefail` to `test_due_flow.sh`
- [ ] Add `set -euo pipefail` to `test_balance_api.sh`
- [ ] Add `set -euo pipefail` to `db_wipe.sh`
- [ ] Add `set -euo pipefail` to `db_reset.sh`
- [ ] Add `set -euo pipefail` to `clear_db.sh`
- [ ] Add `set -euo pipefail` to `clean_non_soldevnet_wallets.sh`
- [ ] Add `set -euo pipefail` to `run_circle_tests.sh`
- [ ] Add `set -euo pipefail` to all other scripts
- [ ] Test all scripts for proper error handling

### Test Files
- [ ] Update test files to use environment variables
- [ ] Create test environment configuration
- [ ] Update CI/CD pipeline for test secrets
- [ ] Document test setup in README
- [ ] Verify tests pass in CI/CD

### CI/CD Integration
- [ ] Add security audit to CI/CD pipeline
- [ ] Add gosec scan to CI/CD pipeline
- [ ] Add dependency vulnerability scan
- [ ] Configure failure thresholds
- [ ] Set up security alerts
- [ ] Document CI/CD security checks

## Phase 4: Validation (Week 4)

### Testing
- [ ] Run full test suite
- [ ] Run security audit script
- [ ] Run gosec scan
- [ ] Run dependency vulnerability scan
- [ ] Manual security testing
- [ ] Load testing with security features
- [ ] Performance testing

### Documentation
- [ ] Update README with security section
- [ ] Update API documentation
- [ ] Update deployment guide
- [ ] Update developer onboarding docs
- [ ] Create security runbook
- [ ] Document incident response process

### Security Review
- [ ] Internal code review
- [ ] Security team review
- [ ] Penetration testing
- [ ] Vulnerability assessment
- [ ] Compliance review
- [ ] Sign-off from security team

### Deployment
- [ ] Deploy to staging
- [ ] Smoke tests in staging
- [ ] Security validation in staging
- [ ] Deploy to production (gradual rollout)
- [ ] Monitor for issues
- [ ] Verify security controls in production

## Ongoing Maintenance

### Daily
- [ ] Monitor security alerts
- [ ] Review failed authentication attempts
- [ ] Check rate limit violations

### Weekly
- [ ] Run security audit script
- [ ] Review audit logs
- [ ] Check for new vulnerabilities

### Monthly
- [ ] Update dependencies
- [ ] Review access controls
- [ ] Security team meeting
- [ ] Update security documentation

### Quarterly
- [ ] Rotate secrets
- [ ] Penetration testing
- [ ] Security training
- [ ] Compliance audit

## Verification Commands

```bash
# Run security audit
bash scripts/security_audit.sh

# Run gosec
gosec ./...

# Run tests
go test ./...

# Check test coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Check dependencies
go list -json -m all | nancy sleuth

# Verify environment variables
env | grep -E "API_KEY|SECRET|PASSWORD" | wc -l
```

## Sign-off

### Phase 1 Complete
- [ ] Developer: _________________ Date: _______
- [ ] Code Reviewer: _____________ Date: _______
- [ ] Security Team: _____________ Date: _______

### Phase 2 Complete
- [ ] Developer: _________________ Date: _______
- [ ] Code Reviewer: _____________ Date: _______
- [ ] Security Team: _____________ Date: _______

### Phase 3 Complete
- [ ] Developer: _________________ Date: _______
- [ ] Code Reviewer: _____________ Date: _______
- [ ] Security Team: _____________ Date: _______

### Phase 4 Complete
- [ ] Developer: _________________ Date: _______
- [ ] Code Reviewer: _____________ Date: _______
- [ ] Security Team: _____________ Date: _______
- [ ] Product Owner: _____________ Date: _______

### Production Deployment Approved
- [ ] Security Team: _____________ Date: _______
- [ ] Engineering Lead: __________ Date: _______
- [ ] Product Owner: _____________ Date: _______

---

**Project:** STACK Service Security Remediation
**Start Date:** _____________
**Target Completion:** _____________
**Status:** In Progress
