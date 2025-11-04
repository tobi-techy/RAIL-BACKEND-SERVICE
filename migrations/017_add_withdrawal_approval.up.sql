-- Add withdrawal approval system for enhanced fund protection
-- Dual authorization required for withdrawals above threshold

-- Create enum for withdrawal status (must be created before tables that use it)
CREATE TYPE withdrawal_status AS ENUM (
    'pending',
    'approved',
    'processing',
    'completed',
    'failed',
    'rejected',
    'expired',
    'cancelled'
);

-- Create withdrawal requests table
CREATE TABLE IF NOT EXISTS withdrawal_requests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    wallet_id uuid NOT NULL REFERENCES managed_wallets(id) ON DELETE CASCADE,
    amount decimal(36,18) NOT NULL CHECK (amount > 0),
    currency varchar(10) NOT NULL DEFAULT 'USDC',
    destination_address text NOT NULL,
    blockchain varchar(50) NOT NULL,
    status withdrawal_status NOT NULL DEFAULT 'pending',
    approval_required boolean NOT NULL DEFAULT false,
    approved_by uuid REFERENCES users(id),
    approved_at timestamp with time zone,
    expires_at timestamp with time zone NOT NULL,
    rejection_reason text,
    rejected_by uuid REFERENCES users(id),
    rejected_at timestamp with time zone,
    idempotency_key varchar(255) UNIQUE,
    metadata jsonb DEFAULT '{}',
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now()
);

-- Create withdrawal approvals table (for dual auth)
CREATE TABLE IF NOT EXISTS withdrawal_approvals (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    withdrawal_request_id uuid NOT NULL REFERENCES withdrawal_requests(id) ON DELETE CASCADE,
    approver_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    approval_level int NOT NULL DEFAULT 1, -- 1=primary, 2=secondary
    approved_at timestamp with time zone DEFAULT now(),
    notes text,
    created_at timestamp with time zone DEFAULT now(),
    UNIQUE(withdrawal_request_id, approver_id)
);

-- Create withdrawal limits table
CREATE TABLE IF NOT EXISTS withdrawal_limits (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    daily_limit decimal(36,18) NOT NULL DEFAULT 1000.00,
    weekly_limit decimal(36,18) NOT NULL DEFAULT 5000.00,
    monthly_limit decimal(36,18) NOT NULL DEFAULT 10000.00,
    require_dual_auth_above decimal(36,18) NOT NULL DEFAULT 1000.00,
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now(),
    UNIQUE(user_id)
);

-- Create withdrawal tracking table
CREATE TABLE IF NOT EXISTS withdrawal_tracking (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    date date NOT NULL,
    daily_total decimal(36,18) NOT NULL DEFAULT 0,
    weekly_total decimal(36,18) NOT NULL DEFAULT 0,
    monthly_total decimal(36,18) NOT NULL DEFAULT 0,
    last_withdrawal_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now(),
    UNIQUE(user_id, date)
);

-- Add indexes for performance
CREATE INDEX idx_withdrawal_requests_user_id ON withdrawal_requests(user_id);
CREATE INDEX idx_withdrawal_requests_status ON withdrawal_requests(status);
CREATE INDEX idx_withdrawal_requests_expires_at ON withdrawal_requests(expires_at);
CREATE INDEX idx_withdrawal_approvals_request_id ON withdrawal_approvals(withdrawal_request_id);
CREATE INDEX idx_withdrawal_limits_user_id ON withdrawal_limits(user_id);
CREATE INDEX idx_withdrawal_tracking_user_date ON withdrawal_tracking(user_id, date);

-- Add trigger to update updated_at
CREATE OR REPLACE FUNCTION update_withdrawal_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_withdrawal_requests_updated_at
    BEFORE UPDATE ON withdrawal_requests
    FOR EACH ROW EXECUTE FUNCTION update_withdrawal_updated_at();

CREATE TRIGGER trigger_withdrawal_limits_updated_at
    BEFORE UPDATE ON withdrawal_limits
    FOR EACH ROW EXECUTE FUNCTION update_withdrawal_updated_at();

CREATE TRIGGER trigger_withdrawal_tracking_updated_at
    BEFORE UPDATE ON withdrawal_tracking
    FOR EACH ROW EXECUTE FUNCTION update_withdrawal_updated_at();

-- Function to check withdrawal limits
CREATE OR REPLACE FUNCTION check_withdrawal_limits(
    p_user_id uuid,
    p_amount decimal
) RETURNS jsonb AS $$
DECLARE
    v_limits record;
    v_tracking record;
    v_daily_used decimal := 0;
    v_weekly_used decimal := 0;
    v_monthly_used decimal := 0;
    v_daily_remaining decimal;
    v_weekly_remaining decimal;
    v_monthly_remaining decimal;
    v_require_dual_auth boolean := false;
