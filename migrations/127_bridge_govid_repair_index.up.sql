CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_users_bridge_govid_repair
    ON users (kyc_approved_at ASC)
    WHERE kyc_status = 'approved'
      AND (bridge_kyc_status IS NULL OR bridge_kyc_status NOT IN ('active', 'rejected'))
      AND kyc_provider_ref IS NOT NULL
      AND bridge_customer_id IS NOT NULL;
