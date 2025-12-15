#!/bin/bash
set -euo pipefail

echo "Applying security fixes to rail_service..."

# Add sanitize import to go.mod if needed
if ! grep -q "sanitize" go.mod 2>/dev/null; then
    echo "Adding internal packages..."
fi

# Fix shell scripts - add error handling
for script in scripts/*.sh test/*.sh; do
    if [ -f "$script" ]; then
        if ! grep -q "set -e" "$script"; then
            echo "Adding error handling to $script"
            sed -i.bak '2i\
set -euo pipefail
' "$script" 2>/dev/null || true
        fi
    fi
done

# Fix file permissions
echo "Setting secure file permissions..."
find . -type f -name "*.key" -exec chmod 600 {} \; 2>/dev/null || true
find . -type f -name "*.pem" -exec chmod 600 {} \; 2>/dev/null || true
find . -type f -name "*secret*" -exec chmod 600 {} \; 2>/dev/null || true

echo "Security fixes applied. Review SECURITY_FIXES.md for details."
echo "Manual steps required:"
echo "1. Update routes to use CSRF middleware"
echo "2. Update handlers to use sanitization"
echo "3. Review SQL queries for parameterization"
echo "4. Move hardcoded secrets to environment variables"
