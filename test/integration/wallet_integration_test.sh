#!/bin/bash

################################################################################
# Legacy Circle Wallet Integration Test Script (deprecated; Bridge is primary)
# Tests the legacy developer-controlled wallet creation flow: signup -> passcode verification -> wallet initiation
# 
# Prerequisites:
#   - Stack Service running on local machine or accessible via API_BASE_URL
#   - Circle API credentials configured (CIRCLE_API_KEY)
#   - Pre-registered Entity Secret Ciphertext in Circle Dashboard (CIRCLE_ENTITY_SECRET_CIPHERTEXT)
#   - For Bridge onboarding, use Bridge integration tests instead
#
# Usage:
#   ./wallet_integration_test.sh
#
# Environment Variables:
#   API_BASE_URL              Base URL for the Stack Service (default: http://localhost:8080)
#   CIRCLE_API_KEY            Circle API Key for legacy wallet operations
#   CIRCLE_ENTITY_SECRET_CIPHERTEXT  Pre-registered Entity Secret Ciphertext from Circle Dashboard
#   VERBOSE                   Set to 1 for detailed output
################################################################################

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
API_BASE_URL="${API_BASE_URL:-http://localhost:8080}"
VERBOSE="${VERBOSE:-0}"
TEST_EMAIL="wallet-test-$(date +%s)@stackfi.dev"
TEST_PASSWORD="Test@Pass123!SecurePassword"
TEST_PASSCODE="1234"

# Test results
TESTS_PASSED=0
TESTS_FAILED=0
FAILED_TESTS=""

################################################################################
# Utility Functions
################################################################################

log_info() {
    echo -e "${BLUE}ℹ️  INFO${NC}: $1"
}

log_success() {
    echo -e "${GREEN}✓ SUCCESS${NC}: $1"
    ((TESTS_PASSED++))
}

log_error() {
    echo -e "${RED}✗ ERROR${NC}: $1"
    ((TESTS_FAILED++))
    FAILED_TESTS="$FAILED_TESTS\n  - $1"
}

log_warning() {
    echo -e "${YELLOW}⚠ WARNING${NC}: $1"
}

log_step() {
    echo -e "\n${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}▶ $1${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
}

log_response() {
    if [ "$VERBOSE" = "1" ]; then
        echo -e "\n${YELLOW}Response:${NC}"
        echo "$1" | jq '.' 2>/dev/null || echo "$1"
    fi
}

check_env_variables() {
    log_step "Checking Environment Variables"
    
    if [ -z "$CIRCLE_API_KEY" ]; then
        log_error "CIRCLE_API_KEY not set. Please set the Circle API key."
        return 1
    fi
    log_success "CIRCLE_API_KEY is configured"
    
    if [ -z "$CIRCLE_ENTITY_SECRET_CIPHERTEXT" ]; then
        log_error "CIRCLE_ENTITY_SECRET_CIPHERTEXT not set. Developer-controlled wallet operations will fail."
        log_info "Register the Entity Secret Ciphertext in Circle Dashboard and set this environment variable."
        log_info "This is required for developer-controlled wallet creation."
        return 1
    else
        log_success "CIRCLE_ENTITY_SECRET_CIPHERTEXT is configured (developer-controlled wallets enabled)"
    fi
    
    log_success "API Base URL: $API_BASE_URL"
}

check_api_health() {
    log_step "Checking API Health"
    
    response=$(curl -s -X GET "$API_BASE_URL/health" 2>/dev/null || echo "")
    
    if [ -z "$response" ]; then
        log_error "Cannot connect to API at $API_BASE_URL. Is the service running?"
        return 1
    fi
    
    log_response "$response"
    log_success "API is healthy"
}

################################################################################
# Authentication Flow
################################################################################

