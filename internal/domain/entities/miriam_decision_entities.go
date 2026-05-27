package entities

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Decision types Miriam can make.
const (
	DecisionExecute      = "execute"
	DecisionDelay        = "delay"
	DecisionAdjustAmount = "adjust_amount"
	DecisionSkip         = "skip"
	DecisionEscalate     = "escalate"
)

// MiriamDecision records the decision Miriam made for a mandate evaluation,
// including what factors influenced it and the adjusted amount.
type MiriamDecision struct {
	ID              uuid.UUID       `json:"id" db:"id"`
	UserID          uuid.UUID       `json:"user_id" db:"user_id"`
	MandateID       uuid.UUID       `json:"mandate_id" db:"mandate_id"`
	DecisionType    string          `json:"decision_type" db:"decision_type"`
	Reason          string          `json:"reason" db:"reason"`
	ConfidenceScore int             `json:"confidence_score" db:"confidence_score"`
	Factors         json.RawMessage `json:"factors" db:"factors"`
	OriginalAmount  decimal.Decimal `json:"original_amount" db:"original_amount"`
	AdjustedAmount  decimal.Decimal `json:"adjusted_amount" db:"adjusted_amount"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
}

// DecisionContext is the full context available when Miriam makes a decision.
type DecisionContext struct {
	MoneyState     *MiriamMoneyState
	Predictions    *PredictionSummary
	MemoryFacts    []MiriamUserFact
	LearningBias   decimal.Decimal
	RecentReceipts []MiriamDecisionReceipt
	Mandate        MiriamAutopilotMandate
	EventType      string
}

// DecisionOutcome records whether a Miriam decision was well-received.
type MiriamDecisionOutcome struct {
	ID            uuid.UUID       `json:"id" db:"id"`
	UserID        uuid.UUID       `json:"user_id" db:"user_id"`
	DecisionID    uuid.UUID       `json:"decision_id" db:"decision_id"`
	Outcome       string          `json:"outcome" db:"outcome"`
	OutcomeDelay  time.Duration   `json:"outcome_delay" db:"outcome_delay"`
	FeedbackScore decimal.Decimal `json:"feedback_score" db:"feedback_score"`
	Metadata      json.RawMessage `json:"metadata" db:"metadata"`
	CreatedAt     time.Time       `json:"created_at" db:"created_at"`
}

// LearningModel tracks Miriam's learned bias per category for a user.
type MiriamLearningModel struct {
	UserID           uuid.UUID       `json:"user_id" db:"user_id"`
	Category         string          `json:"category" db:"category"`
	TotalDecisions   int             `json:"total_decisions" db:"total_decisions"`
	PositiveOutcomes int             `json:"positive_outcomes" db:"positive_outcomes"`
	NegativeOutcomes int             `json:"negative_outcomes" db:"negative_outcomes"`
	CurrentBias      decimal.Decimal `json:"current_bias" db:"current_bias"`
	ConfidenceWeight decimal.Decimal `json:"confidence_weight" db:"confidence_weight"`
	LastUpdatedAt    time.Time       `json:"last_updated_at" db:"last_updated_at"`
}

// Outcome constants.
const (
	OutcomeUserHappy     = "user_happy"
	OutcomeUserReversed  = "user_reversed"
	OutcomeUserIgnored   = "user_ignored"
	OutcomeUserDismissed = "user_dismissed"
	OutcomeSystemError   = "system_error"
)

// Mandate suggestion from the autonomous suggestion engine.
type MiriamMandateSuggestion struct {
	ID                  uuid.UUID       `json:"id" db:"id"`
	UserID              uuid.UUID       `json:"user_id" db:"user_id"`
	Name                string          `json:"name" db:"name"`
	ActionType          string          `json:"action_type" db:"action_type"`
	Reasoning           string          `json:"reasoning" db:"reasoning"`
	SuggestedMaxAmount  decimal.Decimal `json:"suggested_max_amount" db:"suggested_max_amount"`
	SuggestedMaxDay     decimal.Decimal `json:"suggested_max_day" db:"suggested_max_day"`
	SuggestedMinBalance decimal.Decimal `json:"suggested_min_balance" db:"suggested_min_balance"`
	SuggestedCooldown   int             `json:"suggested_cooldown_minutes" db:"suggested_cooldown"`
	Confidence          int             `json:"confidence" db:"confidence"` // 0-100
	Status              string          `json:"status" db:"status"`         // pending, accepted, dismissed
	CreatedAt           time.Time       `json:"created_at" db:"created_at"`
	DismissedAt         *time.Time      `json:"dismissed_at,omitempty" db:"dismissed_at"`
	AcceptedAt          *time.Time      `json:"accepted_at,omitempty" db:"accepted_at"`
}

// Financial health score tracked over time.
type MiriamFinancialHealthScore struct {
	ID             uuid.UUID       `json:"id" db:"id"`
	UserID         uuid.UUID       `json:"user_id" db:"user_id"`
	OverallScore   int             `json:"overall_score" db:"overall_score"` // 0-100
	BudgetScore    int             `json:"budget_score" db:"budget_score"`
	SavingsScore   int             `json:"savings_score" db:"savings_score"`
	DebtScore      int             `json:"debt_score" db:"debt_score"`
	RunwayScore    int             `json:"runway_score" db:"runway_score"`
	StabilityScore int             `json:"stability_score" db:"stability_score"`
	Trend          string          `json:"trend" db:"trend"` // "improving", "stable", "declining"
	PreviousScore  int             `json:"previous_score" db:"previous_score"`
	Reasoning      string          `json:"reasoning" db:"reasoning"`
	DataSnapshot   json.RawMessage `json:"data_snapshot" db:"data_snapshot"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
}
