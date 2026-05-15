-- Backfill deposit_sweeps for existing confirmed deposits on non-Solana chains
-- that don't already have a sweep record. This ensures all historical non-Solana
-- deposits get swept to Solana during the transition.
INSERT INTO deposit_sweeps (id, deposit_id, user_id, source_chain, amount, status, created_at, updated_at)
SELECT
    gen_random_uuid(),
    d.id,
    d.user_id,
    d.chain,
    d.amount,
    'pending',
    NOW(),
    NOW()
FROM deposits d
WHERE d.status = 'confirmed'
  AND UPPER(d.chain) NOT IN ('SOL', 'SOLANA', 'SOL-DEVNET')
  AND NOT EXISTS (
      SELECT 1 FROM deposit_sweeps ds WHERE ds.deposit_id = d.id
  );
