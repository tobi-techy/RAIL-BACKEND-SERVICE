-- Miriam Agent Features: Automations, Receipt Split Tracking, Context Signals, Collaborative Goals

-- 1. Proactive Automations (rules engine)
CREATE TABLE IF NOT EXISTS miriam_automations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(200) NOT NULL,
    description TEXT,
    trigger_type VARCHAR(50) NOT NULL CHECK (trigger_type IN (
        'schedule', 'balance_threshold', 'income_detected', 'spending_spike', 'payday', 'custom'
    )),
    trigger_config JSONB NOT NULL DEFAULT '{}',
    action_type VARCHAR(50) NOT NULL CHECK (action_type IN (
        'transfer_to_stash', 'transfer_to_spend', 'send_p2p', 'set_budget_alert',
        'pause_card', 'resume_card', 'notify', 'custom'
    )),
    action_config JSONB NOT NULL DEFAULT '{}',
    is_active BOOLEAN NOT NULL DEFAULT true,
    last_triggered_at TIMESTAMP WITH TIME ZONE,
    trigger_count INTEGER NOT NULL DEFAULT 0,
    max_triggers_per_day INTEGER DEFAULT 3,
    cooldown_minutes INTEGER DEFAULT 60,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_miriam_automations_user ON miriam_automations(user_id, is_active);
CREATE INDEX idx_miriam_automations_trigger ON miriam_automations(trigger_type, is_active);

CREATE TABLE IF NOT EXISTS miriam_automation_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    automation_id UUID NOT NULL REFERENCES miriam_automations(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status VARCHAR(20) NOT NULL CHECK (status IN ('success', 'failed', 'skipped')),
    trigger_data JSONB,
    result_data JSONB,
    error_message TEXT,
    executed_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_automation_logs_automation ON miriam_automation_logs(automation_id, executed_at DESC);
CREATE INDEX idx_automation_logs_user ON miriam_automation_logs(user_id, executed_at DESC);

-- 2. Receipt Split Tracking (enhanced collection flow)
CREATE TABLE IF NOT EXISTS receipt_splits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    receipt_id UUID NOT NULL REFERENCES receipt_scans(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    split_type VARCHAR(20) NOT NULL CHECK (split_type IN ('equal', 'custom', 'by_item')),
    total_amount DECIMAL(20,6) NOT NULL,
    your_share DECIMAL(20,6) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'partial', 'collected', 'expired')),
    message TEXT,
    expires_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_receipt_splits_user ON receipt_splits(user_id, status, created_at DESC);

CREATE TABLE IF NOT EXISTS receipt_split_participants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    split_id UUID NOT NULL REFERENCES receipt_splits(id) ON DELETE CASCADE,
    rail_tag VARCHAR(100) NOT NULL,
    participant_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    amount DECIMAL(20,6) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'requested', 'paid', 'declined', 'expired')),
    p2p_transfer_id UUID,
    reminder_count INTEGER NOT NULL DEFAULT 0,
    last_reminded_at TIMESTAMP WITH TIME ZONE,
    paid_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_split_participants_split ON receipt_split_participants(split_id);
CREATE INDEX idx_split_participants_user ON receipt_split_participants(participant_user_id, status);

-- 3. Context Signals (multi-modal awareness)
CREATE TABLE IF NOT EXISTS user_context_signals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    signal_type VARCHAR(50) NOT NULL CHECK (signal_type IN (
        'payday_detected', 'recurring_expense', 'spending_spike', 'low_balance',
        'goal_milestone', 'unusual_merchant', 'time_of_day', 'location_hint'
    )),
    signal_data JSONB NOT NULL DEFAULT '{}',
    confidence DECIMAL(3,2) NOT NULL DEFAULT 0.5,
    is_active BOOLEAN NOT NULL DEFAULT true,
    last_seen_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_context_signals_user ON user_context_signals(user_id, is_active, signal_type);
CREATE UNIQUE INDEX idx_context_signals_unique ON user_context_signals(user_id, signal_type, (signal_data->>'key'))
    WHERE signal_data ? 'key';

-- 4. Collaborative Goals
CREATE TABLE IF NOT EXISTS shared_goals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(200) NOT NULL,
    description TEXT,
    target_amount DECIMAL(20,6) NOT NULL,
    current_amount DECIMAL(20,6) NOT NULL DEFAULT 0,
    currency VARCHAR(10) NOT NULL DEFAULT 'USD',
    deadline TIMESTAMP WITH TIME ZONE,
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'completed', 'cancelled', 'expired')),
    visibility VARCHAR(20) NOT NULL DEFAULT 'members' CHECK (visibility IN ('private', 'members', 'public')),
    icon_name VARCHAR(50) DEFAULT 'target-02',
    celebration_message TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_shared_goals_creator ON shared_goals(creator_id, status);

CREATE TABLE IF NOT EXISTS shared_goal_members (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    goal_id UUID NOT NULL REFERENCES shared_goals(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role VARCHAR(20) NOT NULL DEFAULT 'member' CHECK (role IN ('creator', 'admin', 'member')),
    target_contribution DECIMAL(20,6),
    total_contributed DECIMAL(20,6) NOT NULL DEFAULT 0,
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('invited', 'active', 'left', 'removed')),
    invited_by UUID REFERENCES users(id),
    joined_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE (goal_id, user_id)
);
CREATE INDEX idx_goal_members_user ON shared_goal_members(user_id, status);
CREATE INDEX idx_goal_members_goal ON shared_goal_members(goal_id, status);

CREATE TABLE IF NOT EXISTS shared_goal_contributions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    goal_id UUID NOT NULL REFERENCES shared_goals(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount DECIMAL(20,6) NOT NULL,
    note TEXT,
    source VARCHAR(30) DEFAULT 'manual' CHECK (source IN ('manual', 'automation', 'roundup', 'stash_transfer')),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_goal_contributions_goal ON shared_goal_contributions(goal_id, created_at DESC);
CREATE INDEX idx_goal_contributions_user ON shared_goal_contributions(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS shared_goal_invites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    goal_id UUID NOT NULL REFERENCES shared_goals(id) ON DELETE CASCADE,
    inviter_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    rail_tag VARCHAR(100) NOT NULL,
    invitee_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'declined', 'expired')),
    message TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    responded_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_goal_invites_invitee ON shared_goal_invites(invitee_user_id, status);
CREATE INDEX idx_goal_invites_goal ON shared_goal_invites(goal_id, status);
