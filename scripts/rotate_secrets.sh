#!/bin/bash

# Script to help rotate exposed secrets
# Run this after manually rotating credentials in each service dashboard

set -e

echo "🔐 Secret Rotation Helper Script"
echo "================================="
echo ""
echo "⚠️  CRITICAL: This script helps you rotate exposed credentials."
echo "⚠️  You MUST manually rotate credentials in each service dashboard first!"
echo ""

# Check if .env exists
if [ ! -f .env ]; then
    echo "❌ .env file not found. Creating from .env.example..."
    cp .env.example .env
    echo "✅ Created .env from template. Please fill in your NEW credentials."
    exit 0
fi

echo "📋 Checklist of credentials to rotate:"
echo ""
echo "1. ❌ Alpaca Broker API"
echo "   Dashboard: https://app.alpaca.markets/brokerage/dashboard"
echo "   - Revoke key: CK34TUO74LOOU4Z6UWPPFI7JLF"
echo "   - Generate new API key"
echo ""

echo "2. ❌ Circle API"
echo "   Dashboard: https://console.circle.com/"
echo "   - Revoke exposed key"
echo "   - Generate new API key"
echo ""

echo "3. ❌ Sumsub KYC"
echo "   Dashboard: https://cockpit.sumsub.com/"
echo "   - Revoke exposed credentials"
echo "   - Generate new API key and secret"
echo ""

echo "4. ❌ Resend Email"
echo "   Dashboard: https://resend.com/api-keys"
echo "   - Revoke key: re_JMRDKd4X_79kvpRkfGonpWDyvVKg5JbdH"
echo "   - Generate new API key"
echo ""

echo "5. ❌ Twilio SMS"
echo "   Dashboard: https://console.twilio.com/"
echo "   - Rotate auth token for: AC779f2f78e54f465a88a41b37358a5b3d"
echo ""

echo "6. ❌ ZeroG Private Keys"
echo "   - Generate new private keys"
echo "   - Transfer any assets from old addresses"
echo ""

echo ""
echo "After rotating all credentials above, update your .env file with NEW values."
echo ""

read -p "Have you rotated ALL credentials? (yes/no): " confirm

if [ "$confirm" != "yes" ]; then
    echo "❌ Please rotate all credentials before proceeding."
    exit 1
fi

echo ""
echo "✅ Great! Now let's clean up git history..."
echo ""

# Check if git-filter-repo is installed
if ! command -v git-filter-repo &> /dev/null; then
    echo "⚠️  git-filter-repo is not installed."
    echo ""
    echo "Install it with:"
    echo "  macOS: brew install git-filter-repo"
    echo "  Linux: pip install git-filter-repo"
    echo ""
    exit 1
fi

echo "⚠️  WARNING: This will rewrite git history!"
echo "⚠️  All team members will need to re-clone the repository."
echo ""

read -p "Proceed with git history cleanup? (yes/no): " git_confirm

if [ "$git_confirm" != "yes" ]; then
    echo "❌ Aborted. Run this script again when ready."
    exit 1
fi

# Remove .env from git tracking if it exists
if git ls-files --error-unmatch .env &> /dev/null; then
    echo "🗑️  Removing .env from git tracking..."
    git rm --cached .env
    git commit -m "Remove .env from tracking (security incident)"
fi

# Create backup
echo "💾 Creating backup..."
BACKUP_DIR="../rail_service_backup_$(date +%Y%m%d_%H%M%S)"
cp -r . "$BACKUP_DIR"
echo "✅ Backup created at: $BACKUP_DIR"

# Remove .env from history
echo "🧹 Removing .env from git history..."
git filter-repo --path .env --invert-paths --force

echo ""
echo "✅ Git history cleaned!"
echo ""
echo "Next steps:"
echo "1. Force push to remote: git push origin --force --all"
echo "2. Force push tags: git push origin --force --tags"
echo "3. Notify all team members to re-clone the repository"
echo "4. Update production/staging environments with new credentials"
echo "5. Enable secret scanning in GitHub repository settings"
echo ""
