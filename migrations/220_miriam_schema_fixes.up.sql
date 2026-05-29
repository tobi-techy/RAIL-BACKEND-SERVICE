-- Relax action_type CHECK constraint to allow new mandate types
ALTER TABLE miriam_autopilot_mandates DROP CONSTRAINT IF EXISTS miriam_autopilot_mandates_action_type_check;
ALTER TABLE miriam_autopilot_mandates ADD CONSTRAINT miriam_autopilot_mandates_action_type_check
    CHECK (action_type IN ('transfer_to_stash', 'transfer_to_spend', 'bill_reservation', 'spend_cooldown', 'goal_contribution', 'stash_top_up', 'idle_sweep'));

-- Add missing columns to miriam_money_states
ALTER TABLE miriam_money_states ADD COLUMN IF NOT EXISTS monthly_spend NUMERIC(20, 8) NOT NULL DEFAULT 0;
ALTER TABLE miriam_money_states ADD COLUMN IF NOT EXISTS monthly_savings NUMERIC(20, 8) NOT NULL DEFAULT 0;
ALTER TABLE miriam_money_states ADD COLUMN IF NOT EXISTS spend_balance NUMERIC(20, 8) NOT NULL DEFAULT 0;
ALTER TABLE miriam_money_states ADD COLUMN IF NOT EXISTS stash_balance NUMERIC(20, 8) NOT NULL DEFAULT 0;
ALTER TABLE miriam_money_states ADD COLUMN IF NOT EXISTS calibration_score NUMERIC(10, 4) NOT NULL DEFAULT 0;
