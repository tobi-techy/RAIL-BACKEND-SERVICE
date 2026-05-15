-- Down migration intentionally disabled to prevent data loss.
-- Backfilled sweep records are legitimate pending work items.
-- If rollback is needed, manually review deposit_sweeps where
-- intent_address IS NULL and created_at matches the migration window.
SELECT 1;
