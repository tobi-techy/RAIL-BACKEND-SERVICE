-- Rail Stock Sleeve seed (Stocklana Day 1).
--
-- Binds the Rail-curated strategy row to the live Glider tenant strategy
-- "Rail Stock Sleeve" (Solana only, AAPLx 40 / NVDAx 30 / TSLAx 30, daily
-- rebalance). Asset mints were resolved from Jupiter's live token registry and
-- accepted by Glider /strategies/validate before the tenant strategy was
-- created. Nothing here is invented.
--
-- Re-run safety: every statement is idempotent, and conflicts never clobber
-- operator state. A re-run only re-binds the provider strategy id; it never
-- resets status/current_version (so a deliberately paused sleeve stays
-- paused) and never overwrites curated asset rows.

INSERT INTO investment_assets (id, caip19, symbol, name, asset_class, chain, decimals, allowlisted, prohibited, source, priority)
VALUES
    ('a1ee1001-5eed-4a1e-8aa1-0000000000a1',
     'solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:XsbEhLAtcf6HdfpFZ5xEMdqW8nfAvcsP5bdudRLJzJp',
     'AAPLx', 'Apple xStock', 'equity', 'solana', 8, true, false, 'glider', 100),
    ('b2ee1002-5eed-4a1e-8aa1-0000000000b2',
     'solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:Xsc9qvGR1efVDFGLrVsmkzv3qi45LTBjeUKSPmx9qEh',
     'NVDAx', 'NVIDIA xStock', 'equity', 'solana', 8, true, false, 'glider', 100),
    ('c3ee1003-5eed-4a1e-8aa1-0000000000c3',
     'solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:XsDoVfqeBukxuZHWhdvWHBhgEHjGNst4MLodqsJHzoB',
     'TSLAx', 'Tesla xStock', 'equity', 'solana', 8, true, false, 'glider', 100)
ON CONFLICT (caip19) DO NOTHING;

INSERT INTO investment_strategies (id, user_id, owner_type, glider_strategy_id, name, description, objective, risk, horizon, status, current_version, is_public, created_by)
VALUES (
    'd15c0ba1-71c0-4a11-8aa1-000000000001',
    NULL,
    'rail',
    '01M333PQNR9Y2WJ4D78FSRECEQ',
    'Rail Stock Sleeve',
    'Diversified Solana stock sleeve: AAPLx 40 / NVDAx 30 / TSLAx 30, daily rebalance.',
    'Long-term diversified equity exposure on Solana',
    'medium',
    'long',
    'ACTIVE',
    1,
    false,
    'system'
)
ON CONFLICT (id) DO UPDATE SET
    glider_strategy_id = EXCLUDED.glider_strategy_id,
    updated_at = NOW();

INSERT INTO investment_strategy_versions (id, strategy_id, version, target_allocation, risk, horizon, rebalance_rules, contribution_rules, execution_rules, constraints, rationale, glider_strategy_version, created_by)
VALUES (
    'e5aa10ca-710c-4a11-8aa1-000000000001',
    'd15c0ba1-71c0-4a11-8aa1-000000000001',
    1,
    '[
        {"caip19": "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:XsbEhLAtcf6HdfpFZ5xEMdqW8nfAvcsP5bdudRLJzJp", "symbol": "AAPLx", "weight": 40},
        {"caip19": "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:Xsc9qvGR1efVDFGLrVsmkzv3qi45LTBjeUKSPmx9qEh", "symbol": "NVDAx", "weight": 30},
        {"caip19": "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:XsDoVfqeBukxuZHWhdvWHBhgEHjGNst4MLodqsJHzoB", "symbol": "TSLAx", "weight": 30}
    ]'::jsonb,
    'medium',
    'long',
    '{"type": "scheduled", "frequency": "daily", "prefer_contributions": true, "allow_sells": true}'::jsonb,
    '{"type": "manual", "source": "stash"}'::jsonb,
    '{"slippage_bps": 50}'::jsonb,
    '{}'::jsonb,
    'Day-1 Stocklana sleeve: three live Solana xStocks, validated against Glider before creation.',
    1,
    'system'
)
ON CONFLICT (strategy_id, version) DO NOTHING;
