#!/bin/bash

# Database Performance Benchmark Script
# This script runs various database benchmarks to assess performance

set -e

# Configuration
DB_HOST="${DB_HOST:-localhost}"
DB_PORT="${DB_PORT:-5432}"
DB_NAME="${DB_NAME:-rail_service}"
DB_USER="${DB_USER:-postgres}"
DB_PASSWORD="${DB_PASSWORD:-postgres}"
OUTPUT_DIR="${OUTPUT_DIR:-test/performance/reports}"
ITERATIONS="${ITERATIONS:-1000}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo "=========================================="
echo "Database Performance Benchmark"
echo "=========================================="
echo "Host: $DB_HOST:$DB_PORT"
echo "Database: $DB_NAME"
echo "Iterations: $ITERATIONS"
echo "Output: $OUTPUT_DIR"
echo "=========================================="

mkdir -p "$OUTPUT_DIR"

# Function to run a benchmark and capture results
run_benchmark() {
    local name=$1
    local query=$2
    local start_time=$(date +%s%N)
    
    echo -e "${YELLOW}Running: $name${NC}"
    
    # Run the query multiple times and measure
    local times=()
    for i in $(seq 1 $ITERATIONS); do
        local iter_start=$(date +%s%N)
        PGPASSWORD="$DB_PASSWORD" psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -c "$query" > /dev/null 2>&1
        local iter_end=$(date +%s%N)
        local duration=$(( (iter_end - iter_start) / 1000000 )) # Convert to ms
        times+=($duration)
    done
    
    local end_time=$(date +%s%N)
    local total_duration=$(( (end_time - start_time) / 1000000000 )) # Convert to seconds
    
    # Calculate statistics
    local sum=0
    local min=${times[0]}
    local max=0
    for t in "${times[@]}"; do
        sum=$((sum + t))
        if [ $t -lt $min ]; then min=$t; fi
        if [ $t -gt $max ]; then max=$t; fi
    done
    local avg=$((sum / ${#times[@]}))
    
    echo -e "${GREEN}Results for $name:${NC}"
    echo "  Total iterations: $ITERATIONS"
    echo "  Total time: ${total_duration}s"
    echo "  Min: ${min}ms"
    echo "  Max: ${max}ms"
    echo "  Avg: ${avg}ms"
    echo "  TPS: $(echo "scale=2; $ITERATIONS / $total_duration" | bc)"
    echo ""
    
    # Save to JSON
    cat > "$OUTPUT_DIR/db_benchmark_${name// /_}.json" << EOF
{
  "name": "$name",
  "iterations": $ITERATIONS,
  "total_time_seconds": $total_duration,
  "min_ms": $min,
  "max_ms": $max,
  "avg_ms": $avg,
  "tps": $(echo "scale=2; $ITERATIONS / $total_duration" | bc),
  "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
EOF
}

# Check if pgbench is available
if command -v pgbench &> /dev/null; then
    echo -e "${YELLOW}Running pgbench benchmarks...${NC}"
    
    # Initialize pgbench
    echo "Initializing pgbench..."
    PGPASSWORD="$DB_PASSWORD" pgbench -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -i > /dev/null 2>&1
    
    # Run TPC-B like benchmark
    echo "Running TPC-B benchmark..."
    PGPASSWORD="$DB_PASSWORD" pgbench -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -c 10 -T 60 -P 1 \
      | tee "$OUTPUT_DIR/pgbench_tpcb.log"
    
    # Custom read-only benchmark
    echo "Running read-only benchmark..."
    PGPASSWORD="$DB_PASSWORD" pgbench -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -S -c 10 -T 60 -P 1 \
      | tee "$OUTPUT_DIR/pgbench_readonly.log"
else
    echo -e "${YELLOW}pgbench not found, skipping pgbench benchmarks${NC}"
fi

# Run custom SQL benchmarks
echo "=========================================="
echo "Running custom SQL benchmarks..."
echo "=========================================="

# Simple SELECT benchmark
run_benchmark "Simple SELECT" "SELECT 1"

# User lookup benchmark
run_benchmark "User Lookup by Email" "SELECT * FROM users WHERE email = 'test@example.com' LIMIT 1"

# Balance query benchmark
run_benchmark "Balance Query" "SELECT * FROM balances WHERE user_id = '00000000-0000-0000-0000-000000000001' LIMIT 1"

# Wallet query benchmark
run_benchmark "Wallet Query" "SELECT * FROM wallets WHERE user_id = '00000000-0000-0000-0000-000000000001' LIMIT 10"

# Transaction history benchmark
run_benchmark "Transaction History" "SELECT * FROM transactions WHERE user_id = '00000000-0000-0000-0000-000000000001' ORDER BY created_at DESC LIMIT 50"

# Join query benchmark
run_benchmark "Join Query (User with Balance)" "
SELECT u.id, u.email, b.total_balance 
FROM users u 
LEFT JOIN balances b ON u.id = b.user_id 
WHERE u.id = '00000000-0000-0000-0000-000000000001'"

# Aggregate query benchmark
run_benchmark "Aggregate Query" "
SELECT user_id, COUNT(*), SUM(amount) 
FROM transactions 
WHERE created_at > NOW() - INTERVAL '30 days' 
GROUP BY user_id 
LIMIT 100"

# Generate summary report
echo "=========================================="
echo "Generating summary report..."
echo "=========================================="

cat > "$OUTPUT_DIR/benchmark_summary.json" << EOF
{
  "benchmark_date": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "database": {
    "host": "$DB_HOST",
    "port": $DB_PORT,
    "name": "$DB_NAME",
    "user": "$DB_USER"
  },
  "configuration": {
    "iterations": $ITERATIONS
  },
  "results_directory": "$OUTPUT_DIR"
}
EOF

echo -e "${GREEN}Benchmark complete!${NC}"
echo "Results saved to: $OUTPUT_DIR"
echo ""
echo "To view results:"
echo "  cat $OUTPUT_DIR/db_benchmark_*.json"
echo "  cat $OUTPUT_DIR/pgbench_*.log"
