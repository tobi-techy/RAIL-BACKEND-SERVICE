-- Add Row Level Security (RLS) policies for enhanced data protection
-- Enable RLS on critical tables

-- Create current_user_id function for RLS policies
CREATE OR REPLACE FUNCTION current_user_id() RETURNS uuid AS $$
    SELECT nullif(current_setting('app.user_id', true), '')::uuid;
$$ LANGUAGE sql SECURITY DEFINER;

-- Enable RLS on users table
ALTER TABLE users ENABLE ROW LEVEL SECURITY;

-- Policy: Users can only see their own data
CREATE POLICY users_own_data ON users
    FOR ALL USING (current_user_id() = id);

-- Policy: Admins can see all user data
CREATE POLICY users_admin_access ON users
    FOR ALL USING (
        EXISTS (
            SELECT 1 FROM users u
            WHERE u.id = current_user_id()
            AND u.role IN ('admin', 'super_admin')
        )
    );

-- Enable RLS on wallet_sets table
ALTER TABLE wallet_sets ENABLE ROW LEVEL SECURITY;

-- Policy: Only admins can manage wallet sets
CREATE POLICY wallet_sets_admin_only ON wallet_sets
    FOR ALL USING (
        EXISTS (
            SELECT 1 FROM users u
            WHERE u.id = current_user_id()
            AND u.role IN ('admin', 'super_admin')
        )
    );

-- Enable RLS on managed_wallets table
ALTER TABLE managed_wallets ENABLE ROW LEVEL SECURITY;

-- Policy: Users can only access their own wallets
CREATE POLICY managed_wallets_own_data ON managed_wallets
    FOR ALL USING (
        user_id = current_user_id()
    );

-- Policy: Admins can access all wallets
CREATE POLICY managed_wallets_admin_access ON managed_wallets
    FOR ALL USING (
        EXISTS (
            SELECT 1 FROM users u
            WHERE u.id = current_user_id()
            AND u.role IN ('admin', 'super_admin')
        )
    );

-- Enable RLS on deposits table
ALTER TABLE deposits ENABLE ROW LEVEL SECURITY;

-- Policy: Users can only see their own deposits
CREATE POLICY deposits_own_data ON deposits
    FOR ALL USING (
        user_id = current_user_id()
    );

-- Policy: Admins can see all deposits
CREATE POLICY deposits_admin_access ON deposits
    FOR ALL USING (
        EXISTS (
            SELECT 1 FROM users u
            WHERE u.id = current_user_id()
            AND u.role IN ('admin', 'super_admin')
        )
    );

-- Enable RLS on balances table
ALTER TABLE balances ENABLE ROW LEVEL SECURITY;

-- Policy: Users can only access their own balance
CREATE POLICY balances_own_data ON balances
    FOR ALL USING (
        user_id = current_user_id()
    );

-- Policy: Admins can access all balances
CREATE POLICY balances_admin_access ON balances
    FOR ALL USING (
        EXISTS (
            SELECT 1 FROM users u
            WHERE u.id = current_user_id()
            AND u.role IN ('admin', 'super_admin')
        )
    );

-- Enable RLS on baskets table
ALTER TABLE baskets ENABLE ROW LEVEL SECURITY;

-- Policy: All authenticated users can read baskets (they are curated/public)
CREATE POLICY baskets_read_all ON baskets
    FOR SELECT USING (current_user_id() IS NOT NULL);

-- Policy: Only premium/trader users can create/modify baskets
CREATE POLICY baskets_write_premium ON baskets
    FOR ALL USING (
        EXISTS (
            SELECT 1 FROM users u
            WHERE u.id = current_user_id()
            AND u.role IN ('premium', 'trader', 'admin', 'super_admin')
        )
    );

-- Enable RLS on orders table
ALTER TABLE orders ENABLE ROW LEVEL SECURITY;

-- Policy: Users can only access their own orders
CREATE POLICY orders_own_data ON orders
    FOR ALL USING (
        user_id = current_user_id()
    );

-- Policy: Admins can access all orders
CREATE POLICY orders_admin_access ON orders
    FOR ALL USING (
        EXISTS (
            SELECT 1 FROM users u
            WHERE u.id = current_user_id()
            AND u.role IN ('admin', 'super_admin')
        )
    );

-- Enable RLS on positions table
ALTER TABLE positions ENABLE ROW LEVEL SECURITY;

-- Policy: Users can only access their own positions
CREATE POLICY positions_own_data ON positions
    FOR ALL USING (
        user_id = current_user_id()
    );

-- Policy: Admins can access all positions
CREATE POLICY positions_admin_access ON positions
    FOR ALL USING (
        EXISTS (
            SELECT 1 FROM users u
            WHERE u.id = current_user_id()
            AND u.role IN ('admin', 'super_admin')
        )
    );

-- Enable RLS on audit_logs table
ALTER TABLE audit_logs ENABLE ROW LEVEL SECURITY;

-- Policy: Users can only see audit logs related to their data
CREATE POLICY audit_logs_own_data ON audit_logs
    FOR SELECT USING (
        user_id = current_user_id() OR
        EXISTS (
            SELECT 1 FROM users u
            WHERE u.id = current_user_id()
            AND u.role IN ('admin', 'super_admin')
        )
    );

-- Note: In production, you would grant appropriate permissions to application roles
-- For now, relying on connection pooler to handle user context

-- Note: This RLS implementation assumes the use of a connection pooler that can set
-- the app.user_id session variable for each request. In production, this would be
-- handled by your application connection middleware.
-- The current_user_id() function is defined at the beginning of this migration.
