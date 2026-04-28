-- Rollback: restore Lulo yield_state key, remove Reflect keys.
DELETE FROM yield_state WHERE key IN ('last_exchange_rate', 'reflect_deposited_usdc');

INSERT INTO yield_state (key, value, updated_at)
VALUES ('last_distributed_yield', '0', NOW())
ON CONFLICT (key) DO NOTHING;
