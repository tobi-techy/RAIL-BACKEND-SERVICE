-- Add category and narration metadata to withdrawals

ALTER TABLE withdrawals
ADD COLUMN IF NOT EXISTS category VARCHAR(64),
ADD COLUMN IF NOT EXISTS narration TEXT;
