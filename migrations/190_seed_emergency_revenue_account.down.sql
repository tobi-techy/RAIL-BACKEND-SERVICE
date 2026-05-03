DELETE FROM ledger_accounts WHERE account_type = 'emergency_withdrawal_revenue' AND user_id IS NULL AND balance = 0;
