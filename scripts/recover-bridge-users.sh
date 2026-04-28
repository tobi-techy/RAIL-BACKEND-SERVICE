#!/bin/bash
set -euo pipefail

# ═══════════════════════════════════════════════════════════════════════════════
# Rail — Recover test users from Bridge API into fresh database
# ═══════════════════════════════════════════════════════════════════════════════
# This script pulls customer + wallet data from Bridge and inserts it into
# the local/production database so the 3 beta testers can continue using the app.
#
# Prerequisites:
#   - BRIDGE_API_KEY env var set
#   - DATABASE_URL env var set (or pass as argument)
#   - psql installed
#   - curl + jq installed
#
# Usage:
#   export BRIDGE_API_KEY="your-bridge-api-key"
#   export DATABASE_URL="postgres://rail:pass@host:5432/rail?sslmode=require"
#   ./scripts/recover-bridge-users.sh
# ═══════════════════════════════════════════════════════════════════════════════

BRIDGE_API_KEY="${BRIDGE_API_KEY:?Set BRIDGE_API_KEY}"
DATABASE_URL="${DATABASE_URL:?Set DATABASE_URL}"
BRIDGE_BASE="${BRIDGE_BASE_URL:-https://api.sandbox.bridge.xyz}"

echo "═══ Rail Bridge Data Recovery ═══"
echo "Bridge: $BRIDGE_BASE"
echo ""

# ── Fetch all customers from Bridge ───────────────────────────────────────────
echo "▸ Fetching customers from Bridge..."
CUSTOMERS=$(curl -sf -H "Api-Key: $BRIDGE_API_KEY" \
  "$BRIDGE_BASE/v0/customers" | jq -c '.data[]')

if [ -z "$CUSTOMERS" ]; then
  echo "  ✗ No customers found on Bridge"
  exit 1
fi

CUSTOMER_COUNT=$(echo "$CUSTOMERS" | wc -l | tr -d ' ')
echo "  ✓ Found $CUSTOMER_COUNT customers"

# ── Process each customer ─────────────────────────────────────────────────────
echo "$CUSTOMERS" | while IFS= read -r customer; do
  BRIDGE_CUSTOMER_ID=$(echo "$customer" | jq -r '.id')
  FIRST_NAME=$(echo "$customer" | jq -r '.first_name // "Unknown"')
  LAST_NAME=$(echo "$customer" | jq -r '.last_name // "User"')
  EMAIL=$(echo "$customer" | jq -r '.email // ""')
  STATUS=$(echo "$customer" | jq -r '.status // "active"')

  echo ""
  echo "▸ Processing: $FIRST_NAME $LAST_NAME ($BRIDGE_CUSTOMER_ID)"

  # Check if user already exists in DB by bridge_customer_id
  EXISTING=$(psql "$DATABASE_URL" -tAc \
    "SELECT id FROM users WHERE bridge_customer_id = '$BRIDGE_CUSTOMER_ID' LIMIT 1" 2>/dev/null || echo "")

  if [ -n "$EXISTING" ]; then
    USER_ID="$EXISTING"
    echo "  ✓ User already exists: $USER_ID"
  else
    # Create user record
    USER_ID=$(psql "$DATABASE_URL" -tAc "
      INSERT INTO users (id, email, first_name, last_name, bridge_customer_id, status, created_at, updated_at)
      VALUES (gen_random_uuid(), '$EMAIL', '$FIRST_NAME', '$LAST_NAME', '$BRIDGE_CUSTOMER_ID', 'active', NOW(), NOW())
      ON CONFLICT (email) DO UPDATE SET bridge_customer_id = '$BRIDGE_CUSTOMER_ID', updated_at = NOW()
      RETURNING id;
    " 2>/dev/null || echo "")

    if [ -z "$USER_ID" ]; then
      echo "  ✗ Failed to create user record, skipping"
      continue
    fi
    echo "  ✓ Created user: $USER_ID"
  fi

  # ── Fetch wallets for this customer ───────────────────────────────────────
  echo "  ▸ Fetching wallets..."
  WALLETS=$(curl -sf -H "Api-Key: $BRIDGE_API_KEY" \
    "$BRIDGE_BASE/v0/customers/$BRIDGE_CUSTOMER_ID/wallets" | jq -c '.data[]' 2>/dev/null || echo "")

  if [ -z "$WALLETS" ]; then
    echo "    No wallets found"
    continue
  fi

  echo "$WALLETS" | while IFS= read -r wallet; do
    WALLET_ID=$(echo "$wallet" | jq -r '.id')
    CHAIN=$(echo "$wallet" | jq -r '.chain')
    ADDRESS=$(echo "$wallet" | jq -r '.address')

    # Map Bridge chain to our WalletChain enum
    case "$CHAIN" in
      solana)     WALLET_CHAIN="SOL" ;;
      ethereum)   WALLET_CHAIN="MATIC" ;;  # EVM shared address, store as MATIC
      base)       WALLET_CHAIN="BASE" ;;
      tron)       WALLET_CHAIN="TRON" ;;
      *)          WALLET_CHAIN="$CHAIN" ;;
    esac

    echo "    ▸ Wallet: $WALLET_CHAIN ($ADDRESS)"

    psql "$DATABASE_URL" -c "
      INSERT INTO managed_wallets (id, user_id, chain, address, bridge_wallet_id, account_type, status, created_at, updated_at)
      VALUES (gen_random_uuid(), '$USER_ID', '$WALLET_CHAIN', '$ADDRESS', '$WALLET_ID', 'bridge_wallet', 'live', NOW(), NOW())
      ON CONFLICT DO NOTHING;
    " 2>/dev/null && echo "    ✓ Saved" || echo "    ✗ Failed (may already exist)"

    # For EVM wallets, also create entries for other EVM chains sharing the same address
    if [ "$CHAIN" = "ethereum" ]; then
      for EVM_CHAIN in CELO BASE AVAX; do
        if [ "$EVM_CHAIN" != "$WALLET_CHAIN" ]; then
          psql "$DATABASE_URL" -c "
            INSERT INTO managed_wallets (id, user_id, chain, address, bridge_wallet_id, account_type, status, created_at, updated_at)
            VALUES (gen_random_uuid(), '$USER_ID', '$EVM_CHAIN', '$ADDRESS', '$WALLET_ID', 'bridge_wallet', 'live', NOW(), NOW())
            ON CONFLICT DO NOTHING;
          " 2>/dev/null || true
        fi
      done
      echo "    ✓ EVM chains cross-populated"
    fi
  done
done

echo ""
echo "═══ Recovery complete ═══"
echo ""
echo "Verify with:"
echo "  psql \$DATABASE_URL -c 'SELECT u.email, m.chain, m.address FROM managed_wallets m JOIN users u ON u.id = m.user_id ORDER BY u.email, m.chain;'"
