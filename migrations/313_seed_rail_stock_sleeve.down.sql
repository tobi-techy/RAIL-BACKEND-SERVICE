-- Undo the Rail Stock Sleeve seed (the live Glider tenant strategy is NOT
-- deleted: it belongs to the tenant, not to any one database).
DELETE FROM investment_strategy_versions WHERE id = 'e5aa10ca-710c-4a11-8aa1-000000000001';
DELETE FROM investment_strategies WHERE id = 'd15c0ba1-71c0-4a11-8aa1-000000000001';
DELETE FROM investment_assets WHERE id IN (
    'a1ee1001-5eed-4a1e-8aa1-0000000000a1',
    'b2ee1002-5eed-4a1e-8aa1-0000000000b2',
    'c3ee1003-5eed-4a1e-8aa1-0000000000c3'
);