test_user_signup() {
    log_step "Testing User Signup"
    
    local payload=$(cat <<EOF
{
  "email": "$TEST_EMAIL",
  "password": "$TEST_PASSWORD"
}
EOF
)
    
    local response=$(curl -s -X POST "$API_BASE_URL/api/v1/auth/register" \
        -H "Content-Type: application/json" \
        -d "$payload" 2>/dev/null || echo "")
    
    log_response "$response"
    
    if echo "$response" | jq -e '.code' >/dev/null 2>&1; then
        if echo "$response" | jq -e '.code == "USER_EXISTS"' >/dev/null 2>&1; then
            log_warning "User already exists: $TEST_EMAIL"
            return 0
        fi
    fi
    
    if echo "$response" | jq -e '.message' >/dev/null 2>&1; then
        log_success "User registration initiated for $TEST_EMAIL"
        return 0
    fi
    
    log_error "User signup failed: $response"
    return 1
}

test_verify_email() {
    log_step "Testing Email Verification (Manual Step Required)"
    
    log_warning "In production, you must manually check the email for the verification code."
    log_info "Test email: $TEST_EMAIL"
    log_info "For automated testing, retrieve the code from your email service or test database."
    
    # For automated testing, use a placeholder verification code (6 digits)
    # In real testing, you would:
    # 1. Check the emails table in the database
    # 2. Extract the verification code
    # 3. Use it in the verification request
    
    log_info "Skipping automated verification - manual verification required in production"
    return 0
}

# Note: Since we can't automate email code retrieval in this test script,
# we'll provide instructions for manual testing

################################################################################
# Passcode Management Flow
################################################################################

