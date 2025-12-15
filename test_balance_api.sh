#!/bin/bash

# Test Circle API balance endpoint
# Replace with your actual API key
API_KEY="YOUR_API_KEY_HERE"
WALLET_ID="6de94cea-8895-5e29-ac79-cb61413d5c60"
TOKEN_ADDRESS="4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"

echo "Testing Circle API Balance Endpoint"
echo "===================================="
echo ""
echo "Wallet ID: $WALLET_ID"
echo "Token Address: $TOKEN_ADDRESS"
echo ""

# Test without token address filter
echo "1. Testing WITHOUT token address filter:"
curl -X GET \
  "https://api.circle.com/v1/w3s/wallets/${WALLET_ID}/balances" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json" \
  | jq '.'

echo ""
echo ""

# Test with token address filter
echo "2. Testing WITH token address filter:"
curl -X GET \
  "https://api.circle.com/v1/w3s/wallets/${WALLET_ID}/balances?tokenAddress=${TOKEN_ADDRESS}" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json" \
  | jq '.'
