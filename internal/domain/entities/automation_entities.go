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
	TriggerDepositReceived  = "deposit_received"
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
	ActionPayUtilityBill     = "pay_utility_bill"
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

// PayBillActionConfig pays a bill to a real payee (Rail tag, email, or phone)
// via P2P when the linked obligation comes due. Non-Rail payees receive a
// claim link and can claim to their bank.
type PayBillActionConfig struct {
	PayeeIdentifier string  `json:"payee_identifier"` // RailTag, email, or phone
	PayeeName       string  `json:"payee_name,omitempty"`
	Amount          float64 `json:"amount"`
	BillName        string  `json:"bill_name,omitempty"`
}

// PayUtilityBillActionConfig pays a Nigerian utility bill (airtime, data,
// electricity, cable TV, betting, transport) via Airbills when the schedule or
// linked obligation fires. Gated by the standard transfer-consent fields so a
// recurring auto-pay runs only within the 90-day reauthorization window.
type PayUtilityBillActionConfig struct {
	Category      string     `json:"category"`                 // airtime, data, electricity, cable, betting, transport
	Recipient     string     `json:"recipient"`                // phone / meter / smartcard / betting id
	NetworkID     string     `json:"network_id,omitempty"`     // 01..04 for airtime/data
	ProdID        string     `json:"prod_id,omitempty"`        // plan/provider id
	ElectID       string     `json:"elect_id,omitempty"`       // electricity disco id
	AmountNGN     float64    `json:"amount_ngn"`               // face value
	BeneficiaryID *uuid.UUID `json:"beneficiary_id,omitempty"` // optional saved beneficiary
	BillName      string     `json:"bill_name,omitempty"`
}

// NotifyActionConfig for notification-only automations.
type NotifyActionConfig struct {
	Title   string `json:"title"`
	Message string `json:"message"`
	Channel string `json:"channel,omitempty"` // push, email, in_app; defaults to push
}

// IncomeDetectedTriggerConfig triggers when a deposit arrives or income changes.
type IncomeDetectedTriggerConfig struct {
	EventType string  `json:"event_type,omitempty"` // "income_increase", "income_decrease"; empty = any deposit
	Threshold float64 `json:"threshold,omitempty"`   // min change ratio vs trailing avg; 0 = any deposit
}

// SpendingSpikeTriggerConfig triggers when spending exceeds normal levels.
type SpendingSpikeTriggerConfig struct {
	SpikeRatio float64 `json:"spike_ratio,omitempty"` // e.g. 1.5 = 50% above normal; 0 = default 1.5
}

// PaydayTriggerConfig triggers around the user's detected payday.
type PaydayTriggerConfig struct {
	DaysBefore int `json:"days_before,omitempty"` // trigger N days before detected payday; 0 = on payday
	DaysAfter  int `json:"days_after,omitempty"`  // trigger N days after detected payday
}

// LifeEventTriggerConfig for aspirational triggers Miriam detects.
type LifeEventTriggerConfig struct {
	EventType string  `json:"event_type"`            // "income_increase", "income_decrease", "new_recurring_expense", "expense_removed"
	Threshold float64 `json:"threshold,omitempty"`   // e.g. 0.20 = 20% increase
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
