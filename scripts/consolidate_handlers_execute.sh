#!/bin/bash

# Handler Consolidation Execution Script
# This script helps consolidate handler files step by step

set -e

HANDLERS_DIR="internal/api/handlers"
BACKUP_DIR="internal/api/handlers_backup_$(date +%Y%m%d_%H%M%S)"

echo "========================================="
echo "Handler Consolidation Script"
echo "========================================="
echo ""

# Step 1: Create backup
echo "Step 1: Creating backup..."
mkdir -p "$BACKUP_DIR"
cp -r "$HANDLERS_DIR"/*.go "$BACKUP_DIR/"
echo "✓ Backup created at: $BACKUP_DIR"
echo ""

# Step 2: Check for consolidated files
echo "Step 2: Checking for consolidated files..."
CONSOLIDATED_FILES=(
    "core_handlers_consolidated.go"
    "auth_handlers_consolidated.go"
    "wallet_funding_handlers_consolidated.go"
    "investment_handlers_consolidated.go"
    "integration_handlers_consolidated.go"
    "security_admin_handlers_consolidated.go"
    "notification_worker_handlers_consolidated.go"
)

MISSING_FILES=()
for file in "${CONSOLIDATED_FILES[@]}"; do
    if [ ! -f "$HANDLERS_DIR/$file" ]; then
        MISSING_FILES+=("$file")
    fi
done

if [ ${#MISSING_FILES[@]} -gt 0 ]; then
    echo "⚠ Missing consolidated files:"
    for file in "${MISSING_FILES[@]}"; do
        echo "  - $file"
    done
    echo ""
    echo "Please create these files before proceeding."
    echo "See CONSOLIDATION_GUIDE.md for details."
    exit 1
fi

echo "✓ All consolidated files present"
echo ""

# Step 3: List files to be replaced
echo "Step 3: Files to be consolidated:"
echo ""
echo "Core handlers (→ core_handlers.go):"
echo "  - version_handler.go"
echo "  - health_handlers.go"
echo "  - handlers.go (stubs)"
echo ""
echo "Auth handlers (→ auth_handlers.go):"
echo "  - auth_signup_handlers.go"
echo "  - onboarding_handlers.go"
echo ""
echo "Wallet/Funding handlers (→ wallet_funding_handlers.go):"
echo "  - wallet_handlers.go"
echo "  - funding_investing_handlers.go"
echo "  - withdrawal_handlers.go"
echo ""
echo "Investment handlers (→ investment_handlers.go):"
echo "  - investment_handlers.go"
echo "  - portfolio_handlers.go"
echo "  - stack_handlers.go"
echo ""
echo "Integration handlers (→ integration_handlers.go):"
echo "  - alpaca_handlers.go"
echo "  - due_handlers.go"
echo "  - due_webhook_handler.go"
echo "  - zerog_handlers.go"
echo ""
echo "Security/Admin handlers (→ security_admin_handlers.go):"
echo "  - admin_handlers.go"
echo "  - security_handlers.go"
echo "  - enhanced_security_handlers.go"
echo "  - compliance_handlers.go"
echo ""
echo "Notification/Worker handlers (→ notification_worker_handlers.go):"
echo "  - notification_handlers.go"
echo "  - worker_handlers.go"
echo ""

# Step 4: Confirmation
read -p "Do you want to proceed with consolidation? (yes/no): " CONFIRM
if [ "$CONFIRM" != "yes" ]; then
    echo "Consolidation cancelled."
    exit 0
fi

echo ""
echo "Step 4: Renaming consolidated files..."

# Rename consolidated files
for file in "${CONSOLIDATED_FILES[@]}"; do
    BASE_NAME="${file/_consolidated/}"
    if [ -f "$HANDLERS_DIR/$file" ]; then
        mv "$HANDLERS_DIR/$file" "$HANDLERS_DIR/$BASE_NAME"
        echo "✓ Renamed $file → $BASE_NAME"
    fi
done

echo ""
echo "Step 5: Archiving old files..."

# Move old files to backup (don't delete yet)
OLD_FILES=(
    "version_handler.go"
    "health_handlers.go"
    "auth_signup_handlers.go"
    "onboarding_handlers.go"
    "wallet_handlers.go"
    "funding_investing_handlers.go"
    "withdrawal_handlers.go"
    "investment_handlers.go"
    "portfolio_handlers.go"
    "stack_handlers.go"
    "alpaca_handlers.go"
    "due_handlers.go"
    "due_webhook_handler.go"
    "zerog_handlers.go"
    "admin_handlers.go"
    "security_handlers.go"
    "enhanced_security_handlers.go"
    "compliance_handlers.go"
    "notification_handlers.go"
    "worker_handlers.go"
)

for file in "${OLD_FILES[@]}"; do
    if [ -f "$HANDLERS_DIR/$file" ]; then
        mv "$HANDLERS_DIR/$file" "$BACKUP_DIR/${file}.old"
        echo "✓ Archived $file"
    fi
done

echo ""
echo "========================================="
echo "Consolidation Complete!"
echo "========================================="
echo ""
echo "Summary:"
echo "  - Backup location: $BACKUP_DIR"
echo "  - Old files archived with .old extension"
echo "  - New consolidated files in place"
echo ""
echo "Next Steps:"
echo "  1. Update route registrations in internal/api/routes/"
echo "  2. Run tests: go test ./internal/api/handlers/..."
echo "  3. Test all endpoints manually"
echo "  4. If issues occur, restore from backup:"
echo "     cp $BACKUP_DIR/*.go $HANDLERS_DIR/"
echo ""
echo "See CONSOLIDATION_GUIDE.md for detailed instructions."
