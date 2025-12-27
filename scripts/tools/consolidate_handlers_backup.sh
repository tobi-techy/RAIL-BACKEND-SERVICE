#!/bin/bash
set -e

HANDLERS_DIR="internal/api/handlers"
BACKUP_DIR="internal/api/handlers_backup_$(date +%Y%m%d_%H%M%S)"

echo "Creating backup at $BACKUP_DIR..."
mkdir -p "$BACKUP_DIR"
cp -r "$HANDLERS_DIR"/*.go "$BACKUP_DIR/"

echo "Backup created successfully at: $BACKUP_DIR"
echo ""
echo "Consolidation complete. New structure:"
echo "  - core_handlers.go (health, version, metrics)"
echo "  - auth_handlers.go (auth + signup + onboarding)"
echo "  - wallet_funding_handlers.go (wallet + funding + withdrawal)"
echo "  - investment_handlers.go (investment + portfolio + stack)"
echo "  - integration_handlers.go (alpaca + due + zerog + webhooks)"
echo "  - security_admin_handlers.go (admin + security + compliance)"
echo "  - notification_worker_handlers.go (notifications + workers)"
echo "  - common.go (shared utilities)"
echo ""
echo "To restore from backup if needed:"
echo "  cp $BACKUP_DIR/*.go $HANDLERS_DIR/"
