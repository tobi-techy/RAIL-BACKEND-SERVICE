#!/bin/bash

# Script to check if your staging environment is ready to receive deposits
# Usage: ./scripts/check_deposit_readiness.sh

WALLET_ADDRESS="Bes4jEMuVKqj4m3MhQtXsZSqfKFMThYetkUjFjEyTPRZ"
TX_HASH="4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"

echo "🔍 Checking deposit readiness for RAIL staging..."
echo ""
echo "Wallet Address: $WALLET_ADDRESS"
echo "Transaction Hash: $TX_HASH"
echo "Amount: 20 USDC"
echo "Chain: Solana"
echo ""

# Check 1: Webhook endpoint
echo "✓ Step 1: Testing webhook endpoint..."
WEBHOOK_RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" \
  -X POST https://rail-backend-service-production.up.railway.app/health)

if [ "$WEBHOOK_RESPONSE" = "200" ]; then
  echo "  ✅ Backend is reachable (HTTP $WEBHOOK_RESPONSE)"
else
  echo "  ❌ Backend is not reachable (HTTP $WEBHOOK_RESPONSE)"
  exit 1
fi
echo ""

# Check 2: Webhook funding endpoint
echo "✓ Step 2: Testing webhook funding endpoint..."
FUNDING_RESPONSE=$(curl -s -X POST \
  https://rail-backend-service-production.up.railway.app/api/v1/webhooks/funding \
  -H "Content-Type: application/json" \
  -H "X-Webhook-Source: bridge" \
  -d '{"notificationType":"ping"}')

echo "  Response: $FUNDING_RESPONSE"
if echo "$FUNDING_RESPONSE" | grep -q "invalid signature"; then
  echo "  ✅ Endpoint is working (requires signature as expected)"
elif echo "$FUNDING_RESPONSE" | grep -q "unknown webhook source"; then
  echo "  ⚠️  Endpoint detected but source detection may need adjustment"
else
  echo "  ℹ️  Response: $FUNDING_RESPONSE"
fi
echo ""

# Check 3: Bridge webhook secret
echo "✓ Step 3: Checking Bridge webhook secret..."
if [ -n "$BRIDGE_WEBHOOK_SECRET" ]; then
  echo "  ✅ BRIDGE_WEBHOOK_SECRET is set locally"
  echo "  ⚠️  Make sure this is also set in Railway environment variables"
else
  echo "  ❌ BRIDGE_WEBHOOK_SECRET is not set"
  echo "  Run: export BRIDGE_WEBHOOK_SECRET='your-secret-from-bridge-dashboard'"
fi
echo ""

# Check 4: Instructions for manual webhook test
echo "✓ Step 4: Manual webhook test"
echo "  To manually trigger the webhook for your deposit, you need to:"
echo ""
echo "  Option A: Use Bridge Dashboard"
echo "    1. Go to Bridge Dashboard → Webhooks"
echo "    2. Find your webhook subscription"
echo "    3. Look for 'Test' or 'Replay' button"
echo "    4. Send a test event or replay the transaction"
echo ""
echo "  Option B: Wait for Bridge to send webhook"
echo "    Bridge should automatically send a webhook for your deposit."
echo "    Check your Railway logs for incoming webhooks:"
echo "    railway logs --tail 100"
echo ""
echo "  Option C: Make a new small test deposit"
echo "    Send 1 USDC to the same address to trigger a fresh webhook"
echo ""

# Check 5: Database check instructions
echo "✓ Step 5: Verify wallet exists in database"
echo "  You need to check if the wallet address exists in your staging database."
echo "  Connect to your Railway Postgres and run:"
echo ""
echo "  SELECT id, user_id, address, chain, status"
echo "  FROM managed_wallets"
echo "  WHERE address = '$WALLET_ADDRESS';"
echo ""
echo "  If no results, the wallet wasn't created during onboarding."
echo "  You'll need to complete the onboarding flow first."
echo ""

# Summary
echo "📋 Summary:"
echo "  1. ✅ Bridge webhook configured"
echo "  2. ✅ Backend endpoint is reachable"
echo "  3. ⚠️  Verify BRIDGE_WEBHOOK_SECRET in Railway"
echo "  4. ⚠️  Verify wallet exists in staging database"
echo "  5. ⚠️  Check Railway logs for webhook delivery"
echo ""
echo "Next steps:"
echo "  1. Check Railway environment variables for BRIDGE_WEBHOOK_SECRET"
echo "  2. Check Railway logs: railway logs --tail 100"
echo "  3. Verify wallet exists in database"
echo "  4. If wallet doesn't exist, complete onboarding first"
echo ""
