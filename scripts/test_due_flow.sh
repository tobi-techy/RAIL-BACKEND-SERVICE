#!/bin/bash

# STACK Platform - Due Integration Flow Test Script
# Tests the complete user journey from registration to USDC deposits with auto off-ramping

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
BASE_URL="${API_BASE_URL:-http://localhost:8080}"
TEST_EMAIL="taylor$(date +%s)@example.com"
TEST_PASSWORD="SecurePass123!"
TEST_PASSCODE="123456"

# Function to print colored output
print_step() {
    echo -e "${GREEN}[STEP $1]${NC} $2"
}

print_success() {
    echo -e "${GREEN}✓${NC} $1"
}

print_error() {
    echo -e "${RED}✗${NC} $1"
}

print_info() {
    echo -e "${YELLOW}ℹ${NC} $1"
}

# Function to make API calls and extract JSON values
api_call() {
    local method=$1
    local endpoint=$2
    local data=$3
    local auth_header=$4
    
    if [ -n "$auth_header" ]; then
        curl -s -X "$method" "$BASE_URL$endpoint" \
            -H "Content-Type: application/json" \
            -H "Authorization: Bearer $auth_header" \
            -d "$data"
    else
        curl -s -X "$method" "$BASE_URL$endpoint" \
            -H "Content-Type: application/json" \
            -d "$data"
    fi
}

# Check if jq is installed
if ! command -v jq &> /dev/null; then
    print_error "jq is required but not installed. Please install jq first."
    exit 1
fi

echo "========================================="
echo "STACK Platform - Due Integration Test"
echo "========================================="
echo ""

# Step 1: User Registration
print_step 1 "User Registration"
REGISTER_RESPONSE=$(api_call POST "/api/v1/auth/register" "{
    \"email\": \"$TEST_EMAIL\",
    \"password\": \"$TEST_PASSWORD\",
    \"full_name\": \"Taylor Johnson\"
}")

USER_ID=$(echo "$REGISTER_RESPONSE" | jq -r '.user_id')
if [ "$USER_ID" != "null" ] && [ -n "$USER_ID" ]; then
    print_success "User registered: $USER_ID"
else
    print_error "Registration failed"
    echo "$REGISTER_RESPONSE"
    exit 1
fi
echo ""

# Step 2: User Login
print_step 2 "User Login"
LOGIN_RESPONSE=$(api_call POST "/api/v1/auth/login" "{
    \"email\": \"$TEST_EMAIL\",
    \"password\": \"$TEST_PASSWORD\"
}")

ACCESS_TOKEN=$(echo "$LOGIN_RESPONSE" | jq -r '.access_token')
if [ "$ACCESS_TOKEN" != "null" ] && [ -n "$ACCESS_TOKEN" ]; then
    print_success "Login successful"
else
    print_error "Login failed"
    echo "$LOGIN_RESPONSE"
    exit 1
fi
echo ""

# Step 3: Create Passcode
print_step 3 "Create Passcode"
PASSCODE_CREATE_RESPONSE=$(api_call POST "/api/v1/auth/passcode/create" "{
    \"passcode\": \"$TEST_PASSCODE\"
}" "$ACCESS_TOKEN")

if echo "$PASSCODE_CREATE_RESPONSE" | jq -e '.message' > /dev/null; then
    print_success "Passcode created"
else
    print_error "Passcode creation failed"
    echo "$PASSCODE_CREATE_RESPONSE"
    exit 1
fi
echo ""

# Step 4: Verify Passcode
print_step 4 "Verify Passcode"
PASSCODE_VERIFY_RESPONSE=$(api_call POST "/api/v1/auth/passcode/verify" "{
    \"passcode\": \"$TEST_PASSCODE\"
}" "$ACCESS_TOKEN")

if echo "$PASSCODE_VERIFY_RESPONSE" | jq -e '.verified' > /dev/null; then
    print_success "Passcode verified"
else
    print_error "Passcode verification failed"
    echo "$PASSCODE_VERIFY_RESPONSE"
    exit 1
fi
echo ""

# Step 5: Provision Wallets
print_step 5 "Provision Circle Wallets"
PROVISION_RESPONSE=$(api_call POST "/api/v1/wallets/provision" "{
    \"chains\": [\"SOL-DEVNET\"]
}" "$ACCESS_TOKEN")

JOB_ID=$(echo "$PROVISION_RESPONSE" | jq -r '.job.id')
if [ "$JOB_ID" != "null" ] && [ -n "$JOB_ID" ]; then
    print_success "Wallet provisioning initiated: $JOB_ID"
else
    print_error "Wallet provisioning failed"
    echo "$PROVISION_RESPONSE"
    exit 1
fi
echo ""

# Step 6: Wait for Wallet Provisioning
print_step 6 "Waiting for Wallet Provisioning"
print_info "Checking wallet status every 5 seconds..."
MAX_ATTEMPTS=12
ATTEMPT=0

while [ $ATTEMPT -lt $MAX_ATTEMPTS ]; do
    sleep 5
    WALLET_STATUS=$(curl -s -X GET "$BASE_URL/api/v1/wallets/status" \
        -H "Authorization: Bearer $ACCESS_TOKEN")
    
    READY_WALLETS=$(echo "$WALLET_STATUS" | jq -r '.readyWallets')
    
    if [ "$READY_WALLETS" -gt 0 ]; then
        SOL_WALLET=$(echo "$WALLET_STATUS" | jq -r '.walletsByChain."SOL-DEVNET".address')
        print_success "Wallet provisioned: $SOL_WALLET"
        break
    fi
    
    ATTEMPT=$((ATTEMPT + 1))
    print_info "Attempt $ATTEMPT/$MAX_ATTEMPTS - Wallets not ready yet..."
