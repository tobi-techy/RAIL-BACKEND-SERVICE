#!/bin/bash

# Deprecated: Circle webhooks are no longer used. Use Bridge webhook testing instead.
echo "Deprecated: Circle webhooks are no longer used. Use Bridge webhook testing instead."
exit 1

# Test Circle webhook with your actual Solana deposit
# Transaction: 4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU
# Wallet: Bes4jEMuVKqj4m3MhQtXsZSqfKFMThYetkUjFjEyTPRZ

WEBHOOK_URL="${1:-https://rail-backend-service-production.up.railway.app/api/v1/webhooks/funding}"

# Circle webhook payload for inbound transaction
PAYLOAD='{
  "notificationType": "transactions.inbound",
  "notification": {
    "id": "test-notification-id",
    "blockchain": "SOL",
    "walletId": "your-circle-wallet-id",
    "tokenId": "usdc-token-id",
    "destinationAddress": "Bes4jEMuVKqj4m3MhQtXsZSqfKFMThYetkUjFjEyTPRZ",
    "amounts": ["20"],
    "state": "COMPLETED",
    "transactionType": "INBOUND",
    "txHash": "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU",
    "transactionHash": "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU",
    "createDate": "2026-02-16T19:29:00Z",
    "updateDate": "2026-02-16T19:30:00Z"
  },
  "timestamp": "2026-02-16T19:30:00Z"
}'

echo "Testing Circle webhook..."
echo "URL: $WEBHOOK_URL"
echo ""
echo "Payload:"
echo "$PAYLOAD" | jq .
echo ""

# Calculate HMAC signature (if you have the webhook secret)
if [ -n "$CIRCLE_WEBHOOK_SECRET" ]; then
  SIGNATURE=$(echo -n "$PAYLOAD" | openssl dgst -sha256 -hmac "$CIRCLE_WEBHOOK_SECRET" | cut -d' ' -f2)
  echo "Signature: $SIGNATURE"
  echo ""
  
  curl -X POST "$WEBHOOK_URL" \
    -H "Content-Type: application/json" \
    -H "X-Circle-Signature: $SIGNATURE" \
    -H "X-Webhook-Source: circle" \
    -d "$PAYLOAD" \
    -v
else
  echo "⚠️  CIRCLE_WEBHOOK_SECRET not set, sending without signature"
  echo ""
  
  curl -X POST "$WEBHOOK_URL" \
    -H "Content-Type: application/json" \
    -H "X-Webhook-Source: circle" \
    -d "$PAYLOAD" \
    -v
fi
