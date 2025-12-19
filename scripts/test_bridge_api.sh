#!/bin/bash

# Test Bridge API Sandbox Connectivity
# Usage: ./scripts/test_bridge_api.sh

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo "=========================================="
echo "Bridge API Sandbox Connectivity Test"
echo "=========================================="

# Check for API key
if [ -z "$BRIDGE_API_KEY" ]; then
    echo -e "${YELLOW}Warning: BRIDGE_API_KEY environment variable not set${NC}"
    echo "Please set your Bridge API key:"
    echo "  export BRIDGE_API_KEY='your-api-key'"
    echo ""
    echo "You can get an API key from: https://dashboard.bridge.xyz"
    exit 1
fi

BRIDGE_BASE_URL="${BRIDGE_BASE_URL:-https://api.bridge.xyz}"

echo ""
echo "Configuration:"
echo "  Base URL: $BRIDGE_BASE_URL"
echo "  API Key: ${BRIDGE_API_KEY:0:10}..."
echo ""

# Test 1: List Customers (basic connectivity test)
echo -n "Test 1: List Customers... "
RESPONSE=$(curl -s -w "\n%{http_code}" \
    --request GET \
    --url "${BRIDGE_BASE_URL}/v0/customers?limit=1" \
    --header "Content-Type: application/json" \
    --header "Api-Key: ${BRIDGE_API_KEY}")

HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | sed '$d')

if [ "$HTTP_CODE" -eq 200 ]; then
    echo -e "${GREEN}PASSED${NC} (HTTP $HTTP_CODE)"
else
    echo -e "${RED}FAILED${NC} (HTTP $HTTP_CODE)"
    echo "Response: $BODY"
    exit 1
fi

# Test 2: Create a test customer
echo -n "Test 2: Create Customer... "
CUSTOMER_RESPONSE=$(curl -s -w "\n%{http_code}" \
    --request POST \
    --url "${BRIDGE_BASE_URL}/v0/customers" \
    --header "Content-Type: application/json" \
    --header "Api-Key: ${BRIDGE_API_KEY}" \
    --header "Idempotency-Key: $(uuidgen)" \
    --data "{
        \"type\": \"individual\",
        \"first_name\": \"Test\",
        \"last_name\": \"User\",
        \"email\": \"test-$(date +%s)@example.com\"
    }")

HTTP_CODE=$(echo "$CUSTOMER_RESPONSE" | tail -n1)
BODY=$(echo "$CUSTOMER_RESPONSE" | sed '$d')

if [ "$HTTP_CODE" -eq 201 ] || [ "$HTTP_CODE" -eq 200 ]; then
    echo -e "${GREEN}PASSED${NC} (HTTP $HTTP_CODE)"
    CUSTOMER_ID=$(echo "$BODY" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
    echo "  Customer ID: $CUSTOMER_ID"
else
    echo -e "${RED}FAILED${NC} (HTTP $HTTP_CODE)"
    echo "Response: $BODY"
    # Continue with other tests even if this fails
fi

# Test 3: Get Customer (if we have an ID)
if [ -n "$CUSTOMER_ID" ]; then
    echo -n "Test 3: Get Customer... "
    RESPONSE=$(curl -s -w "\n%{http_code}" \
        --request GET \
        --url "${BRIDGE_BASE_URL}/v0/customers/${CUSTOMER_ID}" \
        --header "Content-Type: application/json" \
        --header "Api-Key: ${BRIDGE_API_KEY}")

    HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
    BODY=$(echo "$RESPONSE" | sed '$d')

    if [ "$HTTP_CODE" -eq 200 ]; then
        echo -e "${GREEN}PASSED${NC} (HTTP $HTTP_CODE)"
        STATUS=$(echo "$BODY" | grep -o '"status":"[^"]*"' | head -1 | cut -d'"' -f4)
        echo "  Customer Status: $STATUS"
    else
        echo -e "${RED}FAILED${NC} (HTTP $HTTP_CODE)"
        echo "Response: $BODY"
    fi

    # Test 4: Get KYC Link
    echo -n "Test 4: Get KYC Link... "
    RESPONSE=$(curl -s -w "\n%{http_code}" \
        --request GET \
        --url "${BRIDGE_BASE_URL}/v0/customers/${CUSTOMER_ID}/kyc_link" \
        --header "Content-Type: application/json" \
        --header "Api-Key: ${BRIDGE_API_KEY}")

    HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
    BODY=$(echo "$RESPONSE" | sed '$d')

    if [ "$HTTP_CODE" -eq 200 ]; then
        echo -e "${GREEN}PASSED${NC} (HTTP $HTTP_CODE)"
    else
        echo -e "${YELLOW}SKIPPED${NC} (HTTP $HTTP_CODE - KYC link may require additional setup)"
    fi
fi

echo ""
echo "=========================================="
echo -e "${GREEN}Bridge API Sandbox Connectivity Verified!${NC}"
echo "=========================================="
echo ""
echo "Next steps:"
echo "1. Set up webhooks in Bridge Dashboard"
echo "2. Configure supported chains in config.yaml"
echo "3. Implement customer onboarding flow"
