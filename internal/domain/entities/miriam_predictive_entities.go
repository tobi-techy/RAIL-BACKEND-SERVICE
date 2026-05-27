package entities

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Prediction types Miriam can generate.
const (
	PredictionCashShortfall    = "cash_shortfall"
	PredictionBillPressure     = "bill_pressure"
	PredictionSpendingAnomaly  = "spending_anomaly"
	PredictionIncomeGap        = "income_gap"
	PredictionIdleSurplus      = "idle_surplus"
	PredictionStashOpportunity = "stash_opportunity"
)

// Prediction horizons.
const (
	Horizon3Day  = "3_day"
	Horizon7Day  = "7_day"
	Horizon14Day = "14_day"
	Horizon30Day = "30_day"
)

// Severity levels for predictions.
const (
	SeverityLow      = "low"
	SeverityMedium   = "medium"
	SeverityHigh     = "high"
	SeverityCritical = "critical"
)

// MiriamPrediction is a forward-looking forecast about a user's financial state.
type MiriamPrediction struct {
	ID              uuid.UUID       `json:"id" db:"id"`
	UserID          uuid.UUID       `json:"user_id" db:"user_id"`
	PredictionType  string          `json:"prediction_type" db:"prediction_type"`
	Horizon         string          `json:"horizon" db:"horizon"`
	Probability     decimal.Decimal `json:"probability" db:"probability"`
	Severity        string          `json:"severity" db:"severity"`
	ProjectedAmount decimal.Decimal `json:"projected_amount" db:"projected_amount"`
	Reasoning       string          `json:"reasoning" db:"reasoning"`
	DataSnapshot    json.RawMessage `json:"data_snapshot" db:"data_snapshot"`
	ExpiresAt       time.Time       `json:"expires_at" db:"expires_at"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
}

// PredictionSummary aggregates all active predictions for a user into a
// single risk assessment with a recommended action.
type PredictionSummary struct {
	UserID            uuid.UUID          `json:"user_id"`
	ActivePredictions []MiriamPrediction `json:"active_predictions"`
	RiskScore         int                `json:"risk_score"` // 0–100
	TopRisk           string             `json:"top_risk,omitempty"`
	RecommendedAction string             `json:"recommended_action,omitempty"`
}
