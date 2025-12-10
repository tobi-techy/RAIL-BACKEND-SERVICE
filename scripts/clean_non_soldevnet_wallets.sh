#!/bin/bash
# Clean Non-SOL-DEVNET Wallets Script
# This script helps you delete wallet records that are not on SOL-DEVNET chain
# before running migration 014

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}=== Clean Non-SOL-DEVNET Wallets ===${NC}\n"

# Database connection details (edit these or set as environment variables)
DB_NAME="${DATABASE_NAME:-rail_service_dev}"
DB_USER="${DATABASE_USER:-postgres}"
DB_HOST="${DATABASE_HOST:-localhost}"
DB_PORT="${DATABASE_PORT:-5432}"

echo "Database: $DB_NAME"
echo "User: $DB_USER"
echo "Host: $DB_HOST:$DB_PORT"
echo ""

# Check if psql is available
if ! command -v psql &> /dev/null; then
    echo -e "${RED}Error: psql command not found. Please install PostgreSQL client.${NC}"
    exit 1
fi

# Test database connection
echo -e "${YELLOW}Testing database connection...${NC}"
if ! psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -c "SELECT 1;" > /dev/null 2>&1; then
    echo -e "${RED}Error: Cannot connect to database. Please check your connection settings.${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Connected to database${NC}\n"

# Show current wallet distribution
echo -e "${YELLOW}Current wallet distribution by chain:${NC}"
psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -c "
    SELECT chain, COUNT(*) as count 
    FROM managed_wallets 
    GROUP BY chain 
    ORDER BY count DESC;
"

# Count wallets to be deleted
NON_SOLDEVNET_COUNT=$(psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -t -c "
    SELECT COUNT(*) FROM managed_wallets WHERE chain != 'SOL-DEVNET';
" | xargs)

SOLDEVNET_COUNT=$(psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -t -c "
    SELECT COUNT(*) FROM managed_wallets WHERE chain = 'SOL-DEVNET';
" | xargs)

echo ""
echo -e "${YELLOW}Summary:${NC}"
echo -e "  SOL-DEVNET wallets: ${GREEN}$SOLDEVNET_COUNT${NC} (will be kept)"
echo -e "  Other chain wallets: ${RED}$NON_SOLDEVNET_COUNT${NC} (will be deleted)"
echo ""

if [ "$NON_SOLDEVNET_COUNT" -eq 0 ]; then
    echo -e "${GREEN}✓ No non-SOL-DEVNET wallets found. Database is ready for migration!${NC}"
    exit 0
fi

# Confirm deletion
echo -e "${RED}WARNING: This will permanently delete $NON_SOLDEVNET_COUNT wallet records!${NC}"
echo -e "${YELLOW}Do you want to proceed? (yes/no)${NC}"
read -r CONFIRM

if [ "$CONFIRM" != "yes" ]; then
    echo -e "${YELLOW}Operation cancelled.${NC}"
    exit 0
fi

# Create backup first
BACKUP_FILE="managed_wallets_backup_$(date +%Y%m%d_%H%M%S).sql"
echo -e "\n${YELLOW}Creating backup: $BACKUP_FILE${NC}"
pg_dump -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -t managed_wallets > "$BACKUP_FILE"
echo -e "${GREEN}✓ Backup created: $BACKUP_FILE${NC}"

# Delete non-SOL-DEVNET wallets
echo -e "\n${YELLOW}Deleting non-SOL-DEVNET wallets...${NC}"
DELETED_COUNT=$(psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -t -c "
    WITH deleted AS (
        DELETE FROM managed_wallets 
        WHERE chain != 'SOL-DEVNET' 
        RETURNING *
    )
    SELECT COUNT(*) FROM deleted;
" | xargs)

echo -e "${GREEN}✓ Deleted $DELETED_COUNT wallet records${NC}"

# Show final distribution
echo -e "\n${YELLOW}Final wallet distribution:${NC}"
psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" -c "
    SELECT chain, COUNT(*) as count 
    FROM managed_wallets 
    GROUP BY chain;
"

echo -e "\n${GREEN}✓ Database is now ready for migration 014!${NC}"
echo -e "${YELLOW}You can now run: make run${NC}"
echo -e "\n${YELLOW}Note: Backup file saved to: $BACKUP_FILE${NC}"
