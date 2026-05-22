package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// === Fraud Rules Engine ===

type FraudRuleType string

const (
	FraudRuleVelocity FraudRuleType = "velocity"
	FraudRuleAmount   FraudRuleType = "amount"
	FraudRulePattern  FraudRuleType = "pattern"
	FraudRuleGeo      FraudRuleType = "geo"
	FraudRuleDevice   FraudRuleType = "device"
	FraudRuleCustom   FraudRuleType = "custom"
)

type FraudRuleAction string

const (
	RuleActionAllow        FraudRuleAction = "allow"
	RuleActionFlag         FraudRuleAction = "flag"
	RuleActionBlock        FraudRuleAction = "block"
	RuleActionFreeze       FraudRuleAction = "freeze"
	RuleActionManualReview FraudRuleAction = "manual_review"
)

type FraudRule struct {
	ID          uuid.UUID              `json:"id" db:"id"`
	Name        string                 `json:"name" db:"name"`
	Description string                 `json:"description" db:"description"`
	RuleType    FraudRuleType          `json:"rule_type" db:"rule_type"`
	Conditions  map[string]interface{} `json:"conditions" db:"conditions"`
	Action      FraudRuleAction        `json:"action" db:"action"`
	Severity    string                 `json:"severity" db:"severity"`
	ScoreWeight float64                `json:"score_weight" db:"score_weight"`
	IsActive    bool                   `json:"is_active" db:"is_active"`
	AppliesTo   string                 `json:"applies_to" db:"applies_to"`
	CreatedBy   string                 `json:"created_by" db:"created_by"`
	CreatedAt   time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at" db:"updated_at"`
}

// === Fraud Alerts ===

type FraudAlertStatus string

const (
	AlertStatusOpen          FraudAlertStatus = "open"
	AlertStatusInvestigating FraudAlertStatus = "investigating"
	AlertStatusResolved      FraudAlertStatus = "resolved"
	AlertStatusDismissed     FraudAlertStatus = "dismissed"
)

type FraudRuleAlert struct {
	ID              uuid.UUID              `json:"id" db:"id"`
	UserID          uuid.UUID              `json:"user_id" db:"user_id"`
	RuleID          *uuid.UUID             `json:"rule_id" db:"rule_id"`
	AlertType       string                 `json:"alert_type" db:"alert_type"`
	Severity        string                 `json:"severity" db:"severity"`
	Status          FraudAlertStatus       `json:"status" db:"status"`
	Details         map[string]interface{} `json:"details" db:"details"`
	TransactionID   *uuid.UUID             `json:"transaction_id" db:"transaction_id"`
	TransactionType string                 `json:"transaction_type" db:"transaction_type"`
	Amount          decimal.Decimal        `json:"amount" db:"amount"`
	ResolvedBy      string                 `json:"resolved_by" db:"resolved_by"`
	ResolvedAt      *time.Time             `json:"resolved_at" db:"resolved_at"`
	ResolutionNotes string                 `json:"resolution_notes" db:"resolution_notes"`
	CreatedAt       time.Time              `json:"created_at" db:"created_at"`
}

// === Account Freezes ===

type AccountFreeze struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	UserID         uuid.UUID  `json:"user_id" db:"user_id"`
	FreezeType     string     `json:"freeze_type" db:"freeze_type"`
	Reason         string     `json:"reason" db:"reason"`
	TriggeredBy    string     `json:"triggered_by" db:"triggered_by"`
	AlertID        *uuid.UUID `json:"alert_id" db:"alert_id"`
	IsActive       bool       `json:"is_active" db:"is_active"`
	UnfrozenBy     string     `json:"unfrozen_by" db:"unfrozen_by"`
	UnfrozenAt     *time.Time `json:"unfrozen_at" db:"unfrozen_at"`
	UnfreezeReason string     `json:"unfreeze_reason" db:"unfreeze_reason"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
}

// === Sanctions Screening ===

type SanctionsStatus string

const (
	SanctionsStatusClear          SanctionsStatus = "clear"
	SanctionsStatusPotentialMatch SanctionsStatus = "potential_match"
	SanctionsStatusConfirmedMatch SanctionsStatus = "confirmed_match"
	SanctionsStatusFalsePositive  SanctionsStatus = "false_positive"
)

type SanctionsCheck struct {
	ID           uuid.UUID              `json:"id" db:"id"`
	UserID       uuid.UUID              `json:"user_id" db:"user_id"`
	CheckType    string                 `json:"check_type" db:"check_type"`
	FullName     string                 `json:"full_name" db:"full_name"`
	ListsChecked []string               `json:"lists_checked" db:"lists_checked"`
	MatchFound   bool                   `json:"match_found" db:"match_found"`
	MatchDetails map[string]interface{} `json:"match_details" db:"match_details"`
	MatchScore   float64                `json:"match_score" db:"match_score"`
	Status       SanctionsStatus        `json:"status" db:"status"`
	ReviewedBy   string                 `json:"reviewed_by" db:"reviewed_by"`
	ReviewedAt   *time.Time             `json:"reviewed_at" db:"reviewed_at"`
	CreatedAt    time.Time              `json:"created_at" db:"created_at"`
}

// === Fund-Through Detection ===

type FundThroughDetection struct {
	ID                  uuid.UUID       `json:"id" db:"id"`
	UserID              uuid.UUID       `json:"user_id" db:"user_id"`
	DepositID           *uuid.UUID      `json:"deposit_id" db:"deposit_id"`
	WithdrawalID        *uuid.UUID      `json:"withdrawal_id" db:"withdrawal_id"`
	DepositAmount       decimal.Decimal `json:"deposit_amount" db:"deposit_amount"`
	WithdrawalAmount    decimal.Decimal `json:"withdrawal_amount" db:"withdrawal_amount"`
	TimeBetweenSeconds  int             `json:"time_between_seconds" db:"time_between_seconds"`
	WithdrawalRatio     float64         `json:"withdrawal_ratio" db:"withdrawal_ratio"`
	RiskScore           float64         `json:"risk_score" db:"risk_score"`
	ActionTaken         string          `json:"action_taken" db:"action_taken"`
	CreatedAt           time.Time       `json:"created_at" db:"created_at"`
}

// === Transaction Event (for monitoring worker) ===

type MonitoredTransaction struct {
	ID          uuid.UUID       `json:"id"`
	UserID      uuid.UUID       `json:"user_id"`
	Type        string          `json:"type"` // "deposit", "withdrawal"
	Amount      decimal.Decimal `json:"amount"`
	Currency    string          `json:"currency"`
	Source      string          `json:"source"`      // funding source or destination
	DeviceID    string          `json:"device_id"`
	IPAddress   string          `json:"ip_address"`
	CreatedAt   time.Time       `json:"created_at"`
}

// RuleEvalResult is the outcome of evaluating a single rule against a transaction.
type RuleEvalResult struct {
	RuleID    uuid.UUID `json:"rule_id"`
	RuleName  string    `json:"rule_name"`
	Triggered bool      `json:"triggered"`
	Score     float64   `json:"score"`
	Action    FraudRuleAction `json:"action"`
	Details   string    `json:"details"`
}
