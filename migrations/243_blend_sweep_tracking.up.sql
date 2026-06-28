-- Track sweep status on redemptions so the worker can retry failed sweeps
-- and the crypto withdrawal path can skip the sweep entirely (funds leave platform).
ALTER TABLE blend_yield_redemptions
    ADD COLUMN IF NOT EXISTS swept_at            TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS sweep_failed_reason TEXT;

-- Index for the worker's sweep-retry query: complete rows not yet swept.
CREATE INDEX IF NOT EXISTS idx_blend_redemptions_pending_sweep
    ON blend_yield_redemptions (settled_at)
    WHERE status = 'complete' AND swept_at IS NULL;
