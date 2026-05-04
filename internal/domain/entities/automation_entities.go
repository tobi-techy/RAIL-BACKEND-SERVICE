package entities

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Automation trigger types
const (
	TriggerSchedule         = "schedule"
	TriggerBalanceThreshold = "balance_threshold"
	TriggerIncomeDetected   = "income_detected"
	TriggerSpendingSpike    = "spending_spike"
	TriggerPayday           = "payday"
	TriggerCustom           = "custom"
	TriggerObligationDue    = "obligation_due"
	TriggerLifeEvent        = "life_event"
)

// Automation action types
const (
	ActionTransferToStash    = "transfer_to_stash"
	ActionTransferToSpend    = "transfer_to_spend"
	ActionSendP2P            = "send_p2p"
	ActionSetBudgetAlert     = "set_budget_alert"
	ActionPauseCard          = "pause_card"
	ActionResumeCard         = "resume_card"
	ActionNotify             = "notify"
	ActionCustom             = "custom"
	ActionPauseCardCooldown  = "pause_card_cooldown"
)

// MiriamAutomation represents a user-defined automation rule.
type MiriamAutomation struct {
	ID                uuid.UUID       `json:"id" db:"id"`
	UserID            uuid.UUID       `json:"user_id" db:"user_id"`
	Name              string          `json:"name" db:"name"`
	Description       *string         `json:"description,omitempty" db:"description"`
	TriggerType       string          `json:"trigger_type" db:"trigger_type"`
	TriggerConfig     json.RawMessage `json:"trigger_config" db:"trigger_config"`
	ActionType        string          `json:"action_type" db:"action_type"`
	ActionConfig      json.RawMessage `json:"action_config" db:"action_config"`
	IsActive          bool            `json:"is_active" db:"is_active"`
	LastTriggeredAt   *time.Time      `json:"last_triggered_at,omitempty" db:"last_triggered_at"`
	TriggerCount      int             `json:"trigger_count" db:"trigger_count"`
	MaxTriggersPerDay int             `json:"max_triggers_per_day" db:"max_triggers_per_day"`
	CooldownMinutes   int             `json:"cooldown_minutes" db:"cooldown_minutes"`
	SavingsGoalID     *uuid.UUID      `json:"savings_goal_id,omitempty" db:"savings_goal_id"`
	ObligationID      *uuid.UUID      `json:"obligation_id,omitempty" db:"obligation_id"`
	CreatedAt         time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at" db:"updated_at"`
}

// ScheduleTriggerConfig for cron-like scheduling.
type ScheduleTriggerConfig struct {
	Cron     string `json:"cron,omitempty"`      // e.g. "0 9 * * FRI" (every Friday 9am)
	Weekdays []int  `json:"weekdays,omitempty"`  // 0=Sun, 1=Mon, ...
	Hour     int    `json:"hour,omitempty"`
	Minute   int    `json:"minute,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

// BalanceThresholdConfig triggers when balance crosses a threshold.
type BalanceThresholdConfig struct {
	Wallet    string  `json:"wallet"`    // "spend" or "stash"
	Operator  string  `json:"operator"`  // "below", "above"
	Threshold float64 `json:"threshold"`
}

// TransferActionConfig for stash/spend transfers.
type TransferActionConfig struct {
	Amount     float64 `json:"amount"`
	FromWallet string  `json:"from_wallet"` // "spend" or "stash"
	ToWallet   string  `json:"to_wallet"`
}

// ObligationDueTriggerConfig triggers N days before an obligation is due.
type ObligationDueTriggerConfig struct {
	DaysBeforeDue int `json:"days_before_due"` // e.g. 3 = trigger 3 days before due
}

// PauseCardCooldownConfig pauses the card then resumes after a cooldown.
type PauseCardCooldownConfig struct {
	CooldownMinutes int    `json:"cooldown_minutes"` // how long to pause
	Message         string `json:"message,omitempty"` // notification message
}

// NotifyActionConfig for notification-only automations.
type NotifyActionConfig struct {
	Title   string `json:"title"`
	Message string `json:"message"`
	Channel string `json:"channel,omitempty"` // push, email, in_app; defaults to push
}

// LifeEventTriggerConfig for aspirational triggers Miriam detects.
type LifeEventTriggerConfig struct {
	EventType string  `json:"event_type"` // "income_increase", "income_decrease", "new_recurring_expense", "expense_removed"
	Threshold float64 `json:"threshold,omitempty"` // e.g. 0.20 = 20% increase
}

// MiriamAutomationLog records each automation execution.
type MiriamAutomationLog struct {
	ID           uuid.UUID       `json:"id" db:"id"`
	AutomationID uuid.UUID      `json:"automation_id" db:"automation_id"`
	UserID       uuid.UUID       `json:"user_id" db:"user_id"`
	Status       string          `json:"status" db:"status"` // success, failed, skipped
	TriggerData  json.RawMessage `json:"trigger_data,omitempty" db:"trigger_data"`
	ResultData   json.RawMessage `json:"result_data,omitempty" db:"result_data"`
	ErrorMessage *string         `json:"error_message,omitempty" db:"error_message"`
	ExecutedAt   time.Time       `json:"executed_at" db:"executed_at"`
}