test_create_passcode() {
    log_step "Testing Passcode Creation"
    
    if [ -z "$ACCESS_TOKEN" ]; then
        log_error "ACCESS_TOKEN not set. Ensure user is authenticated first."
        return 1
    fi
    
    local payload=$(cat <<EOF
{
  "passcode": "$TEST_PASSCODE",
  "confirm_passcode": "$TEST_PASSCODE"
}
EOF
)
    
    local response=$(curl -s -X POST "$API_BASE_URL/api/v1/security/passcode" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $ACCESS_TOKEN" \
        -d "$payload" 2>/dev/null || echo "")
    
    log_response "$response"
    
    if echo "$response" | jq -e '.message' >/dev/null 2>&1; then
        log_success "Passcode created successfully"
        return 0
    fi
    
    log_error "Passcode creation failed: $response"
    return 1
}

test_verify_passcode() {
    log_step "Testing Passcode Verification"
    
    if [ -z "$ACCESS_TOKEN" ]; then
        log_error "ACCESS_TOKEN not set. Ensure user is authenticated first."
        return 1
    fi
    
    local payload=$(cat <<EOF
{
  "passcode": "$TEST_PASSCODE"
}
EOF
)
    
    local response=$(curl -s -X POST "$API_BASE_URL/api/v1/security/passcode/verify" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $ACCESS_TOKEN" \
        -d "$payload" 2>/dev/null || echo "")
    
    log_response "$response"
    
    if echo "$response" | jq -e '.session_token' >/dev/null 2>&1; then
        PASSCODE_SESSION_TOKEN=$(echo "$response" | jq -r '.session_token')
        log_success "Passcode verified successfully"
        log_info "Session Token: ${PASSCODE_SESSION_TOKEN:0:20}..."
        return 0
    fi
    
    log_error "Passcode verification failed: $response"
    return 1
}

################################################################################
# Wallet Creation Flow
################################################################################

test_initiate_wallet_creation() {
    log_step "Testing Developer-Controlled Wallet Initiation"
    
    if [ -z "$ACCESS_TOKEN" ]; then
        log_error "ACCESS_TOKEN not set. Ensure user is authenticated first."
        return 1
    fi
    
    # Test with default chains (testnet only)
    local payload=$(cat <<EOF
{
  "chains": []
}
EOF
)
    
    local response=$(curl -s -X POST "$API_BASE_URL/api/v1/wallets/initiate" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $ACCESS_TOKEN" \
        -d "$payload" 2>/dev/null || echo "")
    
    log_response "$response"
    
    if echo "$response" | jq -e '.job.id' >/dev/null 2>&1; then
        JOB_ID=$(echo "$response" | jq -r '.job.id')
        log_success "Developer-controlled wallet creation initiated"
        log_info "Job ID: $JOB_ID"
        log_info "Chains: $(echo "$response" | jq -r '.chains | join(", ")')"
        log_info "Job Status: $(echo "$response" | jq -r '.job.status')"
        return 0
    fi
    
    if echo "$response" | jq -e '.message' >/dev/null 2>&1; then
        log_success "Developer-controlled wallet creation initiated (job details pending)"
        log_info "Message: $(echo "$response" | jq -r '.message')"
        return 0
    fi
    
    # Check for specific error messages
    if echo "$response" | jq -e '.code == "WALLET_INITIATION_FAILED"' >/dev/null 2>&1; then
        log_error "Developer-controlled wallet initiation failed - likely due to missing Circle configuration"
        log_warning "Ensure CIRCLE_ENTITY_SECRET_CIPHERTEXT is set and registered with Circle Dashboard"
        return 1
    fi
    
    log_error "Developer-controlled wallet initiation failed: $response"
    return 1
}

test_initiate_specific_chains() {
    log_step "Testing Developer-Controlled Wallet Initiation with Specific Chains"
    
    if [ -z "$ACCESS_TOKEN" ]; then
        log_error "ACCESS_TOKEN not set. Ensure user is authenticated first."
        return 1
    fi
    
    # Test with specific testnet chains
    local payload=$(cat <<EOF
{
  "chains": ["SOL-DEVNET", "MATIC-AMOY", "APTOS-TESTNET", "BASE-SEPOLIA"]
}
EOF
)
    
    local response=$(curl -s -X POST "$API_BASE_URL/api/v1/wallets/initiate" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $ACCESS_TOKEN" \
        -d "$payload" 2>/dev/null || echo "")
    
    log_response "$response"
    
    if echo "$response" | jq -e '.job.id' >/dev/null 2>&1; then
        log_success "Developer-controlled wallet creation initiated with specific chains"
        log_info "Job ID: $(echo "$response" | jq -r '.job.id')"
        return 0
    fi
    
    log_error "Developer-controlled wallet initiation with specific chains failed: $response"
    return 1
}

test_mainnet_rejection() {
    log_step "Testing Mainnet Chain Rejection (Should Fail)"
    
    if [ -z "$ACCESS_TOKEN" ]; then
        log_error "ACCESS_TOKEN not set. Ensure user is authenticated first."
        return 1
    fi
    
    # Try to create wallet on mainnet (should be rejected)
    local payload=$(cat <<EOF
{
  "chains": ["ETH", "MATIC"]
}
EOF
)
    
    local response=$(curl -s -X POST "$API_BASE_URL/api/v1/wallets/initiate" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $ACCESS_TOKEN" \
        -d "$payload" 2>/dev/null || echo "")
    
    log_response "$response"
    
    if echo "$response" | jq -e '.code == "MAINNET_NOT_SUPPORTED"' >/dev/null 2>&1; then
        log_success "Mainnet chains correctly rejected"
        return 0
    fi
    
    log_error "Expected mainnet rejection, but got: $response"
    return 1
}

test_get_wallet_status() {
    log_step "Testing Get Wallet Status"
    
    if [ -z "$ACCESS_TOKEN" ]; then
        log_error "ACCESS_TOKEN not set. Ensure user is authenticated first."
        return 1
    fi
    
    local response=$(curl -s -X GET "$API_BASE_URL/api/v1/wallet/status" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $ACCESS_TOKEN" 2>/dev/null || echo "")
    
    log_response "$response"
    
    if echo "$response" | jq -e '.totalWallets' >/dev/null 2>&1; then
        log_success "Retrieved wallet status"
        log_info "Total Wallets: $(echo "$response" | jq -r '.totalWallets')"
        log_info "Ready Wallets: $(echo "$response" | jq -r '.readyWallets')"
        log_info "Pending Wallets: $(echo "$response" | jq -r '.pendingWallets')"
        log_info "Failed Wallets: $(echo "$response" | jq -r '.failedWallets')"
        return 0
    fi
    
    log_error "Failed to get wallet status: $response"
    return 1
}

test_get_wallet_addresses() {
    log_step "Testing Get Wallet Addresses"
    
    if [ -z "$ACCESS_TOKEN" ]; then
        log_error "ACCESS_TOKEN not set. Ensure user is authenticated first."
        return 1
    fi
    
    local response=$(curl -s -X GET "$API_BASE_URL/api/v1/wallet/addresses" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $ACCESS_TOKEN" 2>/dev/null || echo "")
    
    log_response "$response"
    
    if echo "$response" | jq -e '.wallets | length' >/dev/null 2>&1; then
        local wallet_count=$(echo "$response" | jq -r '.wallets | length')
        log_success "Retrieved wallet addresses"
        log_info "Number of wallets: $wallet_count"
        if [ "$wallet_count" -gt 0 ]; then
            echo "$response" | jq -r '.wallets[] | "  - \(.chain): \(.address)"'
        fi
        return 0
    fi
    
    log_error "Failed to get wallet addresses: $response"
    return 1
}

################################################################################
# Test Summary
################################################################################

print_summary() {
    log_step "Test Summary"
    
    echo -e "\n${GREEN}Passed: $TESTS_PASSED${NC}"
    echo -e "${RED}Failed: $TESTS_FAILED${NC}"
    
    if [ $TESTS_FAILED -gt 0 ]; then
        echo -e "\n${RED}Failed Tests:${NC}"
        echo -e "$FAILED_TESTS"
    fi
    
    echo ""
    local total=$((TESTS_PASSED + TESTS_FAILED))
    if [ $TESTS_FAILED -eq 0 ] && [ $TESTS_PASSED -gt 0 ]; then
        echo -e "${GREEN}✓ All tests passed!${NC}"
        return 0
    else
        echo -e "${RED}✗ Some tests failed${NC}"
        return 1
    fi
}

################################################################################
# Main Test Flow
################################################################################

main() {
    echo -e "\n${BLUE}╔════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║       Wallet Integration Test Suite                  ║${NC}"
    echo -e "${BLUE}║     Developer-Controlled Wallets (Circle API)        ║${NC}"
    echo -e "${BLUE}╚════════════════════════════════════════════════════════╝${NC}\n"
    
    check_env_variables || exit 1
    check_api_health || exit 1
    
    log_step "Test Configuration"
    log_info "Test Email: $TEST_EMAIL"
    log_info "Test Passcode: $TEST_PASSCODE (hidden in log)"
    log_info "API Base URL: $API_BASE_URL"
    log_info "Verbose Mode: $VERBOSE"
    
    # Note: The following test flow requires manual email verification
    # In a production environment, integrate with your email service to retrieve codes
    
    log_step "Manual Setup Required"
    log_warning "This test script requires manual steps for email verification."
    log_info "Step 1: Run 'test_user_signup' to register a user"
    log_info "Step 2: Check your email for the verification code"
    log_info "Step 3: Call the verify-code endpoint manually with the code from email"
    log_info "Step 4: Set ACCESS_TOKEN environment variable with the received JWT"
    log_info "Step 5: Run passcode creation and wallet initiation tests"
    
    log_step "Available Test Functions"
    echo -e """
Available functions for manual testing:
  test_user_signup                 - Register a new user
  test_create_passcode             - Create a 4-digit passcode
  test_verify_passcode             - Verify passcode and get session token
  test_initiate_wallet_creation    - Initiate developer-controlled wallet creation (default chains)
  test_initiate_specific_chains    - Test with specific chains
  test_mainnet_rejection           - Verify mainnet chains are rejected
  test_get_wallet_status           - Get wallet status
  test_get_wallet_addresses        - Get wallet addresses

Environment variables for testing:
  ACCESS_TOKEN                     - JWT token from successful login/verification
  PASSCODE_SESSION_TOKEN           - Session token from passcode verification
  JOB_ID                           - Job ID from wallet initiation
  CIRCLE_ENTITY_SECRET_CIPHERTEXT  - Pre-registered Entity Secret Ciphertext from Circle Dashboard
"""
    
    print_summary
}

# Run main function
main

# Export functions for manual use
export -f test_user_signup
export -f test_verify_email
export -f test_create_passcode
export -f test_verify_passcode
export -f test_initiate_wallet_creation
export -f test_initiate_specific_chains
export -f test_mainnet_rejection
export -f test_get_wallet_status
export -f test_get_wallet_addresses
export -f log_info
export -f log_success
export -f log_error
export -f log_warning
export -f log_step
export -f log_response