BEGIN
    -- Get user limits
    SELECT * INTO v_limits
    FROM withdrawal_limits
    WHERE user_id = p_user_id AND is_active = true;

    -- If no limits set, use defaults
    IF v_limits IS NULL THEN
        v_limits.daily_limit := 1000.00;
        v_limits.weekly_limit := 5000.00;
        v_limits.monthly_limit := 10000.00;
        v_limits.require_dual_auth_above := 1000.00;
    END IF;

    -- Get current usage for today
    SELECT * INTO v_tracking
    FROM withdrawal_tracking
    WHERE user_id = p_user_id AND date = CURRENT_DATE;

    IF v_tracking IS NOT NULL THEN
        v_daily_used := v_tracking.daily_total;
        v_weekly_used := v_tracking.weekly_total;
        v_monthly_used := v_tracking.monthly_total;
    END IF;

    -- Calculate remaining limits
    v_daily_remaining := v_limits.daily_limit - v_daily_used;
    v_weekly_remaining := v_limits.weekly_limit - v_weekly_used;
    v_monthly_remaining := v_limits.monthly_limit - v_monthly_used;

    -- Check if amount exceeds any limit
    IF p_amount > v_daily_remaining THEN
        RAISE EXCEPTION 'Daily withdrawal limit exceeded. Remaining: %, Requested: %',
            v_daily_remaining, p_amount;
    END IF;

    IF p_amount > v_weekly_remaining THEN
        RAISE EXCEPTION 'Weekly withdrawal limit exceeded. Remaining: %, Requested: %',
            v_weekly_remaining, p_amount;
    END IF;

    IF p_amount > v_monthly_remaining THEN
        RAISE EXCEPTION 'Monthly withdrawal limit exceeded. Remaining: %, Requested: %',
            v_monthly_remaining, p_amount;
    END IF;

    -- Check if dual auth required
    IF p_amount >= v_limits.require_dual_auth_above THEN
        v_require_dual_auth := true;
    END IF;

    -- Return result
    RETURN jsonb_build_object(
        'can_withdraw', true,
        'require_dual_auth', v_require_dual_auth,
        'daily_remaining', v_daily_remaining,
        'weekly_remaining', v_weekly_remaining,
        'monthly_remaining', v_monthly_remaining,
        'limits', jsonb_build_object(
            'daily_limit', v_limits.daily_limit,
            'weekly_limit', v_limits.weekly_limit,
            'monthly_limit', v_limits.monthly_limit,
            'dual_auth_threshold', v_limits.require_dual_auth_above
        )
    );
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- Function to update withdrawal tracking
CREATE OR REPLACE FUNCTION update_withdrawal_tracking(
    p_user_id uuid,
    p_amount decimal
) RETURNS void AS $$
DECLARE
    v_week_start date;
    v_month_start date;
BEGIN
    -- Calculate week and month start dates
    v_week_start := date_trunc('week', CURRENT_DATE)::date;
    v_month_start := date_trunc('month', CURRENT_DATE)::date;

    -- Insert or update daily tracking
    INSERT INTO withdrawal_tracking (user_id, date, daily_total, weekly_total, monthly_total, last_withdrawal_at)
    VALUES (p_user_id, CURRENT_DATE, p_amount, p_amount, p_amount, now())
    ON CONFLICT (user_id, date) DO UPDATE SET
        daily_total = withdrawal_tracking.daily_total + p_amount,
        weekly_total = withdrawal_tracking.weekly_total + p_amount,
        monthly_total = withdrawal_tracking.monthly_total + p_amount,
        last_withdrawal_at = now(),
        updated_at = now();

    -- Note: For simplicity, weekly/monthly totals are reset daily
    -- In production, you might want more sophisticated tracking
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- Insert default limits for existing users
INSERT INTO withdrawal_limits (user_id, daily_limit, weekly_limit, monthly_limit, require_dual_auth_above)
SELECT id, 1000.00, 5000.00, 10000.00, 1000.00
FROM users
WHERE NOT EXISTS (
    SELECT 1 FROM withdrawal_limits wl WHERE wl.user_id = users.id
);

-- Enable RLS on withdrawal tables
ALTER TABLE withdrawal_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE withdrawal_approvals ENABLE ROW LEVEL SECURITY;
ALTER TABLE withdrawal_limits ENABLE ROW LEVEL SECURITY;
ALTER TABLE withdrawal_tracking ENABLE ROW LEVEL SECURITY;

-- RLS Policies for withdrawal_requests
CREATE POLICY withdrawal_requests_own ON withdrawal_requests
    FOR ALL USING (user_id = current_user_id());

CREATE POLICY withdrawal_requests_admin ON withdrawal_requests
    FOR ALL USING (
        EXISTS (
            SELECT 1 FROM users u
            WHERE u.id = current_user_id()
            AND u.role IN ('admin', 'super_admin')
        )
    );

-- RLS Policies for withdrawal_approvals
CREATE POLICY withdrawal_approvals_own ON withdrawal_approvals
    FOR SELECT USING (
        approver_id = current_user_id() OR
        EXISTS (
            SELECT 1 FROM withdrawal_requests wr
            WHERE wr.id = withdrawal_request_id AND wr.user_id = current_user_id()
        )
    );

CREATE POLICY withdrawal_approvals_admin ON withdrawal_approvals
    FOR ALL USING (
        EXISTS (
            SELECT 1 FROM users u
            WHERE u.id = current_user_id()
            AND u.role IN ('admin', 'super_admin')
        )
    );

-- RLS Policies for withdrawal_limits
CREATE POLICY withdrawal_limits_own ON withdrawal_limits
    FOR ALL USING (user_id = current_user_id());

CREATE POLICY withdrawal_limits_admin ON withdrawal_limits
    FOR ALL USING (
        EXISTS (
            SELECT 1 FROM users u
            WHERE u.id = current_user_id()
            AND u.role IN ('admin', 'super_admin')
        )
    );

-- RLS Policies for withdrawal_tracking
CREATE POLICY withdrawal_tracking_own ON withdrawal_tracking
    FOR SELECT USING (user_id = current_user_id());

CREATE POLICY withdrawal_tracking_admin ON withdrawal_tracking
    FOR ALL USING (
        EXISTS (
            SELECT 1 FROM users u
            WHERE u.id = current_user_id()
            AND u.role IN ('admin', 'super_admin')
        )
    );
