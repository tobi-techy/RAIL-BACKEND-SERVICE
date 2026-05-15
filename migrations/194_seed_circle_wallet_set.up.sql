-- Seed the production Circle wallet set so managed_wallets FK constraint is satisfied.
INSERT INTO wallet_sets (id, name, circle_wallet_set_id, entity_secret_ciphertext, status, created_at, updated_at)
SELECT 'd68f52f2-cbff-50ea-92cb-bf9c5dab80d3'::uuid, 'STACK-WalletSet', 'd68f52f2-cbff-50ea-92cb-bf9c5dab80d3', 'managed-by-circle', 'active', NOW(), NOW()
WHERE NOT EXISTS (
    SELECT 1 FROM wallet_sets WHERE id = 'd68f52f2-cbff-50ea-92cb-bf9c5dab80d3'
);
