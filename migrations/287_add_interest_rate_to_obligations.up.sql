-- Add interest_rate column to financial_obligations for debt coaching
ALTER TABLE financial_obligations
ADD COLUMN IF NOT EXISTS interest_rate NUMERIC(6,3);