done

if [ $ATTEMPT -eq $MAX_ATTEMPTS ]; then
    print_error "Wallet provisioning timeout"
    exit 1
fi
echo ""

# Step 7: Complete Onboarding
print_step 7 "Complete Onboarding"
ONBOARDING_RESPONSE=$(api_call POST "/api/v1/onboarding/complete" "{
    \"date_of_birth\": \"1995-06-15\",
    \"phone_number\": \"+1234567890\",
    \"address\": {
        \"street\": \"123 Main St\",
        \"city\": \"New York\",
        \"state\": \"NY\",
        \"postal_code\": \"10001\",
        \"country\": \"US\"
    }
}" "$ACCESS_TOKEN")

if echo "$ONBOARDING_RESPONSE" | jq -e '.message' > /dev/null; then
    print_success "Onboarding completed"
else
    print_error "Onboarding failed"
    echo "$ONBOARDING_RESPONSE"
    exit 1
fi
echo ""

# Step 8: Create Due Account
print_step 8 "Create Due Account"
DUE_ACCOUNT_RESPONSE=$(api_call POST "/api/v1/due/account" "{
    \"name\": \"Taylor Johnson\",
    \"email\": \"$TEST_EMAIL\",
    \"country\": \"US\"
}" "$ACCESS_TOKEN")

DUE_ACCOUNT_ID=$(echo "$DUE_ACCOUNT_RESPONSE" | jq -r '.due_account_id')
if [ "$DUE_ACCOUNT_ID" != "null" ] && [ -n "$DUE_ACCOUNT_ID" ]; then
    print_success "Due account created: $DUE_ACCOUNT_ID"
else
    print_error "Due account creation failed"
    echo "$DUE_ACCOUNT_RESPONSE"
    exit 1
fi
echo ""

# Step 9: Get KYC Link
print_step 9 "Get KYC Verification Link"
KYC_RESPONSE=$(curl -s -X GET "$BASE_URL/api/v1/due/kyc-link?due_account_id=$DUE_ACCOUNT_ID" \
    -H "Authorization: Bearer $ACCESS_TOKEN")

KYC_LINK=$(echo "$KYC_RESPONSE" | jq -r '.kyc_link')
if [ "$KYC_LINK" != "null" ] && [ -n "$KYC_LINK" ]; then
    print_success "KYC link retrieved"
    print_info "KYC Link: $KYC_LINK"
    print_info "Please complete KYC verification manually"
else
    print_error "Failed to get KYC link"
    echo "$KYC_RESPONSE"
    exit 1
fi
echo ""

# Step 10: Link Wallet
print_step 10 "Link Circle Wallet to Due"
LINK_WALLET_RESPONSE=$(api_call POST "/api/v1/due/link-wallet" "{
    \"wallet_address\": \"$SOL_WALLET\",
    \"chain\": \"SOL-DEVNET\"
}" "$ACCESS_TOKEN")

if echo "$LINK_WALLET_RESPONSE" | jq -e '.message' > /dev/null; then
    print_success "Wallet linked to Due"
else
    print_error "Wallet linking failed"
    echo "$LINK_WALLET_RESPONSE"
    exit 1
fi
echo ""

# Step 11: Create Virtual Account
print_step 11 "Create USD Virtual Account"
VIRTUAL_ACCOUNT_RESPONSE=$(api_call POST "/api/v1/due/virtual-account" "{
    \"account_number\": \"123456789\",
    \"routing_number\": \"021000021\",
    \"account_name\": \"Taylor Johnson\",
    \"chain\": \"SOL-DEVNET\"
}" "$ACCESS_TOKEN")

DEPOSIT_ADDRESS=$(echo "$VIRTUAL_ACCOUNT_RESPONSE" | jq -r '.deposit_address')
if [ "$DEPOSIT_ADDRESS" != "null" ] && [ -n "$DEPOSIT_ADDRESS" ]; then
    print_success "Virtual account created"
    print_info "USDC Deposit Address: $DEPOSIT_ADDRESS"
else
    print_error "Virtual account creation failed"
    echo "$VIRTUAL_ACCOUNT_RESPONSE"
    exit 1
fi
echo ""

# Summary
echo "========================================="
echo "Test Summary"
echo "========================================="
echo ""
print_success "All steps completed successfully!"
echo ""
echo "User Details:"
echo "  Email: $TEST_EMAIL"
echo "  User ID: $USER_ID"
echo "  Due Account ID: $DUE_ACCOUNT_ID"
echo ""
echo "Wallet Details:"
echo "  Solana Wallet: $SOL_WALLET"
echo "  USDC Deposit Address: $DEPOSIT_ADDRESS"
echo ""
echo "Next Steps:"
echo "  1. Complete KYC verification: $KYC_LINK"
echo "  2. Send USDC to deposit address: $DEPOSIT_ADDRESS"
echo "  3. Monitor webhook for automatic off-ramp"
echo ""
echo "========================================="
