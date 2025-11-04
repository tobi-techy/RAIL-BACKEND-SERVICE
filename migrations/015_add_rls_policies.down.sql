-- Remove Row Level Security (RLS) policies

-- Drop policies and disable RLS
DROP POLICY IF EXISTS users_own_data ON users;
DROP POLICY IF EXISTS users_admin_access ON users;
ALTER TABLE users DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS wallet_sets_admin_only ON wallet_sets;
ALTER TABLE wallet_sets DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS managed_wallets_own_data ON managed_wallets;
DROP POLICY IF EXISTS managed_wallets_admin_access ON managed_wallets;
ALTER TABLE managed_wallets DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS deposits_own_data ON deposits;
DROP POLICY IF EXISTS deposits_admin_access ON deposits;
ALTER TABLE deposits DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS balances_own_data ON balances;
DROP POLICY IF EXISTS balances_admin_access ON balances;
ALTER TABLE balances DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS baskets_read_all ON baskets;
DROP POLICY IF EXISTS baskets_write_premium ON baskets;
ALTER TABLE baskets DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS orders_own_data ON orders;
DROP POLICY IF EXISTS orders_admin_access ON orders;
ALTER TABLE orders DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS positions_own_data ON positions;
DROP POLICY IF EXISTS positions_admin_access ON positions;
ALTER TABLE positions DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS audit_logs_own_data ON audit_logs;
ALTER TABLE audit_logs DISABLE ROW LEVEL SECURITY;

-- Drop auth function
DROP FUNCTION IF EXISTS auth.uid();
