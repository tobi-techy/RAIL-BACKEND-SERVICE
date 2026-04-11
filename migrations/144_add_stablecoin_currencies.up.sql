-- Add support for new stablecoin currencies: USDT, EURC, PYUSD, USDG
-- Updates CHECK constraints on deposits.token and funding_event_jobs.token

-- 1. deposits.token: allow all supported stablecoins
ALTER TABLE deposits DROP CONSTRAINT IF EXISTS deposits_token_check;
ALTER TABLE deposits ADD CONSTRAINT deposits_token_check
    CHECK (token IN ('USDC', 'USDT', 'EURC', 'PYUSD', 'USDG'));

-- 2. funding_event_jobs.token: allow all supported stablecoins
ALTER TABLE funding_event_jobs DROP CONSTRAINT IF EXISTS funding_event_jobs_token_check;
ALTER TABLE funding_event_jobs ADD CONSTRAINT funding_event_jobs_token_check
    CHECK (token IN ('USDC', 'USDT', 'EURC', 'PYUSD', 'USDG'));
