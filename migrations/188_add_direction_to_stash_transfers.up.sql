-- Add direction column to stash_transfers to support both spending_to_stash and stash_to_spending
ALTER TABLE stash_transfers
    ADD COLUMN IF NOT EXISTS direction VARCHAR(20) NOT NULL DEFAULT 'stash_to_spending';

ALTER TABLE stash_transfers
    ADD CONSTRAINT chk_stash_transfers_direction CHECK (direction IN ('stash_to_spending', 'spending_to_stash'));

CREATE INDEX IF NOT EXISTS idx_stash_transfers_direction ON stash_transfers(direction);
