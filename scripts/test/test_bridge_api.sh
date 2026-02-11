#!/bin/bash

# Bridge API Sandbox Connectivity & Integration Test
# Usage: ./scripts/test/test_bridge_api.sh
#        VERBOSE=1 ./scripts/test/test_bridge_api.sh   # Show full API responses
#
# Environment Variables:
#   BRIDGE_API_KEY      - Required: Your Bridge API key
#   BRIDGE_BASE_URL     - Optional: API base URL (default: https://api.sandbox.bridge.xyz)
#   VERBOSE             - Optional: Set to 1 or true to show full API responses

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

PASSED=0
FAILED=0
SKIPPED=0

VERBOSE="${VERBOSE:-false}"

log_test() {
    echo -n "  $1... "
}

pass() {
    echo -e "${GREEN}PASSED${NC}"
    PASSED=$((PASSED + 1))
}

fail() {
    echo -e "${RED}FAILED${NC} - $1"
    FAILED=$((FAILED + 1))
}

skip() {
    echo -e "${YELLOW}SKIPPED${NC} - $1"
    SKIPPED=$((SKIPPED + 1))
}

info() {
    echo -e "${BLUE}$1${NC}"
}

show_response() {
    if [ "$VERBOSE" = "true" ] || [ "$VERBOSE" = "1" ]; then
        echo -e "    ${YELLOW}Response:${NC}"
        echo "$1" | jq . 2>/dev/null || echo "$1" | head -20
        echo ""
    fi
}

echo "=========================================="
echo "  Bridge API Integration Test Suite"
echo "=========================================="
echo ""

# Check for API key
if [ -z "$BRIDGE_API_KEY" ]; then
    echo -e "${RED}Error: BRIDGE_API_KEY environment variable not set${NC}"
    echo ""
    echo "Usage:"
    echo "  export BRIDGE_API_KEY='your-sandbox-api-key'"
    echo "  ./scripts/test/test_bridge_api.sh"
    echo ""
    echo "Get your API key from: https://dashboard.bridge.xyz"
    exit 1
fi

BRIDGE_BASE_URL="${BRIDGE_BASE_URL:-https://api.sandbox.bridge.xyz}"

echo "Configuration:"
echo "  Base URL: $BRIDGE_BASE_URL"
echo "  API Key:  ${BRIDGE_API_KEY:0:12}..."
echo ""

# ============================================
# SECTION 1: Basic Connectivity
# ============================================
echo "─────────────────────────────────────────"
echo "1. Basic Connectivity Tests"
echo "─────────────────────────────────────────"

# Test 1.1: List Customers (API connectivity)
log_test "API connectivity (list customers)"
RESPONSE=$(curl -s -w "\n%{http_code}" \
    --request GET \
    --url "${BRIDGE_BASE_URL}/v0/customers?limit=1" \
    --header "Content-Type: application/json" \
    --header "Api-Key: ${BRIDGE_API_KEY}" 2>/dev/null)

HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | sed '$d')

if [ "$HTTP_CODE" -eq 200 ]; then
    pass
    show_response "$BODY"
else
    fail "HTTP $HTTP_CODE"
    echo "    Response: $BODY"
fi

# ============================================
# SECTION 2: Customer Management
# ============================================
echo ""
echo "─────────────────────────────────────────"
echo "2. Customer Management Tests"
echo "─────────────────────────────────────────"

# Test 2.1: Create Customer
log_test "Create test customer"
TEST_EMAIL="rail-test-$(date +%s)@example.com"
IDEMPOTENCY_KEY=$(uuidgen 2>/dev/null || cat /proc/sys/kernel/random/uuid 2>/dev/null || echo "test-$(date +%s)")

RESPONSE=$(curl -s -w "\n%{http_code}" \
    --request POST \
    --url "${BRIDGE_BASE_URL}/v0/customers" \
    --header "Content-Type: application/json" \
    --header "Api-Key: ${BRIDGE_API_KEY}" \
    --header "Idempotency-Key: ${IDEMPOTENCY_KEY}" \
    --data "{
        \"type\": \"individual\",
        \"first_name\": \"Rail\",
        \"last_name\": \"TestUser\",
        \"email\": \"${TEST_EMAIL}\"
    }" 2>/dev/null)

HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | sed '$d')

