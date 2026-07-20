#!/usr/bin/env bash
# Submit tx hash for the LOCKED Blend intent 7240b8cc-40a9-443c-86e9-2c9fa319fad4
# Run: source .env && bash scripts/submit_locked_tx_hash.sh

set -euo pipefail

INTENT_ID="7240b8cc-40a9-443c-86e9-2c9fa319fad4"
ACCOUNT_ID="17ee28ec-51e8-41fa-82b7-7005642975c7"
ACCOUNT_TYPE_ID="${BLEND_ACCOUNT_TYPE_ID:?BLEND_ACCOUNT_TYPE_ID not set}"
API_KEY="${BLEND_API_KEY:?BLEND_API_KEY not set}"
BASE_URL="${BLEND_BASE_URL:-https://api.portal.blend.money}"

TX_HASH="0x828666ec59d4617765155aabc1526e52ae4fd4316780adb5585a256198a5ab26"

echo "Submitting tx hash to Blend..."
echo "  Intent:  $INTENT_ID"
echo "  Account: $ACCOUNT_ID"
echo "  Hash:    $TX_HASH"

RESPONSE=$(curl -s -w "\n%{http_code}" \
  -X POST "${BASE_URL}/extern/svr/${ACCOUNT_TYPE_ID}/account/${ACCOUNT_ID}/intent/${INTENT_ID}/submit" \
  -H "X-API-Key: ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d "{\"txHashes\": [{\"hash\": \"${TX_HASH}\", \"chainId\": 8453}]}")

HTTP_CODE=$(echo "$RESPONSE" | tail -1)
BODY=$(echo "$RESPONSE" | sed '$d')

echo ""
echo "HTTP $HTTP_CODE"
echo "$BODY" | python3 -m json.tool 2>/dev/null || echo "$BODY"
