-- Fix withdrawals ON DELETE CASCADE (re-apply if 099 failed)
ALTER TABLE withdrawals DROP CONSTRAINT IF EXISTS withdrawals_user_id_fkey;
ALTER TABLE withdrawals ADD CONSTRAINT withdrawals_user_id_fkey 
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE bridge_transactions DROP CONSTRAINT IF EXISTS bridge_transactions_user_id_fkey;
ALTER TABLE bridge_transactions ADD CONSTRAINT bridge_transactions_user_id_fkey 
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;

-- RESTRICT preserves audit trail per financial regulations - GDPR compliance via user anonymization in application layer
ALTER TABLE ledger_entries DROP CONSTRAINT IF EXISTS ledger_entries_account_id_fkey;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_account_id_fkey 
    FOREIGN KEY (account_id) REFERENCES ledger_accounts(id) ON DELETE RESTRICT;

ALTER TABLE ledger_accounts DROP CONSTRAINT IF EXISTS ledger_accounts_user_id_fkey;
ALTER TABLE ledger_accounts ADD CONSTRAINT ledger_accounts_user_id_fkey 
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;
