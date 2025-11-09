# Security Quick Reference

## Common Security Patterns

### 1. Input Sanitization

```go
import "github.com/stack-service/stack_service/pkg/sanitize"

// HTML escape for XSS prevention
safeOutput := sanitize.String(userInput)

// Log sanitization (remove newlines)
log.Info("Action", zap.String("input", sanitize.LogString(userInput)))

// Email normalization
email := sanitize.Email(req.Email)

// Alphanumeric only
username := sanitize.AlphaNumeric(req.Username)
```

### 2. SQL Injection Prevention

```go
// ✅ CORRECT - Parameterized query
query := "SELECT * FROM users WHERE email = $1 AND role = $2"
rows, err := db.QueryContext(ctx, query, email, role)

// ❌ WRONG - String concatenation
query := "SELECT * FROM users WHERE email = '" + email + "'"

// ❌ WRONG - fmt.Sprintf
query := fmt.Sprintf("SELECT * FROM users WHERE email = '%s'", email)
```

### 3. Dynamic ORDER BY (Safe)

```go
import "github.com/stack-service/stack_service/pkg/database"

allowedColumns := []string{"created_at", "email", "name"}
orderClause := database.BuildOrderByClause(orderBy, allowedColumns)
query := "SELECT * FROM users" + orderClause
```

### 4. CSRF Protection

```go
// In routes setup
csrfStore := middleware.NewCSRFStore()

// Apply to state-changing routes
protected := router.Group("/api/v1")
protected.Use(middleware.CSRFProtection(csrfStore))
protected.POST("/resource", handler)
protected.PUT("/resource/:id", handler)
protected.DELETE("/resource/:id", handler)

// GET requests don't need CSRF
router.GET("/resource", handler)
```

### 5. Error Handling

```go
// ✅ CORRECT - Don't expose internals
if err != nil {
    log.Error("Database error", zap.Error(err), zap.String("user_id", userID.String()))
    c.JSON(http.StatusInternalServerError, gin.H{
        "error": "INTERNAL_ERROR",
        "message": "An error occurred",
    })
    return
}

// ❌ WRONG - Exposes internal details
if err != nil {
    c.JSON(http.StatusInternalServerError, gin.H{
        "error": err.Error(), // Don't do this!
    })
}
```

### 6. Secrets Management

```go
// ✅ CORRECT - Environment variables
apiKey := os.Getenv("API_KEY")
if apiKey == "" {
    log.Fatal("API_KEY not set")
}

// ❌ WRONG - Hardcoded
apiKey := "sk_live_abc123xyz"

// ✅ CORRECT - Don't log secrets
log.Info("API call", zap.String("endpoint", endpoint))

// ❌ WRONG - Logging secrets
log.Info("API call", zap.String("api_key", apiKey))
```

### 7. File Operations

```go
import "path/filepath"

// ✅ CORRECT - Validate paths
cleanPath := filepath.Clean(userPath)
if !strings.HasPrefix(cleanPath, allowedDir) {
    return errors.New("invalid path")
}

// ✅ CORRECT - Secure permissions
err := os.WriteFile(path, data, 0600) // For sensitive files
err := os.WriteFile(path, data, 0644) // For regular files
```

### 8. Authentication Middleware

```go
// Require authentication
protected := router.Group("/api/v1")
protected.Use(middleware.Authentication(cfg, log))
protected.GET("/profile", handler)

// Require admin role
admin := router.Group("/api/v1/admin")
admin.Use(middleware.Authentication(cfg, log))
admin.Use(middleware.AdminAuth(db, log))
admin.GET("/users", handler)
```

### 9. Rate Limiting

```go
// Apply rate limiting
router.Use(middleware.RateLimit(100)) // 100 requests per minute
```

### 10. Security Headers

```go
// Apply security headers to all routes
router.Use(middleware.SecurityHeaders())
```

## Common Vulnerabilities to Avoid

### SQL Injection
- ❌ String concatenation in queries
- ❌ fmt.Sprintf with user input
- ✅ Always use parameterized queries ($1, $2, etc.)

### XSS (Cross-Site Scripting)
- ❌ Returning user input directly in JSON
- ❌ Rendering user input in HTML without escaping
- ✅ Sanitize all user input before output

### CSRF (Cross-Site Request Forgery)
- ❌ State-changing operations without CSRF protection
- ✅ Use CSRF middleware on POST/PUT/DELETE

### Log Injection
- ❌ Concatenating user input into log messages
- ❌ Logging unsanitized user input
- ✅ Use structured logging with sanitized fields

### Hardcoded Secrets
- ❌ API keys in code
- ❌ Passwords in code
- ✅ Use environment variables

### Path Traversal
- ❌ Using user input directly in file paths
- ✅ Validate and clean all paths

## Security Checklist for New Endpoints

- [ ] Input validation on all parameters
- [ ] Parameterized SQL queries
- [ ] Output sanitization
- [ ] Authentication required (if needed)
- [ ] Authorization checks (if needed)
- [ ] CSRF protection (for POST/PUT/DELETE)
- [ ] Rate limiting applied
- [ ] Error handling doesn't leak info
- [ ] Logging doesn't include secrets
- [ ] Tests include security scenarios

## Quick Commands

```bash
# Run security audit
bash scripts/security_audit.sh

# Apply automated fixes
bash scripts/apply_security_fixes.sh

# Check for vulnerabilities
gosec ./...

# Run tests
go test ./...
```

## When in Doubt

1. Check `SECURITY.md` for detailed guidance
2. Review `SECURITY_FIXES.md` for examples
3. Ask the security team
4. Follow the principle of least privilege
5. Fail securely (deny by default)

## Resources

- [OWASP Top 10](https://owasp.org/www-project-top-ten/)
- [Go Security Cheat Sheet](https://github.com/OWASP/Go-SCP)
- Internal: `SECURITY.md`
- Internal: `SECURITY_FIXES.md`
