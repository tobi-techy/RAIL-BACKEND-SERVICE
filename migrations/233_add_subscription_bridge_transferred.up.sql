ALTER TABLE subscription_charges ADD COLUMN IF NOT EXISTS bridge_transferred BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_subscription_charges_untransferred
    ON subscription_charges (charged_at ASC)
    WHERE bridge_transferred = FALSE AND status = 'completed';
