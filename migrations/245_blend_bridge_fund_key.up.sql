-- Per-intent funding idempotency key for Blend deposit-route bridge funding.
-- Previously the Circle funding transfer used a key derived from the route ID
-- alone, so when a failed ChainRails intent was cleared and replaced, the new
-- intent's funding transfer collided with (or was deduped against) the old
-- intent's transfer and the route could never fund the replacement intent.
-- NULL means a legacy (pre-migration) intent: funding falls back to the old
-- route-scoped key so an already-funded in-flight intent is never double-funded.
ALTER TABLE blend_deposit_routes ADD COLUMN IF NOT EXISTS bridge_fund_key TEXT;
