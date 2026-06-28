DROP INDEX IF EXISTS idx_blend_redemptions_pending_sweep;
ALTER TABLE blend_yield_redemptions
    DROP COLUMN IF EXISTS swept_at,
    DROP COLUMN IF EXISTS sweep_failed_reason;
