package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// === Risk Scoring (Feature 2) ===

type TxRiskLevel string
type TxRiskAction string

const (
	TxRiskLevelLow      TxRiskLevel = "low"
	TxRiskLevelMedium   TxRiskLevel = "medium"
	TxRiskLevelHigh     TxRiskLevel = "high"
	TxRiskLevelCritical TxRiskLevel = "critical"

	TxRiskActionAllow      TxRiskAction = "allow"
	TxRiskActionStepUpAuth TxRiskAction = "step_up_auth"
	TxRiskActionFlagReview TxRiskAction = "flag_review"
	TxRiskActionBlock      TxRiskAction = "block"
)

type TransactionRiskAssessment struct {
	ID              uuid.UUID              `json:"id" db:"id"`
	UserID          uuid.UUID              `json:"user_id" db:"user_id"`
	TransactionType string                 `json:"transaction_type" db:"transaction_type"`
	Amount          decimal.Decimal        `json:"amount" db:"amount"`
	RiskScore       float64                `json:"risk_score" db:"risk_score"`
	RiskLevel       TxRiskLevel            `json:"risk_level" db:"risk_level"`
	Action          TxRiskAction           `json:"action" db:"action"`
	Signals         map[string]interface{} `json:"signals" db:"signals"`
	CreatedAt       time.Time              `json:"created_at" db:"created_at"`
}

// === Address Whitelist (Feature 3) ===

type WhitelistStatus string

const (
	WhitelistStatusPending WhitelistStatus = "pending"
	WhitelistStatusActive  WhitelistStatus = "active"
	WhitelistStatusRemoved WhitelistStatus = "removed"
)

type WhitelistedAddress struct {
	ID           uuid.UUID       `json:"id" db:"id"`
	UserID       uuid.UUID       `json:"user_id" db:"user_id"`
	Chain        string          `json:"chain" db:"chain"`
	Address      string          `json:"address" db:"address"`
	Label        string          `json:"label" db:"label"`
	Status       WhitelistStatus `json:"status" db:"status"`
	CoolingUntil *time.Time      `json:"cooling_until" db:"cooling_until"`
	CreatedAt    time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at" db:"updated_at"`
}

// === Session Anomaly (Feature 4) ===

type AnomalyType string

const (
	AnomalyImpossibleTravel  AnomalyType = "impossible_travel"
	AnomalyConcurrentCountry AnomalyType = "concurrent_country"
	AnomalyUserAgentChange   AnomalyType = "user_agent_change"
)

type SessionAnomaly struct {
	ID          uuid.UUID              `json:"id" db:"id"`
	UserID      uuid.UUID              `json:"user_id" db:"user_id"`
	AnomalyType AnomalyType           `json:"anomaly_type" db:"anomaly_type"`
	Severity    string                 `json:"severity" db:"severity"`
	Details     map[string]interface{} `json:"details" db:"details"`
	IPAddress   string                 `json:"ip_address" db:"ip_address"`
	Country     string                 `json:"country" db:"country"`
	City        string                 `json:"city" db:"city"`
	CreatedAt   time.Time              `json:"created_at" db:"created_at"`
}

// === Withdrawal Limits (Feature 5) ===

type WithdrawalTier int

const (
	WithdrawalTier1 WithdrawalTier = 1
	WithdrawalTier2 WithdrawalTier = 2
	WithdrawalTier3 WithdrawalTier = 3
)

type WithdrawalLimitUsage struct {
	ID         uuid.UUID       `json:"id" db:"id"`
	UserID     uuid.UUID       `json:"user_id" db:"user_id"`
	Amount     decimal.Decimal `json:"amount" db:"amount"`
	PeriodType string          `json:"period_type" db:"period_type"`
	PeriodKey  string          `json:"period_key" db:"period_key"`
	CreatedAt  time.Time       `json:"created_at" db:"created_at"`
}

// === MFA Challenge (Feature 7) ===

type MFAChallengeType string

const (
	MFAChallengeOTPEmail  MFAChallengeType = "otp_email"
	MFAChallengeOTPSMS    MFAChallengeType = "otp_sms"
	MFAChallengeBiometric MFAChallengeType = "biometric"
)

type MFAChallenge struct {
	ID            uuid.UUID        `json:"id" db:"id"`
	UserID        uuid.UUID        `json:"user_id" db:"user_id"`
	ChallengeType MFAChallengeType `json:"challenge_type" db:"challenge_type"`
	CodeHash      string           `json:"-" db:"code_hash"`
	ExpiresAt     time.Time        `json:"expires_at" db:"expires_at"`
	Verified      bool             `json:"verified" db:"verified"`
	Attempts      int              `json:"attempts" db:"attempts"`
	CreatedAt     time.Time        `json:"created_at" db:"created_at"`
}

// === Device Trust (Feature 1) ===

type DeviceTrustLevel string

const (
	DeviceTrustTrusted    DeviceTrustLevel = "trusted"
	DeviceTrustNew        DeviceTrustLevel = "new"
	DeviceTrustSuspicious DeviceTrustLevel = "suspicious"
)
