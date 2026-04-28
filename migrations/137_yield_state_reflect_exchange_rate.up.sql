-- Migrate yield_state from Lulo (cumulative interest) to Reflect (exchange rate).
--
-- Old keys:
--   last_distributed_yield  → cumulative USDC interest earned (Lulo)
--
-- New keys:
--   last_exchange_rate       → USDC+ exchange rate high-water mark (e.g. "1.0000")
--   reflect_deposited_usdc   → total USDC minted into Reflect (tracked by sweep worker)

-- Remove old Lulo key if present
DELETE FROM yield_state WHERE key = 'last_distributed_yield';

-- Insert Reflect keys (idempotent)
INSERT INTO yield_state (key, value, updated_at)
VALUES
    ('last_exchange_rate',     '1.0000', NOW()),
    ('reflect_deposited_usdc', '0',      NOW())
ON CONFLICT (key) DO NOTHING;
