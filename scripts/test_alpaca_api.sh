#!/bin/bash

# Test Alpaca API Integration
# This script tests the Alpaca Broker API endpoints with the correct authentication

set -e

# Configuration
BASE_URL="https://broker-api.sandbox.alpaca.markets"
CLIENT_ID="CK34TUO74LOOU4Z6UWPPFI7JLF"
SECRET_KEY="H7QH5VbZKxsbiedQ4ETZXVJ3XucB4wpxz6MrSnoCsWHe"

echo "Testing Alpaca Broker API Integration..."
echo "Base URL: $BASE_URL"
echo "Client ID: $CLIENT_ID"
echo ""

# Test 1: List Accounts
echo "Test 1: List Accounts"
echo "GET $BASE_URL/v1/accounts"
curl -s -X GET "$BASE_URL/v1/accounts" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json" \
  -u "$CLIENT_ID:$SECRET_KEY" | jq '.' || echo "Failed to parse JSON response"

echo ""
echo "----------------------------------------"
echo ""

# # Test 2: List Assets (should work with any valid credentials)
# echo "Test 2: List Assets"
# echo "GET $BASE_URL/v1/assets?limit=5"
# curl -s -X GET "$BASE_URL/v1/assets?limit=5" \
#   -H "Content-Type: application/json" \
#   -H "Accept: application/json" \
#   -u "$CLIENT_ID:$SECRET_KEY" | jq '.' || echo "Failed to parse JSON response"

# echo ""
# echo "----------------------------------------"
# echo ""

# Test 3: Test authentication with invalid credentials (should fail)
echo "Test 3: Test with invalid credentials (should return 401)"
echo "GET $BASE_URL/v1/accounts"
curl -s -X GET "$BASE_URL/v1/accounts" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json" \
  -u "invalid:invalid" | jq '.' || echo "Failed to parse JSON response"

echo ""
echo "----------------------------------------"
echo ""

echo "Alpaca API integration test completed!"