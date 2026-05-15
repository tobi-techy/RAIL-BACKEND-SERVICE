package entities

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Context signal types
const (
	SignalPaydayDetected   = "payday_detected"
	SignalRecurringExpense = "recurring_expense"
	SignalSpendingSpike    = "spending_spike"
	SignalLowBalance       = "low_balance"
	SignalGoalMilestone    = "goal_milestone"
	SignalUnusualMerchant  = "unusual_merchant"
	SignalTimeOfDay        = "time_of_day"
	SignalLocationHint     = "location_hint"
)

// UserContextSignal represents a detected behavioral pattern.
type UserContextSignal struct {
	ID         uuid.UUID       `json:"id" db:"id"`
	UserID     uuid.UUID       `json:"user_id" db:"user_id"`
	SignalType string          `json:"signal_type" db:"signal_type"`
	SignalData json.RawMessage `json:"signal_data" db:"signal_data"`
	Confidence decimal.Decimal `json:"confidence" db:"confidence"`
	IsActive   bool            `json:"is_active" db:"is_active"`
	LastSeenAt time.Time       `json:"last_seen_at" db:"last_seen_at"`
	CreatedAt  time.Time       `json:"created_at" db:"created_at"`
}

// PaydaySignalData holds detected payday info.
type PaydaySignalData struct {
	Key             string  `json:"key"`
	DayOfMonth      int     `json:"day_of_month"`
	TypicalAmount   float64 `json:"typical_amount"`
	Source          string  `json:"source"`
	OccurrenceCount int     `json:"occurrence_count"`
}

// SpendingSpikeData holds spending anomaly info.
type SpendingSpikeData struct {
	Key           string  `json:"key"`
	Category      string  `json:"category"`
	CurrentAmount float64 `json:"current_amount"`
	AverageAmount float64 `json:"average_amount"`
	SpikeRatio    float64 `json:"spike_ratio"`
}

// EnhancedNudgeRequest extends the basic nudge with context signals.
type EnhancedNudgeRequest struct {
	Screen          string   `json:"screen"`
	Amount          string   `json:"amount,omitempty"`
	Currency        string   `json:"currency,omitempty"`
	TimeOfDay       string   `json:"time_of_day,omitempty"` // "morning", "afternoon", "evening", "night"
	DayOfWeek       int      `json:"day_of_week,omitempty"` // 0=Sun
	DaysUntilPayday int      `json:"days_until_payday,omitempty"`
	MerchantHint    string   `json:"merchant_hint,omitempty"`
	RecentActions   []string `json:"recent_actions,omitempty"`
}

// EnhancedNudgeResponse extends nudge with actionable suggestions.
type EnhancedNudgeResponse struct {
	Show      bool         `json:"show"`
	Message   string       `json:"message,omitempty"`
	Severity  string       `json:"severity"`
	Shake     bool         `json:"shake"`
	Action    *NudgeAction `json:"action,omitempty"`
	ExpiresIn int          `json:"expires_in,omitempty"` // seconds before auto-dismiss
}

// NudgeAction is a one-tap action the user can take from a nudge.
type NudgeAction struct {
	Type        string          `json:"type"`        // "transfer", "open_screen", "confirm"
	Label       string          `json:"label"`       // "Move $50 to stash"
	Destination string          `json:"destination"` // screen or action ID
	Params      json.RawMessage `json:"params,omitempty"`
}
