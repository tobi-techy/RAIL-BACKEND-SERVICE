ALTER TABLE miriam_money_states DROP COLUMN IF EXISTS calibration_score;
ALTER TABLE miriam_money_states DROP COLUMN IF EXISTS stash_balance;
ALTER TABLE miriam_money_states DROP COLUMN IF EXISTS spend_balance;
ALTER TABLE miriam_money_states DROP COLUMN IF EXISTS monthly_savings;
ALTER TABLE miriam_money_states DROP COLUMN IF EXISTS monthly_spend;

ALTER TABLE miriam_autopilot_mandates DROP CONSTRAINT IF EXISTS miriam_autopilot_mandates_action_type_check;
ALTER TABLE miriam_autopilot_mandates ADD CONSTRAINT miriam_autopilot_mandates_action_type_check
    CHECK (action_type IN ('transfer_to_stash'));
