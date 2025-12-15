-- Create reconciliation reports table
CREATE TABLE reconciliation_reports (
    id UUID PRIMARY KEY,
    run_type VARCHAR(20) NOT NULL CHECK (run_type IN ('hourly', 'daily', 'manual')),
    status VARCHAR(20) NOT NULL CHECK (status IN ('pending', 'in_progress', 'completed', 'failed')),
    started_at TIMESTAMP NOT NULL,
    completed_at TIMESTAMP,
    total_checks INT NOT NULL DEFAULT 0,
    passed_checks INT NOT NULL DEFAULT 0,
    failed_checks INT NOT NULL DEFAULT 0,
    exceptions_count INT NOT NULL DEFAULT 0,
    error_message TEXT,
    metadata JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Create reconciliation checks table
CREATE TABLE reconciliation_checks (
    id UUID PRIMARY KEY,
    report_id UUID NOT NULL REFERENCES reconciliation_reports(id) ON DELETE CASCADE,
    check_type VARCHAR(50) NOT NULL CHECK (check_type IN (
        'ledger_consistency',
        'circle_balance',
        'alpaca_balance',
        'deposits',
        'conversion_jobs',
        'withdrawals'
    )),
    status VARCHAR(20) NOT NULL CHECK (status IN ('pending', 'in_progress', 'completed', 'failed')),
    expected_value DECIMAL(36,18) NOT NULL DEFAULT 0,
    actual_value DECIMAL(36,18) NOT NULL DEFAULT 0,
    difference DECIMAL(36,18) NOT NULL DEFAULT 0,
    passed BOOLEAN NOT NULL DEFAULT false,
    error_message TEXT,
    execution_time_ms BIGINT NOT NULL DEFAULT 0,
    metadata JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Create reconciliation exceptions table
CREATE TABLE reconciliation_exceptions (
    id UUID PRIMARY KEY,
    report_id UUID NOT NULL REFERENCES reconciliation_reports(id) ON DELETE CASCADE,
    check_id UUID NOT NULL REFERENCES reconciliation_checks(id) ON DELETE CASCADE,
    check_type VARCHAR(50) NOT NULL,
    severity VARCHAR(20) NOT NULL CHECK (severity IN ('low', 'medium', 'high', 'critical')),
    description TEXT NOT NULL,
    expected_value DECIMAL(36,18) NOT NULL,
    actual_value DECIMAL(36,18) NOT NULL,
    difference DECIMAL(36,18) NOT NULL,
    currency VARCHAR(10) NOT NULL,
    affected_user_id UUID REFERENCES users(id),
    affected_entity VARCHAR(200),
    auto_corrected BOOLEAN NOT NULL DEFAULT false,
    correction_action TEXT,
    resolved_at TIMESTAMP,
    resolved_by VARCHAR(200),
    resolution_notes TEXT,
    metadata JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Create indices for efficient queries
CREATE INDEX idx_reconciliation_reports_run_type ON reconciliation_reports(run_type);
CREATE INDEX idx_reconciliation_reports_status ON reconciliation_reports(status);
CREATE INDEX idx_reconciliation_reports_created_at ON reconciliation_reports(created_at DESC);

CREATE INDEX idx_reconciliation_checks_report_id ON reconciliation_checks(report_id);
CREATE INDEX idx_reconciliation_checks_check_type ON reconciliation_checks(check_type);
CREATE INDEX idx_reconciliation_checks_passed ON reconciliation_checks(passed);

CREATE INDEX idx_reconciliation_exceptions_report_id ON reconciliation_exceptions(report_id);
CREATE INDEX idx_reconciliation_exceptions_check_id ON reconciliation_exceptions(check_id);
CREATE INDEX idx_reconciliation_exceptions_severity ON reconciliation_exceptions(severity);
CREATE INDEX idx_reconciliation_exceptions_affected_user_id ON reconciliation_exceptions(affected_user_id);
CREATE INDEX idx_reconciliation_exceptions_resolved_at ON reconciliation_exceptions(resolved_at);
CREATE INDEX idx_reconciliation_exceptions_created_at ON reconciliation_exceptions(created_at DESC);

-- Create composite index for unresolved critical exceptions
CREATE INDEX idx_reconciliation_exceptions_unresolved_critical 
    ON reconciliation_exceptions(severity, resolved_at) 
    WHERE resolved_at IS NULL AND severity IN ('high', 'critical');
