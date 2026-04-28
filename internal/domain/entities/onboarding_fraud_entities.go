package entities

import (
	"time"

	"github.com/google/uuid"
)

// FraudRiskLevel classifies the severity of a fraud risk assessment.
type FraudRiskLevel string

const (
	FraudRiskLow      FraudRiskLevel = "low"
	FraudRiskMedium   FraudRiskLevel = "medium"
	FraudRiskHigh     FraudRiskLevel = "high"
	FraudRiskCritical FraudRiskLevel = "critical"
)

// FraudRiskAction is the enforcement decision from a fraud assessment.
type FraudRiskAction string

const (
	FraudActionAllow        FraudRiskAction = "allow"
	FraudActionFlag         FraudRiskAction = "flag"
	FraudActionDelayFunding FraudRiskAction = "delay_funding"
	FraudActionManualReview FraudRiskAction = "manual_review"
	FraudActionBlock        FraudRiskAction = "block"
)

// DeviceAccountLink records that a specific device fingerprint was used by a user.
// The critical insight: legitimate users have 1 device → 1 account.
// Fraud rings have 1 device → many accounts.
type DeviceAccountLink struct {
	ID                uuid.UUID `db:"id"`
	DeviceFingerprint string    `db:"device_fingerprint"`
	UserID            uuid.UUID `db:"user_id"`
	IPAddress         string    `db:"ip_address"`
	UserAgent         string    `db:"user_agent"`
	EventType         string    `db:"event_type"` // "onboarding", "deposit", "login"
	CreatedAt         time.Time `db:"created_at"`
}

// OnboardingRiskAssessment is the result of evaluating a user at onboarding or first deposit.
type OnboardingRiskAssessment struct {
	ID                uuid.UUID              `db:"id"`
	UserID            uuid.UUID              `db:"user_id"`
	EventType         string                 `db:"event_type"` // "onboarding_complete", "first_deposit"
	RiskScore         float64                `db:"risk_score"`  // 0.0 - 1.0
	RiskLevel         FraudRiskLevel         `db:"risk_level"`
	Action            FraudRiskAction        `db:"action"`
	Signals           map[string]interface{} `db:"signals"`
	IPAddress         string                 `db:"ip_address"`
	DeviceFingerprint string                 `db:"device_fingerprint"`
	CreatedAt         time.Time              `db:"created_at"`
}

// OnboardingRiskSignal is a single fraud indicator contributing to a risk score.
type OnboardingRiskSignal struct {
	Type   string  `json:"type"`
	Score  float64 `json:"score"`  // 0.0 - 1.0 contribution
	Weight float64 `json:"weight"` // multiplier for this signal type
	Detail string  `json:"detail"`
}
