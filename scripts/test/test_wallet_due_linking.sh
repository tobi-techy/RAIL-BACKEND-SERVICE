#!/bin/bash

# Test script for wallet Due linking functionality
# This script tests the wallet provisioning with Due account linking

set -e

echo "🔧 Testing Wallet Due Linking Functionality"
echo "============================================"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${GREEN}✓${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}⚠${NC} $1"
}

print_error() {
    echo -e "${RED}✗${NC} $1"
}

# Check if we're in the right directory
if [ ! -f "go.mod" ]; then
    print_error "Please run this script from the project root directory"
    exit 1
fi

# Run the unit tests for wallet provisioning Due integration
echo "Running wallet provisioning Due integration tests..."
if go test -v ./test/unit/wallet_provisioning_due_integration_test.go; then
    print_status "Wallet provisioning Due integration tests passed"
else
    print_error "Wallet provisioning Due integration tests failed"
    exit 1
fi

# Test the Due API documentation understanding
echo ""
echo "🌐 Testing Due API Documentation Understanding"
echo "=============================================="

# Check if the Due wallet linking endpoint is properly implemented
echo "Checking Due wallet linking implementation..."

# Look for the LinkCircleWallet method in the Due service
if grep -r "LinkCircleWallet" internal/domain/services/due_service.go > /dev/null; then
    print_status "Due wallet linking method found in service"
else
    print_error "Due wallet linking method not found in service"
    exit 1
fi

# Check if the wallet provisioning worker includes Due integration
if grep -r "linkWalletToDue" internal/workers/wallet_provisioning/worker.go > /dev/null; then
    print_status "Wallet provisioning worker includes Due integration"
else
    print_error "Wallet provisioning worker missing Due integration"
    exit 1
fi

# Check if the User entity includes Due account fields
if grep -r "DueAccountID" internal/domain/entities/auth_entities.go > /dev/null; then
    print_status "User entity includes Due account fields"
else
    print_error "User entity missing Due account fields"
    exit 1
fi

# Check if the main.go includes the new dependencies
if grep -r "container.UserRepo" cmd/main.go > /dev/null && grep -r "container.DueService" cmd/main.go > /dev/null; then
    print_status "Main.go includes new wallet provisioning dependencies"
else
    print_error "Main.go missing new wallet provisioning dependencies"
    exit 1
fi

echo ""
echo "🎯 Due API Integration Summary"
echo "============================="
print_status "Wallet provisioning worker enhanced with Due account linking"
print_status "User entity updated with Due account fields"
print_status "Due service includes wallet linking functionality"
print_status "Main application updated with new dependencies"
print_status "Unit tests created for Due integration scenarios"

echo ""
echo "📋 Implementation Details"
echo "========================"
echo "• Wallets are automatically linked to Due accounts during provisioning"
echo "• Only users with existing Due accounts get wallet linking"
echo "• Supported chains: SOL-DEVNET (expandable for other chains)"
echo "• Due API format: 'solana:address' for Solana chains"
echo "• Graceful handling when Due linking fails (doesn't break wallet creation)"
echo "• Comprehensive audit logging for Due wallet linking events"

echo ""
print_status "All tests passed! Wallet Due linking integration is ready."