DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM managed_wallets WHERE wallet_set_id = (
        SELECT id FROM wallet_sets WHERE circle_wallet_set_id = 'd68f52f2-cbff-50ea-92cb-bf9c5dab80d3'
    )) THEN
        RAISE EXCEPTION 'Cannot delete wallet_set: managed_wallets still reference it';
    END IF;
    DELETE FROM wallet_sets WHERE circle_wallet_set_id = 'd68f52f2-cbff-50ea-92cb-bf9c5dab80d3';
END $$;
