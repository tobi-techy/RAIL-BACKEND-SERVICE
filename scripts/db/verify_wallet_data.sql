-- Verify wallet sets have legacy Circle wallet set IDs (if applicable)
SELECT 
    id,
    name,
    circle_wallet_set_id,
    status,
    created_at
FROM wallet_sets
ORDER BY created_at DESC
LIMIT 5;

-- Verify managed wallets have bridge_wallet_id and wallet_set_id (legacy circle_wallet_id shown for reference)
SELECT 
    id,
    user_id,
    wallet_set_id,
    circle_wallet_id,
    bridge_wallet_id,
    chain,
    address,
    account_type,
    status,
    created_at
FROM managed_wallets
ORDER BY created_at DESC
LIMIT 10;

-- Check for any wallets missing bridge_wallet_id (for Bridge-managed wallets)
SELECT 
    COUNT(*) as missing_bridge_wallet_id_count
FROM managed_wallets
WHERE (bridge_wallet_id IS NULL OR bridge_wallet_id = '')
  AND account_type IN ('bridge_wallet', 'liquidation_address');

-- Legacy: check for any wallets missing circle_wallet_id
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