if [ "$HTTP_CODE" -eq 201 ] || [ "$HTTP_CODE" -eq 200 ]; then
    pass
    CUSTOMER_ID=$(echo "$BODY" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
    info "    Customer ID: $CUSTOMER_ID"
    show_response "$BODY"
else
    fail "HTTP $HTTP_CODE"
    echo "    Response: $BODY"
fi

# Test 2.2: Get Customer
if [ -n "$CUSTOMER_ID" ]; then
    log_test "Get customer details"
    RESPONSE=$(curl -s -w "\n%{http_code}" \
        --request GET \
        --url "${BRIDGE_BASE_URL}/v0/customers/${CUSTOMER_ID}" \
        --header "Content-Type: application/json" \
        --header "Api-Key: ${BRIDGE_API_KEY}" 2>/dev/null)

    HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
    BODY=$(echo "$RESPONSE" | sed '$d')

    if [ "$HTTP_CODE" -eq 200 ]; then
        pass
        STATUS=$(echo "$BODY" | grep -o '"status":"[^"]*"' | head -1 | cut -d'"' -f4)
        info "    Customer Status: $STATUS"
        show_response "$BODY"
    else
        fail "HTTP $HTTP_CODE"
    fi
fi

# ============================================
# SECTION 3: KYC Flow
# ============================================
echo ""
echo "─────────────────────────────────────────"
echo "3. KYC Flow Tests"
echo "─────────────────────────────────────────"

if [ -n "$CUSTOMER_ID" ]; then
    # Test 3.1: Get KYC Link
    log_test "Get KYC link"
    RESPONSE=$(curl -s -w "\n%{http_code}" \
        --request GET \
        --url "${BRIDGE_BASE_URL}/v0/customers/${CUSTOMER_ID}/kyc_link" \
        --header "Content-Type: application/json" \
        --header "Api-Key: ${BRIDGE_API_KEY}" 2>/dev/null)

    HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
    BODY=$(echo "$RESPONSE" | sed '$d')

    if [ "$HTTP_CODE" -eq 200 ]; then
        pass
        KYC_LINK=$(echo "$BODY" | grep -o '"kyc_link":"[^"]*"' | head -1 | cut -d'"' -f4)
        if [ -n "$KYC_LINK" ]; then
            info "    KYC Link: ${KYC_LINK:0:50}..."
        fi
        show_response "$BODY"
    else
        skip "HTTP $HTTP_CODE (may require additional setup)"
    fi

    # Test 3.2: Get TOS Link
    log_test "Get Terms of Service link"
    RESPONSE=$(curl -s -w "\n%{http_code}" \
        --request GET \
        --url "${BRIDGE_BASE_URL}/v0/customers/${CUSTOMER_ID}/tos_link" \
        --header "Content-Type: application/json" \
        --header "Api-Key: ${BRIDGE_API_KEY}" 2>/dev/null)

    HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
    BODY=$(echo "$RESPONSE" | sed '$d')

    if [ "$HTTP_CODE" -eq 200 ]; then
        pass
        show_response "$BODY"
    else
        skip "HTTP $HTTP_CODE"
    fi
else
    skip "No customer ID available"
    skip "No customer ID available"
fi

# ============================================
# SECTION 4: Virtual Accounts
# ============================================
echo ""
echo "─────────────────────────────────────────"
echo "4. Virtual Account Tests"
echo "─────────────────────────────────────────"

if [ -n "$CUSTOMER_ID" ]; then
    # Test 4.1: List Virtual Accounts
    log_test "List virtual accounts"
    RESPONSE=$(curl -s -w "\n%{http_code}" \
        --request GET \
        --url "${BRIDGE_BASE_URL}/v0/customers/${CUSTOMER_ID}/virtual_accounts" \
        --header "Content-Type: application/json" \
        --header "Api-Key: ${BRIDGE_API_KEY}" 2>/dev/null)

    HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
    BODY=$(echo "$RESPONSE" | sed '$d')

    if [ "$HTTP_CODE" -eq 200 ]; then
        pass
        show_response "$BODY"
    else
        skip "HTTP $HTTP_CODE (customer may need KYC approval)"
    fi

    # Test 4.2: Create Virtual Account (requires KYC approval)
    log_test "Create virtual account"
    RESPONSE=$(curl -s -w "\n%{http_code}" \
        --request POST \
        --url "${BRIDGE_BASE_URL}/v0/customers/${CUSTOMER_ID}/virtual_accounts" \
        --header "Content-Type: application/json" \
        --header "Api-Key: ${BRIDGE_API_KEY}" \
        --header "Idempotency-Key: va-${IDEMPOTENCY_KEY}" \
        --data "{
            \"source\": {
                \"currency\": \"usd\"
            },
            \"destination\": {
                \"currency\": \"usdc\",
                \"payment_rail\": \"polygon\"
            }
        }" 2>/dev/null)

    HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
    BODY=$(echo "$RESPONSE" | sed '$d')

    if [ "$HTTP_CODE" -eq 201 ] || [ "$HTTP_CODE" -eq 200 ]; then
        pass
        VA_ID=$(echo "$BODY" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
        info "    Virtual Account ID: $VA_ID"
        show_response "$BODY"
    else
        skip "HTTP $HTTP_CODE (requires KYC approval)"
        show_response "$BODY"
    fi
else
    skip "No customer ID available"
    skip "No customer ID available"
fi

# ============================================
# SECTION 5: Wallets
# ============================================
echo ""
echo "─────────────────────────────────────────"
echo "5. Wallet Tests"
echo "─────────────────────────────────────────"

if [ -n "$CUSTOMER_ID" ]; then
    # Test 5.1: List Wallets
    log_test "List wallets"
    RESPONSE=$(curl -s -w "\n%{http_code}" \
        --request GET \
        --url "${BRIDGE_BASE_URL}/v0/customers/${CUSTOMER_ID}/wallets" \
        --header "Content-Type: application/json" \
        --header "Api-Key: ${BRIDGE_API_KEY}" 2>/dev/null)

    HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
    BODY=$(echo "$RESPONSE" | sed '$d')

    if [ "$HTTP_CODE" -eq 200 ]; then
        pass
        show_response "$BODY"
    else
        skip "HTTP $HTTP_CODE"
    fi
else
    skip "No customer ID available"
fi

# ============================================
# SECTION 6: Transfers
# ============================================
echo ""
echo "─────────────────────────────────────────"
echo "6. Transfer Tests"
echo "─────────────────────────────────────────"

if [ -n "$CUSTOMER_ID" ]; then
    # Test 6.1: List Transfers
    log_test "List transfers"
    RESPONSE=$(curl -s -w "\n%{http_code}" \
        --request GET \
        --url "${BRIDGE_BASE_URL}/v0/transfers?on_behalf_of=${CUSTOMER_ID}&limit=5" \
        --header "Content-Type: application/json" \
        --header "Api-Key: ${BRIDGE_API_KEY}" 2>/dev/null)

    HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
    BODY=$(echo "$RESPONSE" | sed '$d')

    if [ "$HTTP_CODE" -eq 200 ]; then
        pass
        show_response "$BODY"
    else
        skip "HTTP $HTTP_CODE"
    fi
else
    skip "No customer ID available"
fi

# ============================================
# SECTION 7: Webhooks Configuration
# ============================================
echo ""
echo "─────────────────────────────────────────"
echo "7. Webhook Configuration Tests"
echo "─────────────────────────────────────────"

# Test 7.1: List Webhooks
log_test "List configured webhooks"
RESPONSE=$(curl -s -w "\n%{http_code}" \
    --request GET \
    --url "${BRIDGE_BASE_URL}/v0/webhooks" \
    --header "Content-Type: application/json" \
    --header "Api-Key: ${BRIDGE_API_KEY}" 2>/dev/null)

HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | sed '$d')

