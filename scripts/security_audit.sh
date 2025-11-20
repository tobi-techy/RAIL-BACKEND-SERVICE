#!/bin/bash
set -euo pipefail

echo "=== STACK Service Security Audit ==="
echo ""

# Check for hardcoded secrets
echo "[1] Checking for hardcoded secrets..."
if grep -r -i "password.*=.*\"" --include="*.go" internal/ pkg/ cmd/ 2>/dev/null | grep -v "PasswordHash" | grep -v "Password:" | grep -v "// "; then
    echo "⚠️  WARNING: Potential hardcoded passwords found"
else
    echo "✓ No hardcoded passwords detected"
fi

# Check for SQL injection vulnerabilities
echo ""
echo "[2] Checking for potential SQL injection..."
if grep -r "fmt.Sprintf.*SELECT\|fmt.Sprintf.*INSERT\|fmt.Sprintf.*UPDATE\|fmt.Sprintf.*DELETE" --include="*.go" internal/ 2>/dev/null; then
    echo "⚠️  WARNING: Potential SQL injection via string formatting"
else
    echo "✓ No obvious SQL injection patterns found"
fi

# Check for XSS vulnerabilities
echo ""
echo "[3] Checking for XSS prevention..."
if grep -r "c.JSON.*c.Query\|c.JSON.*c.Param" --include="*.go" internal/api/handlers/ 2>/dev/null | grep -v "sanitize"; then
    echo "⚠️  WARNING: User input may be returned without sanitization"
else
    echo "✓ Handlers appear to sanitize user input"
fi

# Check for CSRF protection
echo ""
echo "[4] Checking for CSRF protection..."
if [ -f "internal/api/middleware/csrf.go" ]; then
    echo "✓ CSRF middleware exists"
else
    echo "⚠️  WARNING: CSRF middleware not found"
fi

# Check file permissions
echo ""
echo "[5] Checking file permissions..."
find . -type f \( -name "*.key" -o -name "*.pem" -o -name "*secret*" \) -not -path "./vendor/*" -not -path "./.git/*" 2>/dev/null | while read -r file; do
    perms=$(stat -f "%A" "$file" 2>/dev/null || stat -c "%a" "$file" 2>/dev/null || echo "unknown")
    if [ "$perms" != "600" ] && [ "$perms" != "unknown" ]; then
        echo "⚠️  WARNING: $file has permissions $perms (should be 600)"
    fi
done
echo "✓ File permission check complete"

# Check for error handling
echo ""
echo "[6] Checking error handling in shell scripts..."
for script in scripts/*.sh test/*.sh; do
    if [ -f "$script" ]; then
        if ! grep -q "set -e" "$script"; then
            echo "⚠️  WARNING: $script missing 'set -e'"
        fi
    fi
done
echo "✓ Shell script error handling check complete"

# Check for environment variables
echo ""
echo "[7] Checking environment variable usage..."
if [ -f ".env.security.example" ]; then
    echo "✓ Security environment template exists"
else
    echo "⚠️  WARNING: .env.security.example not found"
fi

# Check for security headers
echo ""
echo "[8] Checking security headers middleware..."
if grep -q "SecurityHeaders" internal/api/middleware/middleware.go 2>/dev/null; then
    echo "✓ Security headers middleware exists"
else
    echo "⚠️  WARNING: Security headers middleware not found"
fi

# Check for rate limiting
echo ""
echo "[9] Checking rate limiting..."
if grep -q "RateLimit" internal/api/middleware/middleware.go 2>/dev/null; then
    echo "✓ Rate limiting middleware exists"
else
    echo "⚠️  WARNING: Rate limiting middleware not found"
fi

# Check for input validation
echo ""
echo "[10] Checking input validation..."
if grep -q "InputValidation" internal/api/middleware/middleware.go 2>/dev/null; then
    echo "✓ Input validation middleware exists"
else
    echo "⚠️  WARNING: Input validation middleware not found"
fi

echo ""
echo "=== Audit Complete ==="
echo ""
echo "Review warnings above and apply fixes as needed."
echo "See SECURITY_FIXES.md for remediation guidance."
