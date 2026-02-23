#!/bin/bash

# Manually trigger Circle webhook for your Solana deposit
# This simulates what Circle should send automatically

WEBHOOK_URL="https://rail-backend-service-production.up.railway.app/api/v1/webhooks/funding"
WALLET_ADDRESS="Bes4jEMuVKqj4m3MhQtXsZSqfKFMThYetkUjFjEyTPRZ"
TX_HASH="4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"

# Circle webhook payload for Solana USDC deposit
PAYLOAD=$(cat <<EOF
{
  "notificationType": "transactions.inbound",
  "notification": {
    "id": "manual-test-$(date +%s)",
    "blockchain": "SOL",
    "destinationAddress": "$WALLET_ADDRESS",
    "amounts": ["20"],
    "state": "COMPLETED",
    "transactionType": "INBOUND",
    "txHash": "$TX_HASH",
    "transactionHash": "$TX_HASH",
    "createDate": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
    "updateDate": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  },
  "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
EOF
)

echo "🚀 Triggering Circle webhook for Solana deposit..."
echo ""
echo "Webhook URL: $WEBHOOK_URL"
echo "Wallet: $WALLET_ADDRESS"
echo "TX Hash: $TX_HASH"
echo "Amount: 20 USDC"
echo ""

# Check if webhook secret is set
if [ -z "$CIRCLE_WEBHOOK_SECRET" ]; then
  echo "⚠️  CIRCLE_WEBHOOK_SECRET not set"
  echo "   This will likely fail signature verification"
  echo "   Set it with: export CIRCLE_WEBHOOK_SECRET='your-secret'"
  echo ""
  echo "Sending without signature (will only work in development mode)..."
  echo ""
  
  curl -X POST "$WEBHOOK_URL" \
    -H "Content-Type: application/json" \
    -H "X-Webhook-Source: circle" \
    -d "$PAYLOAD" \
    -w "\n\nHTTP Status: %{http_code}\n" \
    -v
else
  # Calculate HMAC-SHA256 signature
  SIGNATURE=$(echo -n "$PAYLOAD" | openssl dgst -sha256 -hmac "$CIRCLE_WEBHOOK_SECRET" | awk '{print $2}')
  
  echo "✅ Using webhook secret for signature"
  echo "Signature: $SIGNATURE"
  echo ""
  
  curl -X POST "$WEBHOOK_URL" \
    -H "Content-Type: application/json" \
    -H "X-Circle-Signature: $SIGNATURE" \
    -H "X-Webhook-Source: circle" \
    -d "$PAYLOAD" \
    -w "\n\nHTTP Status: %{http_code}\n" \
    -v
fi

echo ""
echo "✅ Webhook sent!"
echo ""
echo "Next steps:"
echo "  1. Check Railway logs: railway logs --tail 50"
echo "  2. Look for 'Processing Circle webhook' or errors"
echo "  3. Check if deposit was created in database"
echo ""
