package entities

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const (
	MiriamMandateStatusActive  = "active"
	MiriamMandateStatusPaused  = "paused"
	MiriamMandateStatusExpired = "expired"

	MiriamMandateTransferToStash = "transfer_to_stash"

	MiriamReceiptStatusSuggested = "suggested"
	MiriamReceiptStatusExecuted  = "executed"
	MiriamReceiptStatusSkipped   = "skipped"
	MiriamReceiptStatusFailed    = "failed"

	MiriamFeedbackAccepted  = "accepted"
	MiriamFeedbackIgnored   = "ignored"
	MiriamFeedbackReversed  = "reversed"
	MiriamFeedbackDismissed = "dismissed"
)

// MiriamPredictionOutcome records whether a prediction materialized, enabling
// accuracy tracking and confidence calibration over time.
type MiriamPredictionOutcome struct {
	ID                   uuid.UUID       `json:"id" db:"id"`
	UserID               uuid.UUID       `json:"user_id" db:"user_id"`
	PredictionID         uuid.UUID       `json:"prediction_id" db:"prediction_id"`
	PredictionType       string          `json:"prediction_type" db:"prediction_type"`
	PredictedProbability decimal.Decimal `json:"predicted_probability" db:"predicted_probability"`
	HorizonDays          int             `json:"horizon_days" db:"horizon_days"`
	ThresholdData        json.RawMessage `json:"threshold_data" db:"threshold_data"`
	ActualOutcome        *bool           `json:"actual_outcome" db:"actual_outcome"`
	OutcomeObservedAt    *time.Time      `json:"outcome_observed_at" db:"outcome_observed_at"`
	CreatedAt            time.Time       `json:"created_at" db:"created_at"`
}

// MiriamMoneyState is the durable, periodically refreshed summary Miriam uses
// to make deterministic financial decisions without rebuilding context from
// scratch on every chat request.
type MiriamMoneyState struct {
	UserID                uuid.UUID       `json:"user_id" db:"user_id"`
	IncomeCadence         string          `json:"income_cadence" db:"income_cadence"`
	AvgMonthlyIncome      decimal.Decimal `json:"avg_monthly_income" db:"avg_monthly_income"`
	UpcomingObligations   decimal.Decimal `json:"upcoming_obligations" db:"upcoming_obligations"`
	SafeToSpendDaily      decimal.Decimal `json:"safe_to_spend_daily" db:"safe_to_spend_daily"`
	LiquidityRunwayDays   int             `json:"liquidity_runway_days" db:"liquidity_runway_days"`
	StashTarget           decimal.Decimal `json:"stash_target" db:"stash_target"`
	RecurringSpendMonthly decimal.Decimal `json:"recurring_spend_monthly" db:"recurring_spend_monthly"`
	AnomalyCount          int             `json:"anomaly_count" db:"anomaly_count"`
	ConfidenceLevel       string          `json:"confidence_level" db:"confidence_level"`
	ConfidenceScore       int             `json:"confidence_score" db:"confidence_score"`
	LastEvaluatedAt       time.Time       `json:"last_evaluated_at" db:"last_evaluated_at"`
	Snapshot              json.RawMessage `json:"snapshot" db:"snapshot"`
	CreatedAt             time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at" db:"updated_at"`

	// Transient fields — computed during RefreshMoneyState, not persisted.
	// These are set from real transaction data and used by health score
	// computation, replacing the old SafeToSpendDaily proxy.
	MonthlySpend      decimal.Decimal `json:"-"` // trailing average of total monthly outflow
	MonthlySavings    decimal.Decimal `json:"-"` // trailing average of (deposits − outflow), floored at 0
	SpendBalance      decimal.Decimal `json:"-"` // current spend account balance
	StashBalance      decimal.Decimal `json:"-"` // current stash account balance
	CalibrationScore  int             `json:"-"` // prediction accuracy (0–100), used to scale confidence
}

// MiriamAutopilotMandate is a user-approved bounded permission for Miriam to
// act quietly. It must remain narrow enough to be explainable and revocable.
type MiriamAutopilotMandate struct {
	ID                 uuid.UUID       `json:"id" db:"id"`
	UserID             uuid.UUID       `json:"user_id" db:"user_id"`
	Name               string          `json:"name" db:"name"`
	ActionType         string          `json:"action_type" db:"action_type"`
	Status             string          `json:"status" db:"status"`
	MaxAmountPerAction decimal.Decimal `json:"max_amount_per_action" db:"max_amount_per_action"`
	MaxAmountPerDay    decimal.Decimal `json:"max_amount_per_day" db:"max_amount_per_day"`
	MinSpendBalance    decimal.Decimal `json:"min_spend_balance" db:"min_spend_balance"`
	MinSafeToSpend     decimal.Decimal `json:"min_safe_to_spend" db:"min_safe_to_spend"`
	CooldownMinutes    int             `json:"cooldown_minutes" db:"cooldown_minutes"`
	LastExecutedAt     *time.Time      `json:"last_executed_at,omitempty" db:"last_executed_at"`
	ExpiresAt          *time.Time      `json:"expires_at,omitempty" db:"expires_at"`
	Metadata           json.RawMessage `json:"metadata" db:"metadata"`
	CreatedAt          time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at" db:"updated_at"`
}

// MiriamDecisionReceipt is the user-visible receipt for a quiet action,
// suggestion, skip, or failure.
type MiriamDecisionReceipt struct {
	ID           uuid.UUID       `json:"id" db:"id"`
	UserID       uuid.UUID       `json:"user_id" db:"user_id"`
	MandateID    *uuid.UUID      `json:"mandate_id,omitempty" db:"mandate_id"`
	EventType    string          `json:"event_type" db:"event_type"`
	ActionType   string          `json:"action_type" db:"action_type"`
	Amount       decimal.Decimal `json:"amount" db:"amount"`
	Currency     string          `json:"currency" db:"currency"`
	Status       string          `json:"status" db:"status"`
	Reason       string          `json:"reason" db:"reason"`
	Evidence     json.RawMessage `json:"evidence" db:"evidence"`
	ErrorMessage *string         `json:"error_message,omitempty" db:"error_message"`
	CreatedAt    time.Time       `json:"created_at" db:"created_at"`
}

// MiriamEvent records money events and worker observations that can trigger
// state refreshes, suggestions, or mandate execution.
type MiriamEvent struct {
	ID        uuid.UUID       `json:"id" db:"id"`
	UserID    uuid.UUID       `json:"user_id" db:"user_id"`
	EventType string          `json:"event_type" db:"event_type"`
	Severity  string          `json:"severity" db:"severity"`
	Amount    decimal.Decimal `json:"amount" db:"amount"`
	Currency  string          `json:"currency" db:"currency"`
	Metadata  json.RawMessage `json:"metadata" db:"metadata"`
	CreatedAt time.Time       `json:"created_at" db:"created_at"`
}

// MiriamLearningSignal captures whether a recommendation or action helped.
type MiriamLearningSignal struct {
	ID        uuid.UUID       `json:"id" db:"id"`
	UserID    uuid.UUID       `json:"user_id" db:"user_id"`
	ReceiptID uuid.UUID       `json:"receipt_id" db:"receipt_id"`
	Signal    string          `json:"signal" db:"signal"`
	Weight    decimal.Decimal `json:"weight" db:"weight"`
	Metadata  json.RawMessage `json:"metadata" db:"metadata"`
	CreatedAt time.Time       `json:"created_at" db:"created_at"`
}
