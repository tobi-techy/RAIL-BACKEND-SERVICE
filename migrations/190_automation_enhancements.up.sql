-- Automation Enhancements: bill shield, spending spike cooldown, goal-linked, life events

-- 1. Add new trigger types: obligation_due, life_event
ALTER TABLE miriam_automations DROP CONSTRAINT IF EXISTS miriam_automations_trigger_type_check;
ALTER TABLE miriam_automations ADD CONSTRAINT miriam_automations_trigger_type_check CHECK (trigger_type IN (
    'schedule', 'balance_threshold', 'income_detected', 'spending_spike', 'payday', 'custom',
    'obligation_due', 'life_event'
));

-- 2. Widen action type constraint (pause_card, resume_card already listed but add pause_card_cooldown)
ALTER TABLE miriam_automations DROP CONSTRAINT IF EXISTS miriam_automations_action_type_check;
ALTER TABLE miriam_automations ADD CONSTRAINT miriam_automations_action_type_check CHECK (action_type IN (
    'transfer_to_stash', 'transfer_to_spend', 'send_p2p', 'set_budget_alert',
    'pause_card', 'resume_card', 'notify', 'custom',
    'pause_card_cooldown'
));

-- 3. Add optional FK columns for goal-linked and obligation-linked automations
ALTER TABLE miriam_automations ADD COLUMN IF NOT EXISTS savings_goal_id UUID REFERENCES shared_goals(id) ON DELETE SET NULL;
ALTER TABLE miriam_automations ADD COLUMN IF NOT EXISTS obligation_id UUID REFERENCES financial_obligations(id) ON DELETE SET NULL;

-- 4. Index for goal-linked deactivation checks
CREATE INDEX IF NOT EXISTS idx_miriam_automations_goal ON miriam_automations(savings_goal_id) WHERE savings_goal_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_miriam_automations_obligation ON miriam_automations(obligation_id) WHERE obligation_id IS NOT NULL;
