# Database Schema

**Version:** v0.2  
**Last Updated:** October 24, 2025

## Navigation
- **Previous:** [Workflows](./04-workflows.md)
- **Next:** [Source Tree](./06-source-tree.md)
- **[Index](./README.md)**

---

## 8. Database Schema

Based on the data models defined in [Data Models](./02-data-models.md) and using PostgreSQL as our database, here is the initial DDL (Data Definition Language) for the MVP schema.

```sql
-- Extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Enums (Consider creating ENUM types for status fields for better type safety)
CREATE TYPE kyc_status AS ENUM ('not_started', 'pending', 'approved', 'rejected');
CREATE TYPE wallet_status AS ENUM ('creating', 'active', 'inactive');
CREATE TYPE chain_type AS ENUM ('ethereum', 'solana'); -- Add more supported chains
CREATE TYPE token_type AS ENUM ('USDC');
CREATE TYPE deposit_status AS ENUM (
    'pending_confirmation',
    'confirmed_on_chain',
    'off_ramp_initiated',
    'off_ramp_complete',
    'broker_funded',
    'failed'
);
CREATE TYPE withdrawal_status AS ENUM (
    'pending',
    'broker_withdrawal_initiated',
    'broker_withdrawal_complete',
    'on_ramp_initiated',
    'on_ramp_complete',
    'transfer_initiated',
    'complete',
    'failed'
);
CREATE TYPE asset_type AS ENUM ('basket', 'option', 'stock', 'etf');
CREATE TYPE order_side AS ENUM ('buy', 'sell');
CREATE TYPE order_status AS ENUM (
    'pending',
    'accepted_by_broker',
    'partially_filled',
    'filled',
    'failed',
    'canceled'
);
CREATE TYPE risk_level AS ENUM ('conservative', 'balanced', 'growth');

-- Tables
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    auth_provider_id VARCHAR(255) UNIQUE NOT NULL,
    email VARCHAR(255) UNIQUE,
    phone_number VARCHAR(50) UNIQUE,
    kyc_status kyc_status DEFAULT 'not_started' NOT NULL,
    passcode_hash VARCHAR(255), -- Store hashed passcode
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL
);
CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_phone ON users(phone_number);

CREATE TABLE wallets (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chain chain_type NOT NULL,
    address VARCHAR(255) NOT NULL,
    circle_wallet_id VARCHAR(255) NOT NULL,
    status wallet_status DEFAULT 'creating' NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL,
    UNIQUE (user_id, chain) -- Assuming one wallet per chain per user initially
);
CREATE INDEX idx_wallets_user_id ON wallets(user_id);
CREATE INDEX idx_wallets_address ON wallets(address);

CREATE TABLE deposits (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    chain chain_type NOT NULL,
    tx_hash VARCHAR(255) NOT NULL,
    token token_type DEFAULT 'USDC' NOT NULL,
    amount_stablecoin DECIMAL(36, 18) NOT NULL, -- High precision for crypto
    status deposit_status DEFAULT 'pending_confirmation' NOT NULL,
    confirmed_at TIMESTAMPTZ,
    off_ramp_initiated_at TIMESTAMPTZ,
    off_ramp_completed_at TIMESTAMPTZ,
    broker_funded_at TIMESTAMPTZ,
    failure_reason TEXT,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL,
    UNIQUE (chain, tx_hash)
);
CREATE INDEX idx_deposits_user_id ON deposits(user_id);
CREATE INDEX idx_deposits_status ON deposits(status);

CREATE TABLE withdrawals (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount_usd DECIMAL(18, 2) NOT NULL, -- Amount requested from broker
    target_chain chain_type NOT NULL,
    target_address VARCHAR(255) NOT NULL,
    status withdrawal_status DEFAULT 'pending' NOT NULL,
    broker_withdrawal_ref VARCHAR(255),
    circle_on_ramp_ref VARCHAR(255),
    circle_transfer_ref VARCHAR(255),
    tx_hash VARCHAR(255), -- Final on-chain tx hash
    failure_reason TEXT,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL
);
CREATE INDEX idx_withdrawals_user_id ON withdrawals(user_id);
CREATE INDEX idx_withdrawals_status ON withdrawals(status);


CREATE TABLE balances (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    buying_power_usd DECIMAL(18, 2) DEFAULT 0.00 NOT NULL, -- Brokerage balance
    pending_broker_deposits_usd DECIMAL(18, 2) DEFAULT 0.00 NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL
);

CREATE TABLE baskets (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    risk_level risk_level,
    composition_json JSONB NOT NULL, -- [{"symbol": "XYZ", "weight": 0.5}, ...]
    is_active BOOLEAN DEFAULT TRUE NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL
);

CREATE TABLE orders (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    basket_id UUID REFERENCES baskets(id), -- Nullable for options/direct stocks
    asset_type asset_type NOT NULL,
    option_details_json JSONB, -- Store option contract specifics here
    side order_side NOT NULL,
    amount_usd DECIMAL(18, 2) NOT NULL, -- Target amount/value
    status order_status DEFAULT 'pending' NOT NULL,
    Alpaca_order_ref VARCHAR(255) UNIQUE,
    failure_reason TEXT,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL
);
CREATE INDEX idx_orders_user_id ON orders(user_id);
CREATE INDEX idx_orders_status ON orders(status);
CREATE INDEX idx_orders_Alpaca_ref ON orders(Alpaca_order_ref);

-- Consider if positions are fully derived or need caching
CREATE TABLE positions_cache (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    symbol VARCHAR(100) NOT NULL,
    quantity DECIMAL(36, 18) NOT NULL, -- High precision might be needed
    average_price DECIMAL(18, 6),
    market_value DECIMAL(18, 2),
    asset_type asset_type NOT NULL,
    last_updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, symbol, asset_type) -- Composite key
);

CREATE TABLE ai_summaries (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    week_start_date DATE NOT NULL,
    summary_markdown TEXT NOT NULL,
    generated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL,
    UNIQUE (user_id, week_start_date)
);
CREATE INDEX idx_ai_summaries_user_id ON ai_summaries(user_id);

-- Trigger function to update 'updated_at' columns
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
   NEW.updated_at = NOW();
   RETURN NEW;
END;
$$ language 'plpgsql';

-- Apply trigger to tables with 'updated_at'
CREATE TRIGGER update_users_updated_at BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_wallets_updated_at BEFORE UPDATE ON wallets FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_deposits_updated_at BEFORE UPDATE ON deposits FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_withdrawals_updated_at BEFORE UPDATE ON withdrawals FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_balances_updated_at BEFORE UPDATE ON balances FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_baskets_updated_at BEFORE UPDATE ON baskets FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_orders_updated_at BEFORE UPDATE ON orders FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
```

---

**Next:** [Source Tree](./06-source-tree.md)