if [ "$HTTP_CODE" -eq 200 ]; then
    pass
    WEBHOOK_COUNT=$(echo "$BODY" | grep -o '"id"' | wc -l | tr -d ' ')
    info "    Configured webhooks: $WEBHOOK_COUNT"
    show_response "$BODY"
else
    fail "HTTP $HTTP_CODE"
fi

# ============================================
# Summary
# ============================================
echo ""
echo "=========================================="
echo "  Test Summary"
echo "=========================================="
echo -e "  ${GREEN}Passed:${NC}  $PASSED"
echo -e "  ${RED}Failed:${NC}  $FAILED"
echo -e "  ${YELLOW}Skipped:${NC} $SKIPPED"
echo "=========================================="

if [ $FAILED -gt 0 ]; then
    echo ""
    echo -e "${RED}Some tests failed. Check the output above for details.${NC}"
    exit 1
fi

echo ""
echo -e "${GREEN}Bridge API connectivity verified!${NC}"
echo ""
echo "Next Steps:"
echo "  1. Configure webhooks in Bridge Dashboard:"
echo "     https://dashboard.bridge.xyz/webhooks"
echo ""
echo "  2. Set webhook URL to:"
echo "     https://your-domain.com/api/v1/webhooks/bridge"
echo ""
echo "  3. Enable these event categories:"
echo "     - customer"
echo "     - kyc_link"
echo "     - transfer"
echo "     - virtual_account.activity"
echo "     - card_account"
echo "     - card_transaction"
echo ""
echo "  4. For local testing, use ngrok:"
echo "     ngrok http 8080"
echo ""
