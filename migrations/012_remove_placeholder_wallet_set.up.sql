-- Remove placeholder wallet set inserted during early development
DELETE FROM wallet_sets
WHERE circle_wallet_set_id = 'placeholder-wallet-set-id'
   OR entity_secret_ciphertext = 'placeholder-entity-secret';

-- Clean up any wallets that referenced the placeholder set
DELETE FROM managed_wallets
WHERE wallet_set_id NOT IN (SELECT id FROM wallet_sets);
