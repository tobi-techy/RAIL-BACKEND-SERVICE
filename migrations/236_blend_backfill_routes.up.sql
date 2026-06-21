-- Support one-time backfill routes that move a user's EXISTING stash balance into Blend
-- (no originating deposit row). deposit_id becomes nullable for these synthetic routes,
-- and a source discriminator separates per-deposit routing from backfill.

ALTER TABLE blend_deposit_routes ALTER COLUMN deposit_id DROP NOT NULL;
ALTER TABLE blend_deposit_routes ADD COLUMN IF NOT EXISTS source VARCHAR(16) NOT NULL DEFAULT 'deposit';

ALTER TABLE blend_yield_positions ALTER COLUMN deposit_id DROP NOT NULL;

CREATE INDEX IF NOT EXISTS idx_blend_deposit_routes_source ON blend_deposit_routes(source);
