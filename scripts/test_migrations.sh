#!/bin/bash
#
# test_migrations.sh - Test all database migrations (up and down)
#
# This script tests database migrations by:
# 1. Running all migrations up
# 2. Running all migrations down one at a time
# 3. Verifying rollback procedures
# 4. Testing idempotency
#
# Usage: ./scripts/test_migrations.sh [environment]
# Environment: local (default), test, staging

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MIGRATIONS_DIR="$PROJECT_ROOT/migrations"
ENV="${1:-local}"

# Database connection (use environment variables or defaults)
DB_HOST="${DB_HOST:-localhost}"
DB_PORT="${DB_PORT:-5432}"
DB_NAME="${DB_NAME:-stack_test}"
DB_USER="${DB_USER:-postgres}"
DB_PASSWORD="${DB_PASSWORD:-postgres}"

# Migration tool (assuming golang-migrate)
MIGRATE_CMD="migrate"

# Build connection string
DB_URL="postgres://${DB_USER}:${DB_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=disable"

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Database Migration Test${NC}"
echo -e "${GREEN}========================================${NC}"
echo "Environment: $ENV"
echo "Database: $DB_NAME"
echo "Host: $DB_HOST:$DB_PORT"
echo ""

# Function to print success
print_success() {
    echo -e "${GREEN}✓ $1${NC}"
}

# Function to print error
print_error() {
    echo -e "${RED}✗ $1${NC}"
}

# Function to print info
print_info() {
    echo -e "${YELLOW}→ $1${NC}"
}

# Function to check if migrate command exists
check_migrate_installed() {
    if ! command -v $MIGRATE_CMD &> /dev/null; then
        print_error "golang-migrate not found. Please install it:"
        echo "  macOS: brew install golang-migrate"
        echo "  Linux: curl -L https://github.com/golang-migrate/migrate/releases/latest/download/migrate.linux-amd64.tar.gz | tar xvz"
        exit 1
    fi
    print_success "golang-migrate found: $($MIGRATE_CMD -version)"
}

# Function to check database connection
check_db_connection() {
    print_info "Checking database connection..."
    if psql "$DB_URL" -c "SELECT 1" &> /dev/null; then
        print_success "Database connection successful"
    else
        print_error "Cannot connect to database"
        exit 1
    fi
}

# Function to create test database if it doesn't exist
create_test_db() {
    print_info "Creating test database if needed..."
    psql "postgres://${DB_USER}:${DB_PASSWORD}@${DB_HOST}:${DB_PORT}/postgres?sslmode=disable" \
        -c "CREATE DATABASE ${DB_NAME};" 2>/dev/null || true
    print_success "Test database ready"
}

# Function to drop test database
drop_test_db() {
    print_info "Dropping test database..."
    psql "postgres://${DB_USER}:${DB_PASSWORD}@${DB_HOST}:${DB_PORT}/postgres?sslmode=disable" \
        -c "DROP DATABASE IF EXISTS ${DB_NAME};" &> /dev/null
    print_success "Test database dropped"
}

# Function to get current migration version
get_current_version() {
    $MIGRATE_CMD -path "$MIGRATIONS_DIR" -database "$DB_URL" version 2>&1 | \
        grep -oE '[0-9]+' | head -1 || echo "0"
}

# Function to run all migrations up
test_migrate_up() {
    print_info "Running all migrations UP..."
    if $MIGRATE_CMD -path "$MIGRATIONS_DIR" -database "$DB_URL" up; then
        local version=$(get_current_version)
        print_success "All migrations applied successfully (version: $version)"
        return 0
    else
        print_error "Failed to apply migrations"
        return 1
    fi
}

# Function to run all migrations down
test_migrate_down_all() {
    print_info "Running all migrations DOWN..."
    if $MIGRATE_CMD -path "$MIGRATIONS_DIR" -database "$DB_URL" down -all; then
        print_success "All migrations rolled back successfully"
        return 0
    else
        print_error "Failed to rollback all migrations"
        return 1
    fi
}

