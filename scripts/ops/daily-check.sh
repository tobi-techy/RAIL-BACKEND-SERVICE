#!/bin/bash

# Daily operational check script for RAIL Backend
# Usage: ./scripts/ops/daily-check.sh

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo "=============================================="
echo "RAIL Backend Daily Operations Check"
echo "Date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "=============================================="
echo ""

PASS=0
FAIL=0

check_result() {
    local check_name=$1
    local result=$2
    if [ "$result" -eq 0 ]; then
        echo -e "${GREEN}✓${NC} $check_name"
        PASS=$((PASS + 1))
    else
        echo -e "${RED}✗${NC} $check_name"
        FAIL=$((FAIL + 1))
    fi
}

check_health_endpoint() {
    local url=$1
    local name=$2
    local response=$(curl -s -o /dev/null -w "%{http_code}" "$url")
    if [ "$response" -eq 200 ]; then
        check_result "$name" 0
    else
        check_result "$name" 1
    fi
}

# 1. Service Health Checks
echo "=== Service Health ==="

check_health_endpoint "https://api.rail-service.com/health" "Primary health endpoint"
check_health_endpoint "https://api.rail-service.com/ready" "Primary ready endpoint"

# 2. ECS Task Count
echo ""
echo "=== ECS Tasks ==="

PRIMARY_COUNT=$(aws ecs list-tasks \
    --cluster rail-backend-us-east-1 \
    --desired-status RUNNING \
    --query 'taskArns | length(@)' \
    --output text 2>/dev/null || echo "0")

if [ "$PRIMARY_COUNT" -ge 4 ]; then
    echo -e "${GREEN}✓${NC} Primary region: $PRIMARY_COUNT tasks running"
    PASS=$((PASS + 1))
else
    echo -e "${RED}✗${NC} Primary region: only $PRIMARY_COUNT tasks (expected 4+)"
    FAIL=$((FAIL + 1))
fi

# 3. RDS Status
echo ""
echo "=== Database Status ==="

DB_STATUS=$(aws rds describe-db-instances \
    --db-instance-identifier rail-backend-prod \
    --query 'DBInstances[0].DBInstanceStatus' \
    --output text 2>/dev/null || echo "unknown")

if [ "$DB_STATUS" == "available" ]; then
    check_result "RDS status: $DB_STATUS" 0
else
    check_result "RDS status: $DB_STATUS" 1
fi

REPLICA_LAG=$(aws rds describe-db-instances \
    --db-instance-identifier rail-backend-prod \
    --query 'DBInstances[0].SecondaryStatus' \
    --output text 2>/dev/null || echo "N/A")

echo "  Replica status: $REPLICA_LAG"

# 4. ElastiCache Status
echo ""
echo "=== Redis Status ==="

CACHE_STATUS=$(aws elasticache describe-cache-clusters \
    --cache-cluster-id rail-backend-cluster \
    --query 'CacheClusters[0].CacheClusterStatus' \
    --output text 2>/dev/null || echo "unknown")

if [ "$CACHE_STATUS" == "available" ]; then
    check_result "ElastiCache status: $CACHE_STATUS" 0
else
    check_result "ElastiCache status: $CACHE_STATUS" 1
fi

# 5. ALB Health
echo ""
echo "=== Load Balancer Health ==="

TG_HEALTH=$(aws elbv2 describe-target-health \
    --target-group-arn "$TARGET_GROUP_ARN" \
    --query 'TargetHealthDescriptions[?TargetHealth.State==`healthy`] | length(@)' \
    --output text 2>/dev/null || echo "0")

if [ "$TG_HEALTH" -ge 4 ]; then
    check_result "ALB targets healthy: $TG_HEALTH" 0
else
    check_result "ALB targets healthy: $TG_HEALTH (expected 4+)" 1
fi

# 6. Recent Errors
echo ""
echo "=== Recent Errors (last hour) ==="

ERROR_COUNT=$(aws logs filter-log-events \
    --log-group-name /ecs/rail-backend-us-east-1 \
    --start-time $(($(date +%s) - 3600))000 \
    --filter-pattern "ERROR" \
    --query 'events | length(@)' \
    --output text 2>/dev/null || echo "0")

echo "  Error count (us-east-1, 1h): $ERROR_COUNT"

if [ "$ERROR_COUNT" -lt 10 ]; then
    echo -e "${GREEN}✓${NC} Error count within normal range"
    PASS=$((PASS + 1))
else
    echo -e "${YELLOW}!${NC} Elevated error count - investigate"
    FAIL=$((FAIL + 1))
fi

# 7. Recent Deployments
echo ""
echo "=== Recent Deployments ==="

LATEST_DEPLOY=$(aws ecs list-task-definitions \
    --family-prefix rail-backend \
    --status ACTIVE \
    --query 'taskDefinitionArns[-1]' \
    --output text 2>/dev/null | cut -d':' -f11 || echo "unknown")

echo "  Latest task definition: $LATEST_DEPLOY"

# Summary
echo ""
echo "=============================================="
echo "Summary: $PASS passed, $FAIL failed"
echo "=============================================="

if [ "$FAIL" -gt 0 ]; then
    echo -e "${YELLOW}⚠${NC} Some checks failed - review above"
    exit 1
else
    echo -e "${GREEN}✓${NC} All checks passed"
    exit 0
fi
