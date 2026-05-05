CREATE TABLE IF NOT EXISTS pending_card_unfreezes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    card_id UUID NOT NULL,
    automation_id UUID NOT NULL,
    unfreeze_at TIMESTAMPTZ NOT NULL,
    attempts INT NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- UNIQUE on card_id is safe here: this is a fresh table creation with no existing data.
    -- Only one pending unfreeze per card can exist at a time.
    UNIQUE(card_id)
);

CREATE INDEX idx_pending_card_unfreezes_due ON pending_card_unfreezes (unfreeze_at) WHERE attempts < 5;
