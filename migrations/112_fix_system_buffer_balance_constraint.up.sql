-- System buffer accounts are counter-entries for external inflows/outflows.
-- They must be allowed to go negative (they represent external reserves, not user funds).
ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS chk_balance_positive;
ALTER TABLE ledger_accounts ADD CONSTRAINT chk_balance_positive
    CHECK (balance >= 0 OR account_type IN ('system_buffer_usdc', 'system_buffer_fiat', 'broker_operational'));

-- Seed system buffers with a large value so deposits don't fail on staging/production.
-- In production this should be topped up via treasury operations.
UPDATE ledger_accounts SET balance = 1000000 
WHERE account_type = 'system_buffer_usdc' AND user_id IS NULL AND balance = 0;
