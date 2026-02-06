#!/bin/bash
# Migration Squash Script
# Squashes migrations 001-050 into a single baseline migration
# Run this in a development environment only

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
MIGRATIONS_DIR="$PROJECT_ROOT/migrations"
BACKUP_DIR="$PROJECT_ROOT/migrations_backup_$(date +%Y%m%d_%H%M%S)"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# Check prerequisites
check_prerequisites() {
    if ! command -v psql &> /dev/null; then
        log_error "psql is required but not installed"
        exit 1
    fi
    
    if [ -z "$DATABASE_URL" ]; then
        log_error "DATABASE_URL environment variable is not set"
        exit 1
    fi
}

# Backup existing migrations
backup_migrations() {
    log_info "Backing up existing migrations to $BACKUP_DIR"
    mkdir -p "$BACKUP_DIR"
    cp -r "$MIGRATIONS_DIR"/* "$BACKUP_DIR/"
    log_info "Backup complete"
}

# Generate current schema dump
generate_schema_dump() {
    log_info "Generating current schema dump..."
    
    # Extract connection details from DATABASE_URL
    # Format: postgres://user:password@host:port/database
    
    psql "$DATABASE_URL" -c "
        SELECT 
            'CREATE TABLE IF NOT EXISTS ' || tablename || ' (...);' as ddl
        FROM pg_tables 
        WHERE schemaname = 'public'
        ORDER BY tablename;
    " > "$PROJECT_ROOT/docs/current_schema_tables.txt"
    
    log_info "Schema dump saved to docs/current_schema_tables.txt"
}

# Create squashed baseline migration
create_baseline() {
    local BASELINE_FILE="$MIGRATIONS_DIR/000_baseline.up.sql"
    local BASELINE_DOWN="$MIGRATIONS_DIR/000_baseline.down.sql"
    
    log_info "Creating baseline migration..."
    
    cat > "$BASELINE_FILE" << 'EOF'
-- Baseline Migration
-- This migration represents the squashed state of migrations 001-050
-- Generated on: $(date -u +"%Y-%m-%dT%H:%M:%SZ")
-- 
-- IMPORTANT: This baseline should only be applied to NEW databases.
-- Existing databases should continue using the incremental migrations.
--
-- To use this baseline:
-- 1. Ensure the database is empty (no schema_migrations table)
-- 2. Run: migrate -path migrations -database $DATABASE_URL up 1
-- 3. Then run remaining migrations: migrate -path migrations -database $DATABASE_URL up

-- This file should be populated by running:
-- pg_dump --schema-only --no-owner --no-privileges $DATABASE_URL > baseline_schema.sql
-- Then manually reviewing and cleaning up the output

-- Placeholder: Replace with actual schema dump
SELECT 'Baseline migration placeholder - replace with actual schema';
EOF

    cat > "$BASELINE_DOWN" << 'EOF'
-- Baseline Down Migration
-- WARNING: This will drop ALL tables. Use with extreme caution.

-- Drop all tables in reverse dependency order
-- This should be generated from the actual schema

SELECT 'Baseline down migration placeholder - replace with actual drop statements';
EOF

    log_info "Baseline migration files created"
    log_warn "You must manually populate these files with the actual schema"
}

# Print instructions
print_instructions() {
    echo ""
    echo "=========================================="
    echo "Migration Squash - Next Steps"
    echo "=========================================="
    echo ""
    echo "1. Review the backup at: $BACKUP_DIR"
    echo ""
    echo "2. Generate the actual baseline schema:"
    echo "   pg_dump --schema-only --no-owner --no-privileges \$DATABASE_URL > baseline_schema.sql"
    echo ""
    echo "3. Edit migrations/000_baseline.up.sql with the schema dump"
    echo ""
    echo "4. Create proper down migration in migrations/000_baseline.down.sql"
    echo ""
    echo "5. Test on a fresh database:"
    echo "   - Create new test database"
    echo "   - Run: migrate -path migrations -database \$TEST_DB_URL up"
    echo "   - Verify schema matches production"
    echo ""
    echo "6. For existing databases, migrations 001-050 remain in place"
    echo "   New databases can start from baseline (000)"
    echo ""
    echo "See docs/MIGRATION_BASELINE.md for detailed documentation"
    echo ""
}

# Main execution
main() {
    log_info "Starting migration squash process..."
    
    check_prerequisites
    backup_migrations
    generate_schema_dump
    create_baseline
    print_instructions
    
    log_info "Migration squash preparation complete"
}

# Run if executed directly
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
