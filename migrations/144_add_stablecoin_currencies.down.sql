-- Revert stablecoin currency support (back to USDC only)

ALTER TABLE deposits DROP CONSTRAINT IF EXISTS deposits_token_check;
ALTER TABLE deposits ADD CONSTRAINT deposits_token_check
    CHECK (token IN ('USDC'));

ALTER TABLE funding_event_jobs DROP CONSTRAINT IF EXISTS funding_event_jobs_token_check;
ALTER TABLE funding_event_jobs ADD CONSTRAINT funding_event_jobs_token_check
    CHECK (token IN ('USDC'));
