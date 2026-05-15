-- Remove backfilled sweep records (only pending ones that were never processed)
DELETE FROM deposit_sweeps WHERE status = 'pending' AND intent_address IS NULL;
