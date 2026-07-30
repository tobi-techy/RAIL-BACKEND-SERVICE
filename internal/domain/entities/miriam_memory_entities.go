package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Fact categories Miriam can learn about a user.
const (
	FactCategoryGoal              = "goal"
	FactCategoryLifeEvent         = "life_event"
	FactCategoryPreference        = "preference"
	FactCategoryHabit             = "habit"
	FactCategoryFear              = "fear"
	FactCategoryFamily            = "family"
	FactCategoryWork              = "work"
	FactCategoryLocation          = "location"
	FactCategoryIdentity          = "identity"
	FactCategoryFinancialBehavior = "financial_behavior"

	FactCategoryIncomePattern   = "income_pattern"
	FactCategoryDepositCadence  = "deposit_cadence"
	FactCategorySalaryDay       = "salary_day"
	FactCategoryFreelancePattern = "freelance_pattern"
	FactCategoryFamilySupport   = "family_support"
	FactCategoryCurrencyContext = "currency_context"
	FactCategoryRiskPreference  = "risk_preference"
	FactCategoryStashBehavior   = "stash_behavior"
)

// Fact sources.
const (
	FactSourceConversation      = "conversation"
	FactSourceInferred          = "inferred"
	FactSourceTransactionPattern = "transaction_pattern"
	FactSourceProfile           = "profile"
)

// MiriamUserFact is a single piece of knowledge Miriam remembers about a user.
type MiriamUserFact struct {
	ID              uuid.UUID       `json:"id" db:"id"`
	UserID          uuid.UUID       `json:"user_id" db:"user_id"`
	Category        string          `json:"category" db:"category"`
	Fact            string          `json:"fact" db:"fact"`
	Source          string          `json:"source" db:"source"`
	Confidence      decimal.Decimal `json:"confidence" db:"confidence"`
	Importance      int             `json:"importance" db:"importance"`
	SupersededBy    *uuid.UUID      `json:"superseded_by,omitempty" db:"superseded_by"`
	Embedding       []byte          `json:"-" db:"-"`
	FirstObservedAt time.Time       `json:"first_observed_at" db:"first_observed_at"`
	LastConfirmedAt time.Time       `json:"last_confirmed_at" db:"last_confirmed_at"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
}

// MiriamMemorySummary is a compressed narrative of all facts for a user.
type MiriamMemorySummary struct {
	UserID           uuid.UUID `json:"user_id" db:"user_id"`
	Summary          string    `json:"summary" db:"summary"`
	FactCount        int       `json:"fact_count" db:"fact_count"`
	LastSummarizedAt time.Time `json:"last_summarized_at" db:"last_summarized_at"`
	CreatedAt        time.Time `json:"created_at" db:"created_at"`
}

// SimilarFact wraps a fact with its cosine distance from a query vector.
type SimilarFact struct {
	MiriamUserFact
	Distance float64 `db:"distance"`
}

// ToneProfile controls how Miriam speaks to a specific user.
// All dimension values range from 0.0 to 1.0.
type MiriamToneProfile struct {
	UserID          uuid.UUID       `json:"user_id" db:"user_id"`
	Formality       decimal.Decimal `json:"formality" db:"formality"`
	Directness      decimal.Decimal `json:"directness" db:"directness"`
	Warmth          decimal.Decimal `json:"warmth" db:"warmth"`
	Humor           decimal.Decimal `json:"humor" db:"humor"`
	Brevity         decimal.Decimal `json:"brevity" db:"brevity"`
	PreferredName   string          `json:"preferred_name" db:"preferred_name"`
	LanguageStyle   string          `json:"language_style" db:"language_style"`
	LocaleStyle     string          `json:"locale_style" db:"locale_style"`
	PersonalityMode string          `json:"personality_mode" db:"personality_mode"` // "default", "roast", "coach", "protector", "celebration", "quiet"
	ControlLevel    string          `json:"control_level" db:"control_level"`       // "full", "guided", "monitor"
	SampleCount     int             `json:"sample_count" db:"sample_count"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at" db:"updated_at"`
}

// Personality mode constants
const (
	PersonalityModeDefault      = "default"
	PersonalityModeRoast        = "roast"
	PersonalityModeCoach        = "coach"
	PersonalityModeProtector    = "protector"
	PersonalityModeCelebration  = "celebration"
	PersonalityModeQuiet        = "quiet"
)

// ControlLevel constants control how autonomous Miriam is for a user.
// MVP default for new users is ControlLevelGuided (suggest + confirm).
// Silent money moves require ControlLevelFull plus an active mandate.
const (
	ControlLevelFull    = "full"    // Full Autopilot — act on pre-approved mandates, ask on new ones
	ControlLevelGuided  = "guided"  // Guided — suggest actions, wait for approval (default)
	ControlLevelMonitor = "monitor" // Manual — only alert and advise, never create actions
)

// --- Financial Event Timeline ---

// EventType constants for miriam_user_events.
const (
	EventSalaryReceived     = "salary_received"
	EventBudgetExceeded     = "budget_exceeded"
	EventGoalCompleted      = "goal_completed"
	EventGoalCreated        = "goal_created"
	EventLargePurchase      = "large_purchase"
	EventSavingsMilestone   = "savings_milestone"
	EventBillPaid           = "bill_paid"
	EventSubscriptionCancelled = "subscription_cancelled"
	EventAccountLinked      = "account_linked"
	EventInvestmentCreated  = "investment_created"
	EventStashTransfer      = "stash_transfer"
	EventAnomalyDetected    = "anomaly_detected"
)

// MiriamUserEvent is a structured financial event on the user's timeline.
type MiriamUserEvent struct {
	ID         uuid.UUID       `json:"id" db:"id"`
	UserID     uuid.UUID       `json:"user_id" db:"user_id"`
	EventType  string          `json:"event_type" db:"event_type"`
	Title      string          `json:"title" db:"title"`
	Detail     string          `json:"detail" db:"detail"`
	Amount     decimal.Decimal `json:"amount" db:"amount"`
	Currency   string          `json:"currency" db:"currency"`
	Metadata   []byte          `json:"metadata" db:"metadata"`
	OccurredAt time.Time       `json:"occurred_at" db:"occurred_at"`
	CreatedAt  time.Time       `json:"created_at" db:"created_at"`
}
