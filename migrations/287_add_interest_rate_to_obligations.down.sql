-- Remove interest_rate column from financial_obligations
ALTER TABLE financial_obligations
DROP COLUMN IF EXISTS interest_rate;
