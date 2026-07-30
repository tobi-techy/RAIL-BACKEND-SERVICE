DROP TABLE IF EXISTS ledger_velocity_buckets;

ALTER TABLE ledger_transactions
    DROP COLUMN IF EXISTS previous_transaction_hash,
    DROP COLUMN IF EXISTS transaction_hash,
    DROP COLUMN IF EXISTS initiated_by,
    DROP COLUMN IF EXISTS reason;
