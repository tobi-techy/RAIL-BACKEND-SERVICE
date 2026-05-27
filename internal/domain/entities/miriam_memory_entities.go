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
	UserID        uuid.UUID       `json:"user_id" db:"user_id"`
	Formality     decimal.Decimal `json:"formality" db:"formality"`
	Directness    decimal.Decimal `json:"directness" db:"directness"`
	Warmth        decimal.Decimal `json:"warmth" db:"warmth"`
	Humor         decimal.Decimal `json:"humor" db:"humor"`
	Brevity       decimal.Decimal `json:"brevity" db:"brevity"`
	PreferredName string          `json:"preferred_name" db:"preferred_name"`
	LanguageStyle string          `json:"language_style" db:"language_style"`
	LocaleStyle   string          `json:"locale_style" db:"locale_style"`
	SampleCount   int             `json:"sample_count" db:"sample_count"`
	CreatedAt     time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at" db:"updated_at"`
}
