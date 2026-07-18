-- Saved bill-payment beneficiaries so Miriam can pay "the usual" without the
-- user re-entering a phone/meter/smartcard/betting id each time. Sensitive
-- identifiers are stored here; access is scoped per user.
CREATE TABLE IF NOT EXISTS bill_beneficiaries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label VARCHAR(100) NOT NULL,                      -- user-facing name, e.g. "Mum's phone"
    product_category VARCHAR(20) NOT NULL,            -- airtime, data, electricity, cable, betting, transport
    recipient VARCHAR(255) NOT NULL,                  -- phone / meter / smartcard / betting id
    network_id VARCHAR(8),                            -- 01..04 for airtime/data
    prod_id VARCHAR(64),                              -- default plan/provider for this beneficiary
    elect_id VARCHAR(64),                             -- electricity disco id
    recipient_name VARCHAR(255),                      -- validated holder name (meter/account)
    last_used_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, product_category, recipient)
);

CREATE INDEX idx_bill_beneficiaries_user ON bill_beneficiaries(user_id, product_category);
