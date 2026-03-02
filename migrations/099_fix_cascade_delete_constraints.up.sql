-- Fix missing ON DELETE CASCADE for user deletion to work properly

-- bridge_transactions
ALTER TABLE bridge_transactions DROP CONSTRAINT IF EXISTS bridge_transactions_user_id_fkey;
ALTER TABLE bridge_transactions ADD CONSTRAINT bridge_transactions_user_id_fkey 
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

-- withdrawals
ALTER TABLE withdrawals DROP CONSTRAINT IF EXISTS withdrawals_user_id_fkey;
ALTER TABLE withdrawals ADD CONSTRAINT withdrawals_user_id_fkey 
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
