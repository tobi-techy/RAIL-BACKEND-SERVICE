-- Add off-ramp tracking fields to deposits table
-- Enables Due API off-ramp integration and status tracking

ALTER TABLE deposits
ADD COLUMN off_ramp_initiated_at TIMESTAMPTZ,
ADD COLUMN off_ramp_completed_at TIMESTAMPTZ,
ADD COLUMN due_transfer_reference VARCHAR(255);

-- Add indexes for efficient querying
CREATE INDEX idx_deposits_off_ramp_initiated_at ON deposits(off_ramp_initiated_at);
CREATE INDEX idx_deposits_off_ramp_completed_at ON deposits(off_ramp_completed_at);
CREATE INDEX idx_deposits_due_transfer_reference ON deposits(due_transfer_reference);

-- Add comments for documentation
COMMENT ON COLUMN deposits.off_ramp_initiated_at IS 'Timestamp when Due off-ramp transfer was initiated';
COMMENT ON COLUMN deposits.off_ramp_completed_at IS 'Timestamp when Due off-ramp transfer was completed';
COMMENT ON COLUMN deposits.due_transfer_reference IS 'Due API transfer ID for tracking and reconciliation';
