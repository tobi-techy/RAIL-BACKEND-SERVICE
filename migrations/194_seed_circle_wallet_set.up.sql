-- Seed the production Circle wallet set so managed_wallets FK constraint is satisfied.
-- The wallet_set_id in managed_wallets references wallet_sets.id directly using the Circle wallet set UUID.
INSERT INTO wallet_sets (id, name, circle_wallet_set_id, status, created_at, updated_at)
SELECT 'd68f52f2-cbff-50ea-92cb-bf9c5dab80d3'::uuid, 'STACK-WalletSet', 'd68f52f2-cbff-50ea-92cb-bf9c5dab80d3', 'active', NOW(), NOW()
WHERE NOT EXISTS (
    SELECT 1 FROM wallet_sets WHERE id = 'd68f52f2-cbff-50ea-92cb-bf9c5dab80d3'
);
