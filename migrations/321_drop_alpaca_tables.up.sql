-- Drop the Alpaca brokerage tables.
--
-- The Alpaca brokerage provider has been removed and superseded by the
-- Glider/Solana investment sleeve (see migrations 303-306). Every table below
-- existed only to mirror Alpaca accounts, orders, positions, instant-funding
-- transfers and webhook events, so they are dropped together.
--
-- Order matters: drop dependants (orders/positions/funding/events, which carry
-- alpaca_account_id FKs) before alpaca_accounts.

DROP TABLE IF EXISTS alpaca_events;
DROP TABLE IF EXISTS alpaca_instant_funding;
DROP TABLE IF EXISTS investment_positions;
DROP TABLE IF EXISTS investment_orders;
DROP TABLE IF EXISTS alpaca_accounts;
