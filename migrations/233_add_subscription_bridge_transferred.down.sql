-- Safety: prevent rollback if charges have been transferred (would cause duplicate Bridge transfers on re-apply)
DO $$ BEGIN
IF EXISTS (SELECT 1 FROM subscription_charges WHERE bridge_transferred = TRUE LIMIT 1) THEN
    RAISE EXCEPTION 'Cannot rollback: subscription charges have been transferred. Manual intervention required to prevent duplicate transfers.';
END IF;
END $$;

ALTER TABLE subscription_charges DROP COLUMN IF EXISTS bridge_transferred;
