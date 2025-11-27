-- Rollback smart allocation mode tables migration

-- Drop trigger and function
DROP TRIGGER IF EXISTS update_smart_allocation_mode_updated_at_trigger ON smart_allocation_mode;
DROP FUNCTION IF EXISTS update_smart_allocation_mode_updated_at();

-- Remove column from transactions table
ALTER TABLE transactions DROP COLUMN IF EXISTS declined_due_to_7030;

-- Drop tables in reverse order
DROP TABLE IF EXISTS weekly_allocation_summaries;
DROP TABLE IF EXISTS allocation_events;
DROP TABLE IF EXISTS smart_allocation_mode;
