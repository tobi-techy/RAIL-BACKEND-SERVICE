#!/bin/bash

# Script to consolidate handler files
# This creates consolidated handler files from existing ones

set -e

HANDLERS_DIR="internal/api/handlers"
BACKUP_DIR="internal/api/handlers_backup_$(date +%Y%m%d_%H%M%S)"

echo "Creating backup at $BACKUP_DIR..."
mkdir -p "$BACKUP_DIR"
cp -r "$HANDLERS_DIR"/*.go "$BACKUP_DIR/"

echo "Backup created successfully"
echo ""
echo "Consolidation plan:"
echo "  - wallet_funding_handlers.go (wallet + funding + withdrawal)"
echo "  - integration_handlers.go (alpaca + due + zerog + webhooks)"
echo "  - security_admin_handlers.go (admin + security + compliance)"
echo "  - auth_handlers.go (auth_signup + onboarding)"
echo ""
echo "Files to be consolidated are backed up in: $BACKUP_DIR"
echo ""
echo "Next steps:"
echo "1. Review the consolidated files created"
echo "2. Update route registrations in internal/api/routes/"
echo "3. Test all endpoints"
echo "4. Remove old handler files"

echo ""
echo "To restore from backup if needed:"
echo "  cp $BACKUP_DIR/*.go $HANDLERS_DIR/"
