-- Revert CASCADE constraints (restore original behavior)

-- bridge_transactions
ALTER TABLE bridge_transactions DROP CONSTRAINT IF EXISTS bridge_transactions_user_id_fkey;
ALTER TABLE bridge_transactions ADD CONSTRAINT bridge_transactions_user_id_fkey 
    FOREIGN KEY (user_id) REFERENCES users(id);

-- withdrawals
ALTER TABLE withdrawals DROP CONSTRAINT IF EXISTS withdrawals_user_id_fkey;
ALTER TABLE withdrawals ADD CONSTRAINT withdrawals_user_id_fkey 
    FOREIGN KEY (user_id) REFERENCES users(id);
