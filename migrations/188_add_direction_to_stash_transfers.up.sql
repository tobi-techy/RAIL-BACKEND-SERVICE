-- Add direction column to stash_transfers to support both spending_to_stash and stash_to_spending
DO $$
DECLARE
    invalid_existing_rows INTEGER;
BEGIN
    -- The legacy stash_transfers table represented only stash-to-spending transfers.
    -- Validate the existing rows still match the expected legacy shape before backfilling direction.
    SELECT COUNT(*)
    INTO invalid_existing_rows
    FROM stash_transfers
    WHERE user_id IS NULL
       OR amount IS NULL
       OR amount <= 0
       OR status NOT IN ('pending', 'completed', 'failed');

    IF invalid_existing_rows > 0 THEN
        RAISE EXCEPTION 'stash_transfers contains % rows that cannot be validated as legacy stash-to-spending transfers', invalid_existing_rows;
    END IF;
END $$;

ALTER TABLE stash_transfers
    ADD COLUMN IF NOT EXISTS direction VARCHAR(20);

UPDATE stash_transfers
SET direction = 'stash_to_spending'
WHERE direction IS NULL;

ALTER TABLE stash_transfers
    ALTER COLUMN direction SET NOT NULL,
    ALTER COLUMN direction SET DEFAULT 'stash_to_spending';

ALTER TABLE stash_transfers
    ADD CONSTRAINT chk_stash_transfers_direction CHECK (direction IN ('stash_to_spending', 'spending_to_stash'));

CREATE INDEX IF NOT EXISTS idx_stash_transfers_direction ON stash_transfers(direction);
