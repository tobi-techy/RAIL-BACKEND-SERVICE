-- Ensure bridge KYC fields exist on users for environments that never had due_* KYC columns.
ALTER TABLE users
ADD COLUMN IF NOT EXISTS bridge_kyc_status VARCHAR(50),
ADD COLUMN IF NOT EXISTS bridge_kyc_link TEXT;

-- Backfill from legacy due_* columns when they still exist.
DO $$
BEGIN
	IF EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'users' AND column_name = 'due_kyc_status'
	) THEN
		UPDATE users
		SET bridge_kyc_status = COALESCE(bridge_kyc_status, due_kyc_status)
		WHERE bridge_kyc_status IS NULL AND due_kyc_status IS NOT NULL;
	END IF;

	IF EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'users' AND column_name = 'due_kyc_link'
	) THEN
		UPDATE users
		SET bridge_kyc_link = COALESCE(bridge_kyc_link, due_kyc_link)
		WHERE bridge_kyc_link IS NULL AND due_kyc_link IS NOT NULL;
	END IF;
END $$;
