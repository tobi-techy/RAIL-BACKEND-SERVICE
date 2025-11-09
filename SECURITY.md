# Security Guide

## Overview

This document outlines the security measures implemented in the STACK service and provides guidelines for maintaining security.

## Security Features

### 1. Authentication & Authorization
- JWT-based authentication with access and refresh tokens
- Role-based access control (RBAC)
- Secure password hashing with bcrypt
- Token expiration and rotation

### 2. Input Validation & Sanitization
- Request size limits (10MB default)
- Content-type validation
- HTML escaping for XSS prevention
- Log injection prevention
- SQL injection prevention via parameterized queries

### 3. CSRF Protection
- Token-based CSRF protection for state-changing operations
- Automatic token generation and validation
- 1-hour token expiration

### 4. Rate Limiting
- Per-IP rate limiting (100 requests/minute default)
- Configurable burst allowance
- Protection against brute force attacks

### 5. Security Headers
- X-Content-Type-Options: nosniff
- X-Frame-Options: DENY
- X-XSS-Protection: 1; mode=block
- Strict-Transport-Security
- Content-Security-Policy
- Referrer-Policy

### 6. Data Protection
- AES-256-GCM encryption for sensitive data
- Encrypted storage of private keys
- Secure credential management via environment variables
- TLS/HTTPS enforcement

### 7. Audit Logging
- Comprehensive audit trail for sensitive operations
- Structured logging with request IDs
- Log sanitization to prevent injection

## Configuration

### Environment Variables

Copy `.env.security.example` to `.env` and configure:

```bash
cp .env.security.example .env
# Edit .env with your actual values
```

**Never commit `.env` to version control.**

### Generate Secrets

```bash
# Generate 32-byte hex key
openssl rand -hex 32

# Or using Python
python3 -c "import secrets; print(secrets.token_hex(32))"
```

## Security Checklist

### Before Deployment

- [ ] All secrets moved to environment variables
- [ ] HTTPS/TLS enabled
- [ ] CSRF protection enabled on all state-changing endpoints
- [ ] Rate limiting configured
- [ ] Security headers enabled
- [ ] Input validation on all endpoints
- [ ] SQL queries use parameterized statements
- [ ] File permissions set correctly (600 for sensitive files)
- [ ] Audit logging enabled
- [ ] Error messages don't leak sensitive information

### Regular Maintenance

- [ ] Review and rotate secrets quarterly
- [ ] Update dependencies for security patches
- [ ] Review audit logs for suspicious activity
- [ ] Run security audit script monthly
- [ ] Perform penetration testing annually
- [ ] Review and update access controls

## Security Audit

Run the security audit script:

```bash
./scripts/security_audit.sh
```

This checks for:
- Hardcoded secrets
- SQL injection vulnerabilities
- XSS vulnerabilities
- Missing CSRF protection
- Incorrect file permissions
- Missing error handling
- Security middleware configuration

## Vulnerability Reporting

If you discover a security vulnerability:

1. **DO NOT** open a public issue
2. Email security@stack.com with details
3. Include steps to reproduce
4. Allow 90 days for remediation before public disclosure

## Security Best Practices

### For Developers

#### Input Validation
```go
import "github.com/stack-service/stack_service/pkg/sanitize"

// Sanitize user input
email := sanitize.Email(req.Email)
name := sanitize.String(req.Name)

// Sanitize for logging
log.Info("User action", zap.String("email", sanitize.LogString(email)))
```

#### SQL Queries
```go
// Always use parameterized queries
query := "SELECT * FROM users WHERE email = $1 AND role = $2"
rows, err := db.QueryContext(ctx, query, email, role)

// For dynamic ORDER BY, use whitelist
import "github.com/stack-service/stack_service/pkg/database"
allowedColumns := []string{"created_at", "email", "name"}
orderClause := database.BuildOrderByClause(orderBy, allowedColumns)
```

#### Error Handling
```go
// Don't expose internal errors to clients
if err != nil {
    log.Error("Database error", zap.Error(err))
    c.JSON(http.StatusInternalServerError, gin.H{
        "error": "INTERNAL_ERROR",
        "message": "An error occurred processing your request",
    })
    return
}
```

#### Secrets Management
```go
// Load from environment
apiKey := os.Getenv("CIRCLE_API_KEY")
if apiKey == "" {
    log.Fatal("CIRCLE_API_KEY not set")
}

// Never log secrets
log.Info("API call", zap.String("endpoint", endpoint))
// NOT: log.Info("API call", zap.String("api_key", apiKey))
```

### For Operations

#### Deployment
- Use secrets management (AWS Secrets Manager, HashiCorp Vault)
- Enable TLS 1.3
- Configure firewall rules
- Use private networks for database connections
- Enable database encryption at rest
- Regular backups with encryption

#### Monitoring
- Set up alerts for:
  - Failed authentication attempts
  - Rate limit violations
  - Unusual API usage patterns
  - Database errors
  - High error rates

#### Incident Response
1. Identify and contain the threat
2. Assess the impact
3. Notify affected users if data breach
4. Remediate the vulnerability
5. Document lessons learned
6. Update security measures

## Compliance

### Data Protection
- GDPR compliance for EU users
- CCPA compliance for California users
- Data retention policies
- Right to deletion
- Data portability

### Financial Regulations
- KYC/AML compliance
- PCI DSS for payment data
- SOC 2 Type II certification
- Regular security audits

## Security Tools

### Static Analysis
```bash
# Install gosec
go install github.com/securego/gosec/v2/cmd/gosec@latest

# Run security scan
gosec ./...
```

### Dependency Scanning
```bash
# Check for vulnerable dependencies
go list -json -m all | nancy sleuth
```

### Code Review
- All code changes require review
- Security-sensitive changes require security team review
- Automated security checks in CI/CD

## Resources

- [OWASP Top 10](https://owasp.org/www-project-top-ten/)
- [Go Security Best Practices](https://github.com/OWASP/Go-SCP)
- [CWE Top 25](https://cwe.mitre.org/top25/)

## Updates

This document is reviewed and updated quarterly. Last update: 2024-01-01

## Contact

Security Team: security@stack.com
