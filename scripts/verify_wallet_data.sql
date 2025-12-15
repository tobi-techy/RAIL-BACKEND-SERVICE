-- Verify wallet sets have Circle wallet set IDs
SELECT 
    id,
    name,
    circle_wallet_set_id,
    status,
    created_at
FROM wallet_sets
ORDER BY created_at DESC
LIMIT 5;

-- Verify managed wallets have both circle_wallet_id and wallet_set_id
SELECT 
    id,
    user_id,
    wallet_set_id,
    circle_wallet_id,
    chain,
    address,
    account_type,
    status,
    created_at
FROM managed_wallets
ORDER BY created_at DESC
LIMIT 10;

-- Check for any wallets missing circle_wallet_id or wallet_set_id
SELECT 
    COUNT(*) as missing_circle_wallet_id_count
FROM managed_wallets
WHERE circle_wallet_id IS NULL OR circle_wallet_id = '';

SELECT 
    COUNT(*) as missing_wallet_set_id_count
FROM managed_wallets
WHERE wallet_set_id IS NULL;

-- Check wallet distribution by chain
SELECT 
    chain,
    COUNT(*) as wallet_count,
    COUNT(DISTINCT user_id) as unique_users
FROM managed_wallets
GROUP BY chain
ORDER BY wallet_count DESC;
