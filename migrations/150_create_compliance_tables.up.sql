CREATE TABLE IF NOT EXISTS compliance_screenings (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    screening_type  VARCHAR(32) NOT NULL,  -- transaction, aml_kyc
    direction       VARCHAR(16) NOT NULL DEFAULT '',
    amount          VARCHAR(32) NOT NULL DEFAULT '',
    currency        VARCHAR(8) NOT NULL DEFAULT '',
    didit_txn_uuid  VARCHAR(64) NOT NULL DEFAULT '',
    reference_id    VARCHAR(128) NOT NULL DEFAULT '',
    status          VARCHAR(16) NOT NULL,  -- APPROVED, IN_REVIEW, DECLINED
    score           INTEGER NOT NULL DEFAULT 0,
    severity        VARCHAR(16) NOT NULL DEFAULT '',
    details         JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_compliance_screenings_user_id ON compliance_screenings (user_id);
CREATE INDEX idx_compliance_screenings_status ON compliance_screenings (status);
CREATE INDEX idx_compliance_screenings_created_at ON compliance_screenings (created_at);
CREATE INDEX idx_compliance_screenings_reference_id ON compliance_screenings (reference_id);
CREATE UNIQUE INDEX idx_compliance_screenings_didit_txn_uuid ON compliance_screenings (didit_txn_uuid) WHERE didit_txn_uuid != '';

CREATE TABLE IF NOT EXISTS compliance_alerts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    screening_id    UUID NOT NULL REFERENCES compliance_screenings(id),
    alert_type      VARCHAR(32) NOT NULL,
    severity        VARCHAR(16) NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    status          VARCHAR(16) NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'investigating', 'resolved', 'dismissed')),
    resolved_by     VARCHAR(128),
    resolved_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_compliance_alerts_user_id ON compliance_alerts (user_id);
CREATE INDEX idx_compliance_alerts_status ON compliance_alerts (status);
CREATE INDEX idx_compliance_alerts_created_at ON compliance_alerts (created_at);
