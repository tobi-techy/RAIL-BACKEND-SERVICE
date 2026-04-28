-- Make all challenges free (gameplay is free, Pro is for financial perks)
UPDATE challenges SET pro_only = false WHERE pro_only = true;

-- Update subscription plan column to support monthly/yearly
ALTER TABLE subscriptions ALTER COLUMN plan SET DEFAULT 'pro_monthly';
