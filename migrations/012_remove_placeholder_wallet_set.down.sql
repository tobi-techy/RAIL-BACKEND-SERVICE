-- Re-insert placeholder wallet set for rollback (not recommended for production)
INSERT INTO wallet_sets (id, name, circle_wallet_set_id, entity_secret_ciphertext, status, created_at, updated_at)
SELECT '00000000-0000-0000-0000-000000000001'::uuid,
       'default-wallet-set',
       'placeholder-wallet-set-id',
       'placeholder-entity-secret',
       'active',
       NOW(),
       NOW()
WHERE NOT EXISTS (
    SELECT 1 FROM wallet_sets WHERE circle_wallet_set_id = 'placeholder-wallet-set-id'
);
