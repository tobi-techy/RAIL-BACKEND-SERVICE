-- Revert automation enhancements

DROP INDEX IF EXISTS idx_miriam_automations_obligation;
DROP INDEX IF EXISTS idx_miriam_automations_goal;

ALTER TABLE miriam_automations DROP COLUMN IF EXISTS obligation_id;
ALTER TABLE miriam_automations DROP COLUMN IF EXISTS savings_goal_id;

ALTER TABLE miriam_automations DROP CONSTRAINT IF EXISTS miriam_automations_action_type_check;
ALTER TABLE miriam_automations ADD CONSTRAINT miriam_automations_action_type_check CHECK (action_type IN (
    'transfer_to_stash', 'transfer_to_spend', 'send_p2p', 'set_budget_alert',
    'pause_card', 'resume_card', 'notify', 'custom'
));

ALTER TABLE miriam_automations DROP CONSTRAINT IF EXISTS miriam_automations_trigger_type_check;
ALTER TABLE miriam_automations ADD CONSTRAINT miriam_automations_trigger_type_check CHECK (trigger_type IN (
    'schedule', 'balance_threshold', 'income_detected', 'spending_spike', 'payday', 'custom'
));