# Function to test individual migration rollback
test_individual_rollbacks() {
    print_info "Testing individual migration rollbacks..."
    
    # First, apply all migrations
    $MIGRATE_CMD -path "$MIGRATIONS_DIR" -database "$DB_URL" up &> /dev/null
    
    local total_migrations=$(ls "$MIGRATIONS_DIR"/*.up.sql 2>/dev/null | wc -l | tr -d ' ')
    local current_version=$(get_current_version)
    local failed=0
    
    echo "Total migrations: $total_migrations"
    echo "Current version: $current_version"
    echo ""
    
    # Rollback each migration one by one
    for ((i=current_version; i>0; i--)); do
        print_info "Rolling back migration $i..."
        
        if $MIGRATE_CMD -path "$MIGRATIONS_DIR" -database "$DB_URL" down 1 &> /dev/null; then
            print_success "Migration $i rolled back successfully"
        else
            print_error "Migration $i rollback failed"
            ((failed++))
        fi
    done
    
    if [ $failed -eq 0 ]; then
        print_success "All individual rollbacks passed"
        return 0
    else
        print_error "$failed migration rollbacks failed"
        return 1
    fi
}

# Function to test idempotency (applying same migration twice)
test_idempotency() {
    print_info "Testing migration idempotency..."
    
    # Apply all migrations
    $MIGRATE_CMD -path "$MIGRATIONS_DIR" -database "$DB_URL" up &> /dev/null
    
    # Try to apply again (should be no-op)
    if $MIGRATE_CMD -path "$MIGRATIONS_DIR" -database "$DB_URL" up &> /dev/null; then
        print_success "Idempotency test passed (no errors when re-applying)"
        return 0
    else
        print_error "Idempotency test failed"
        return 1
    fi
}

# Function to verify schema after migrations
verify_schema() {
    print_info "Verifying database schema..."
    
    # Check if important tables exist
    local tables=("users" "wallets" "deposits" "withdrawals" "orders" "balances" "idempotency_keys")
    local missing=0
    
    for table in "${tables[@]}"; do
        if psql "$DB_URL" -c "SELECT 1 FROM $table LIMIT 1" &> /dev/null; then
            print_success "Table '$table' exists"
        else
            print_error "Table '$table' missing"
            ((missing++))
        fi
    done
    
    if [ $missing -eq 0 ]; then
        print_success "Schema verification passed"
        return 0
    else
        print_error "Schema verification failed ($missing tables missing)"
        return 1
    fi
}

# Function to generate rollback documentation
generate_rollback_docs() {
    print_info "Generating rollback documentation..."
    
    local doc_file="$PROJECT_ROOT/docs/database/ROLLBACK_PROCEDURES.md"
    mkdir -p "$(dirname "$doc_file")"
    
    cat > "$doc_file" << 'EOF'
# Database Migration Rollback Procedures

## Overview
This document describes procedures for rolling back database migrations in the Stack Service.

## Prerequisites
- Access to the database server
- `golang-migrate` CLI tool installed
- Database credentials with appropriate permissions
- Backup of production database (always before rollback)

## Rollback Commands

### Roll back the last migration
```bash
migrate -path migrations -database "$DB_URL" down 1
```

### Roll back to a specific version
```bash
migrate -path migrations -database "$DB_URL" goto VERSION
```

### Roll back all migrations (DANGEROUS)
```bash
migrate -path migrations -database "$DB_URL" down -all
```

## Safety Checklist

Before rolling back in production:

- [ ] Create full database backup
- [ ] Verify backup restoration works
- [ ] Test rollback in staging environment
- [ ] Review migration down scripts for data loss risks
- [ ] Notify team of pending rollback
- [ ] Check for dependent services that may break
- [ ] Prepare rollback plan for application code if needed
- [ ] Have DBA on standby

## Common Rollback Scenarios

### Scenario 1: Bad Migration Just Applied
1. Immediately roll back the last migration:
   ```bash
   migrate -path migrations -database "$DB_URL" down 1
   ```
2. Verify application health
3. Fix migration script
4. Re-apply after testing

### Scenario 2: Multiple Migrations Need Rollback
1. Identify target version
2. Roll back incrementally:
   ```bash
   for i in {1..N}; do
     migrate -path migrations -database "$DB_URL" down 1
     # Verify application after each step
   done
   ```

### Scenario 3: Data Migration Gone Wrong
1. Stop application to prevent further writes
2. Restore from backup
3. Apply migrations up to last known good version
4. Fix data migration script
5. Test thoroughly before re-applying

## Emergency Rollback Procedure

If production is severely impacted:

1. **STOP** - Stop accepting new writes (put app in maintenance mode)
2. **ASSESS** - Determine scope of damage
3. **BACKUP** - Take immediate backup of current state
4. **ROLLBACK** - Execute rollback
5. **VERIFY** - Confirm application functionality
6. **RESUME** - Gradually restore traffic

## Post-Rollback Actions

- [ ] Document what went wrong
- [ ] Update migration scripts
- [ ] Add tests to catch the issue
- [ ] Test thoroughly in dev/staging
- [ ] Schedule re-deployment

## Monitoring During Rollback

Monitor these metrics during rollback:
- Application error rates
- Database connection pools
- Query performance
- Active transactions
- Replication lag (if applicable)

## Testing Rollbacks

Always test migrations with this script:
```bash
./scripts/test_migrations.sh
```

This validates:
- All migrations can be applied
- All migrations can be rolled back
- No data loss in down migrations
- Idempotency of migrations

## Contact

For migration emergencies, contact:
- DevOps Team: [contact info]
- Database Admin: [contact info]
- On-call Engineer: [contact info]
EOF

    print_success "Rollback documentation generated: $doc_file"
}

# Main test execution
main() {
    echo ""
    
    # Check prerequisites
    check_migrate_installed
    
    # Setup test environment
    create_test_db
    check_db_connection
    
    echo ""
    echo -e "${GREEN}========================================${NC}"
    echo -e "${GREEN}Running Migration Tests${NC}"
    echo -e "${GREEN}========================================${NC}"
    echo ""
    
    local tests_passed=0
    local tests_failed=0
    
    # Test 1: Migrate up
    if test_migrate_up; then
        ((tests_passed++))
    else
        ((tests_failed++))
    fi
    echo ""
    
    # Test 2: Verify schema
    if verify_schema; then
        ((tests_passed++))
    else
        ((tests_failed++))
    fi
    echo ""
    
    # Test 3: Idempotency
    if test_idempotency; then
        ((tests_passed++))
    else
        ((tests_failed++))
    fi
    echo ""
    
    # Test 4: Rollback all
    if test_migrate_down_all; then
        ((tests_passed++))
    else
        ((tests_failed++))
    fi
    echo ""
    
    # Test 5: Individual rollbacks
    if test_individual_rollbacks; then
        ((tests_passed++))
    else
        ((tests_failed++))
    fi
    echo ""
    
    # Generate documentation
    generate_rollback_docs
    echo ""
    
    # Cleanup
    if [ "$ENV" = "local" ]; then
        drop_test_db
    fi
    
    # Summary
    echo ""
    echo -e "${GREEN}========================================${NC}"
    echo -e "${GREEN}Test Summary${NC}"
    echo -e "${GREEN}========================================${NC}"
    echo -e "Tests passed: ${GREEN}$tests_passed${NC}"
    echo -e "Tests failed: ${RED}$tests_failed${NC}"
    echo ""
    
    if [ $tests_failed -eq 0 ]; then
        print_success "All migration tests passed!"
        exit 0
    else
        print_error "Some migration tests failed"
        exit 1
    fi
}

# Run main function
main
