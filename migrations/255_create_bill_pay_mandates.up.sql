-- Bill-pay mandates: a one-time in-app (Face ID) authorization that lets Miriam
-- pay bills in a category up to a per-payment cap without a fresh biometric
-- step-up for each payment. Under-cap payments run low-friction (iMessage poll)
-- or fully automatic (recurring). Over-cap or expired-consent payments fall back
-- to an in-app Face ID step-up. consent_stamped_at anchors the reauthorization
-- window (mirrors the transfer-consent model used by automation).
CREATE TABLE IF NOT EXISTS bill_pay_mandates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    product_category VARCHAR(20) NOT NULL,            -- airtime, data, electricity, cable, betting, transport, or 'all'
    per_payment_cap_ngn DECIMAL(20, 2) NOT NULL,      -- max NGN per payment without step-up
    daily_cap_ngn DECIMAL(20, 2),                     -- optional rolling 24h ceiling
    allow_auto BOOLEAN NOT NULL DEFAULT FALSE,        -- allow fully-automatic recurring payments
    status VARCHAR(20) NOT NULL DEFAULT 'active',     -- active, revoked
    consent_stamped_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, product_category)
);

CREATE INDEX idx_bill_pay_mandates_user ON bill_pay_mandates(user_id) WHERE status = 'active';
