ALTER TABLE ledger_accounts ADD CONSTRAINT positive_user_balance CHECK (account_type IN ('system_buffer_usdc', 'system_buffer_fiat', 'broker_operational') OR balance >= 0);
