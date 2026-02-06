# Migration Baseline Documentation

## Overview

Rail uses [golang-migrate](https://github.com/golang-migrate/migrate) for database schema management. As of January 2026, we have 81 migrations spanning the full evolution of the schema.

## Current Migration State

| Range | Description | Status |
|-------|-------------|--------|
| 001-010 | Initial schema, users, wallets, auth | Stable |
| 011-030 | Onboarding, funding, deposits, withdrawals | Stable |
| 031-050 | Security, compliance, audit logs | Stable |
| 051-070 | Ledger, treasury, advanced features | Stable |
| 071-081 | MFA, copy trading, cards, device sessions | Active |

## Migration Strategy

### For New Databases

New databases can use the baseline migration (when available):

```bash
# Apply baseline (migrations 001-050 squashed)
migrate -path migrations -database $DATABASE_URL up 1

# Apply remaining migrations
migrate -path migrations -database $DATABASE_URL up
```

### For Existing Databases

Existing databases continue using incremental migrations:

```bash
# Check current version
migrate -path migrations -database $DATABASE_URL version

# Apply pending migrations
migrate -path migrations -database $DATABASE_URL up
```

## Creating a Baseline

To squash old migrations into a baseline:

1. **Generate schema dump** from a database at migration 050:
   ```bash
   pg_dump --schema-only --no-owner --no-privileges $DATABASE_URL > baseline_schema.sql
   ```

2. **Create baseline migration**:
   ```bash
   ./scripts/db/squash_migrations.sh
   ```

3. **Test thoroughly**:
   - Apply baseline to fresh database
   - Compare schema with production
   - Run application tests

## Migration Best Practices

### Writing Migrations

1. **Always provide down migrations** - Every `.up.sql` needs a `.down.sql`
2. **Make migrations idempotent** - Use `IF NOT EXISTS`, `IF EXISTS`
3. **Avoid data migrations in schema files** - Use separate data migration scripts
4. **Test rollbacks** - Verify down migrations work correctly

### Naming Convention

```
{number}_{description}.up.sql
{number}_{description}.down.sql
```

Example: `082_add_user_preferences.up.sql`

### Transaction Safety

Wrap DDL in transactions where supported:

```sql
BEGIN;

ALTER TABLE users ADD COLUMN preferences JSONB;
CREATE INDEX idx_users_preferences ON users USING GIN (preferences);

COMMIT;
```

## Rollback Procedures

### Single Migration Rollback

```bash
migrate -path migrations -database $DATABASE_URL down 1
```

### Rollback to Specific Version

```bash
migrate -path migrations -database $DATABASE_URL goto 75
```

### Emergency Rollback

```bash
# Force set version (use with caution)
migrate -path migrations -database $DATABASE_URL force 75
```

## Testing Migrations

### Local Testing

```bash
# Run migration tests
./scripts/test/test_migrations.sh

# Test specific migration
migrate -path migrations -database $TEST_DATABASE_URL goto 80
migrate -path migrations -database $TEST_DATABASE_URL up 1
migrate -path migrations -database $TEST_DATABASE_URL down 1
```

### CI/CD Pipeline

Migrations are tested automatically:
1. Fresh database created
2. All migrations applied
3. Schema validated
4. Rollback tested for latest migration

## Schema Documentation

Current schema is documented in:
- `docs/architecture/database-schema.md` - ERD and relationships
- `docs/api/data-models.md` - Entity definitions

## Troubleshooting

### Dirty Database State

If migrations fail mid-way:

```bash
# Check dirty state
migrate -path migrations -database $DATABASE_URL version

# Force clean state (after manual fix)
migrate -path migrations -database $DATABASE_URL force {version}
```

### Migration Conflicts

When multiple developers add migrations:

1. Coordinate migration numbers in team channel
2. Use feature branches for migration development
3. Rebase and renumber before merging

## Contact

For migration issues:
- Check `#backend` Slack channel
- Review migration logs in CloudWatch
- Escalate to on-call if production impact
