-- Revert CASCADE constraints (restore original behavior)

-- bridge_transactions
ALTER TABLE bridge_transactions DROP CONSTRAINT IF EXISTS bridge_transactions_user_id_fkey;
ALTER TABLE bridge_transactions ADD CONSTRAINT bridge_transactions_user_id_fkey 
    FOREIGN KEY (user_id) REFERENCES users(id);

-- withdrawals
ALTER TABLE withdrawals DROP CONSTRAINT IF EXISTS withdrawals_user_id_fkey;
ALTER TABLE withdrawals ADD CONSTRAINT withdrawals_user_id_fkey 
    FOREIGN KEY (user_id) REFERENCES users(id);

-- compliance_reviews.reviewed_by
ALTER TABLE compliance_reviews DROP CONSTRAINT IF EXISTS compliance_reviews_reviewed_by_fkey;
ALTER TABLE compliance_reviews ADD CONSTRAINT compliance_reviews_reviewed_by_fkey 
    FOREIGN KEY (reviewed_by) REFERENCES users(id);

-- conductor_applications.reviewed_by
ALTER TABLE conductor_applications DROP CONSTRAINT IF EXISTS conductor_applications_reviewed_by_fkey;
ALTER TABLE conductor_applications ADD CONSTRAINT conductor_applications_reviewed_by_fkey 
    FOREIGN KEY (reviewed_by) REFERENCES users(id);
