#!/bin/bash

# Simple Performance Test Script
# Simulates k6-like testing using curl and timing

BASE_URL="${BASE_URL:-http://localhost:8080}"
REQUESTS="${REQUESTS:-100}"
CONCURRENT="${CONCURRENT:-5}"

echo "=========================================="
echo "RAIL Backend Performance Test"
echo "=========================================="
echo "Base URL: $BASE_URL"
echo "Total Requests: $REQUESTS"
echo "Concurrent: $CONCURRENT"
echo "=========================================="

mkdir -p test/performance/reports

# Function to test an endpoint
test_endpoint() {
    local endpoint="$1"
    local method="${2:-GET}"
    local data="$3"
    local name="$4"
    local requests=$5
    
    echo "Testing: $name"
    
    local success=0
    local failed=0
    local total_time=0
    
    for i in $(seq 1 $requests); do
        local start=$(date +%s%N)
        
        if [ "$method" = "POST" ]; then
            response=$(curl -s -w "%{http_code},%{time_total}" -X POST \
                -H "Content-Type: application/json" \
                -d "$data" \
                "$BASE_URL$endpoint" 2>/dev/null)
        else
            response=$(curl -s -w "%{http_code},%{time_total}" \
                "$BASE_URL$endpoint" 2>/dev/null)
        fi
        
        local end=$(date +%s%N)
        local duration=$(echo "scale=3; ($end - $start) / 1000000000" | bc)
        
        if [[ "$response" =~ ^200, ]]; then
            ((success++))
            total_time=$(echo "$total_time + $duration" | bc)
        else
            ((failed++))
        fi
        
        if [ $((i % 10)) -eq 0 ]; then
            echo -n "."
        fi
    done
    
    echo ""
    
    if [ $success -gt 0 ]; then
        local avg=$(echo "scale=3; $total_time / $success" | bc)
        echo "✅ $name: $success/$requests successful, ${avg}s avg"
    else
        echo "❌ $name: $failed/$requests failed"
    fi
    
    # Save result
    cat > "test/performance/reports/${name// /_}.json" << EOF
{
  "endpoint": "$endpoint",
  "method": "$method",
  "requests": $requests,
  "successful": $success,
  "failed": $failed,
  "success_rate": $(echo "scale=2; $success * 100 / $requests" | bc),
  "avg_response_time": ${avg:-0},
  "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
EOF
}

# Test health endpoint (should be fast)
test_endpoint "/health" "GET" "" "Health Endpoint" 20

# Test ready endpoint
test_endpoint "/ready" "GET" "" "Ready Endpoint" 20

# Test registration (rate limited)
test_endpoint "/api/v1/auth/register" "POST" \
    '{"email":"loadtest@example.com","password":"Test123!@#","username":"loadtest"}' \
    "Registration Endpoint" 10

# Test login
test_endpoint "/api/v1/auth/login" "POST" \
    '{"email":"test@rail.app","password":"Dev123!@#test"}' \
    "Login Endpoint" 10

echo "=========================================="
echo "Performance Test Complete!"
echo "Results saved to: test/performance/reports/"
echo "=========================================="