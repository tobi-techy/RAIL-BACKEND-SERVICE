ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS chk_balance_positive;
ALTER TABLE ledger_accounts ADD CONSTRAINT chk_balance_positive CHECK (balance >= 0);
