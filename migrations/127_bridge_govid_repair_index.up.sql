-- Partial index for the bridge gov ID repair worker poller query in FindApprovedNotActiveBridge.
-- Covers the four predicates on the users table; the EXISTS (SELECT 1 FROM kyc_submissions
-- WHERE provider = 'didit') subquery cannot be expressed as an index predicate in PostgreSQL
-- and is evaluated per candidate row after the index scan. This is acceptable because the
-- index already narrows the candidate set to a small number of rows (approved users with
-- incomplete Bridge status), making the per-row subquery cheap.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_users_bridge_govid_repair
    ON users (kyc_approved_at ASC)
    WHERE kyc_status = 'approved'
      AND (bridge_kyc_status IS NULL OR bridge_kyc_status NOT IN ('active', 'rejected'))
      AND kyc_provider_ref IS NOT NULL
      AND bridge_customer_id IS NOT NULL;
