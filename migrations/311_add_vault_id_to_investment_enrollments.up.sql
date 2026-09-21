-- The GliderPortfolioLink: which retirement vault a portfolio belongs to.
-- This is the one-query join ops uses to answer "where is this user's money".

ALTER TABLE investment_enrollments
    ADD COLUMN IF NOT EXISTS vault_id UUID REFERENCES retirement_vaults(id) ON DELETE RESTRICT;

-- One portfolio per vault.
CREATE UNIQUE INDEX IF NOT EXISTS idx_investment_enrollments_vault
    ON investment_enrollments(vault_id) WHERE vault_id IS NOT NULL;
