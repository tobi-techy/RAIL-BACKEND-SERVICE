#!/bin/bash

# Bridge Integration Test Script
# This script tests all Bridge API integration components

echo "🚀 Testing Bridge API Integration..."
echo ""

# Initialize failure tracking
FAILURES=0
declare -A CHECK_RESULTS

# Check if environment variables are set
if [ -z "$BRIDGE_API_KEY" ]; then
    echo "❌ BRIDGE_API_KEY environment variable is required"
    echo "Please set: export BRIDGE_API_KEY=your-bridge-sandbox-api-key"
    exit 1
fi

echo "✅ Environment variables configured"
CHECK_RESULTS["env_vars"]="passed"

# Check if key files exist
echo "📁 Checking for key implementation files..."

files_to_check=(
    "internal/adapters/bridge/adapter.go"
    "internal/adapters/bridge/client.go"
    "internal/adapters/bridge/interface.go"
    "internal/adapters/bridge/types.go"
    "internal/infrastructure/config/config.go"
    "internal/infrastructure/di/bridge_adapters.go"
    "internal/infrastructure/di/container.go"
    ".env.example"
    "test/unit/bridge_adapter_test.go"
    "test/integration/bridge_integration_test.go"
    "test/sandbox/Bridge_Setup.md"
)

missing_files=0

for file in "${files_to_check[@]}"; do
    if [ -f "$file" ]; then
        echo "  ✅ $file"
    else
        echo "  ❌ $file (missing)"
        ((missing_files++))
    fi
done

if [ $missing_files -gt 0 ]; then
    echo ""
    echo "❌ $missing_files files are missing"
    CHECK_RESULTS["impl_files"]="failed ($missing_files missing)"
    ((FAILURES++))
else
    echo ""
    echo "✅ All implementation files present"
    CHECK_RESULTS["impl_files"]="passed"
fi

# Check domain entity updates
echo ""
echo "🏗️ Checking domain entity updates..."

domain_files=(
    "internal/domain/entities/onboarding_entities.go"
    "internal/domain/entities/wallet_entities.go"
    "internal/domain/entities/virtual_account_entities.go"
    "internal/domain/entities/rail_entities.go"
)

domain_issues=0
for file in "${domain_files[@]}"; do
    if [ -f "$file" ]; then
        if grep -q "BridgeCustomerID\|BridgeWalletID\|BridgeAccountID" "$file"; then
            echo "  ✅ $file (Bridge fields added)"
        else
            echo "  ⚠️  $file (may need Bridge fields)"
            ((domain_issues++))
        fi
    else
        echo "  ❌ $file (missing)"
        ((domain_issues++))
    fi
done

if [ $domain_issues -gt 0 ]; then
    CHECK_RESULTS["domain_entities"]="warning ($domain_issues issues)"
else
    CHECK_RESULTS["domain_entities"]="passed"
fi

# Check configuration
echo ""
echo "⚙️ Configuration check..."

config_issues=0
if grep -q "BRIDGE_API_KEY\|BRIDGE_BASE_URL\|BRIDGE_ENVIRONMENT" ".env.example"; then
    echo "  ✅ Bridge environment variables in .env.example"
else
    echo "  ❌ Bridge environment variables missing from .env.example"
    ((config_issues++))
fi

if grep -q "BridgeConfig\|bridge\." "internal/infrastructure/config/config.go"; then
    echo "  ✅ Bridge configuration in config.go"
else
    echo "  ❌ Bridge configuration missing from config.go"
    ((config_issues++))
fi

if [ $config_issues -gt 0 ]; then
    CHECK_RESULTS["configuration"]="failed ($config_issues issues)"
    ((FAILURES++))
else
    CHECK_RESULTS["configuration"]="passed"
fi

# Check DI integration
echo ""
echo "🔌 DI Container integration check..."

if grep -q "BridgeClient\|BridgeAdapter\|BridgeKYCAdapter\|BridgeFundingAdapter" "internal/infrastructure/di/container.go"; then
    echo "  ✅ Bridge adapters integrated in DI container"
    CHECK_RESULTS["di_container"]="passed"
else
    echo "  ❌ Bridge adapters missing from DI container"
    CHECK_RESULTS["di_container"]="failed"
    ((FAILURES++))
fi

# Summary
echo ""
echo "📋 Bridge Integration Summary:"

print_check() {
    local name="$1"
    local result="${CHECK_RESULTS[$2]}"
    if [[ "$result" == "passed" ]]; then
        echo "  ✅ $name"
    elif [[ "$result" == warning* ]]; then
        echo "  ⚠️  $name: $result"
    else
        echo "  ❌ $name: $result"
    fi
}

print_check "Configuration: Environment variables and defaults" "configuration"
print_check "Client & Adapter: Core Bridge implementation" "impl_files"
print_check "Domain Integration: Entity field mappings" "domain_entities"
print_check "DI Container: Service wiring" "di_container"

echo ""
echo "🎯 Next Steps:"
echo "  1. Set BRIDGE_API_KEY in your environment"
echo "  2. Run: go run test_bridge_connectivity.go"
echo "  3. Run unit tests: go test ./test/unit/bridge_adapter_test.go -v"
echo "  4. Run integration tests: go test -tags=integration ./test/integration/... -v"

echo ""
if [ $FAILURES -gt 0 ]; then
    echo "❌ Bridge API integration has $FAILURES failure(s)"
    exit 1
else
    echo "🎉 Bridge API integration is ready for testing!"
    exit 0
fi