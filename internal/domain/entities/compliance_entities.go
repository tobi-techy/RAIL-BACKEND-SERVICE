package entities

import (
	"time"

	"github.com/google/uuid"
)

// ComplianceScreening records every transaction/AML screening sent to Didit.
type ComplianceScreening struct {
	ID              uuid.UUID              `json:"id" db:"id"`
	UserID          uuid.UUID              `json:"user_id" db:"user_id"`
	ScreeningType   string                 `json:"screening_type" db:"screening_type"`     // "transaction", "aml_kyc"
	Direction       string                 `json:"direction" db:"direction"`               // "inbound", "outbound", ""
	Amount          string                 `json:"amount,omitempty" db:"amount"`
	Currency        string                 `json:"currency,omitempty" db:"currency"`
	DiditTxnUUID    string                 `json:"didit_txn_uuid,omitempty" db:"didit_txn_uuid"`
	ReferenceID     string                 `json:"reference_id" db:"reference_id"`         // deposit/withdrawal ID
	Status          string                 `json:"status" db:"status"`                     // APPROVED, IN_REVIEW, DECLINED
	Score           int                    `json:"score" db:"score"`
	Severity        string                 `json:"severity" db:"severity"`                 // LOW, MEDIUM, HIGH, CRITICAL
	Details         map[string]interface{} `json:"details,omitempty" db:"details"`
	CreatedAt       time.Time              `json:"created_at" db:"created_at"`
}

// ComplianceAlert is created when a screening is flagged for review or declined.
type ComplianceAlert struct {
	ID           uuid.UUID `json:"id" db:"id"`
	UserID       uuid.UUID `json:"user_id" db:"user_id"`
	ScreeningID  uuid.UUID `json:"screening_id" db:"screening_id"`
	AlertType    string    `json:"alert_type" db:"alert_type"`       // "transaction_flagged", "transaction_declined", "aml_hit", "sanctions_match"
	Severity     string    `json:"severity" db:"severity"`
	Description  string    `json:"description" db:"description"`
	Status       string    `json:"status" db:"status"`               // "open", "investigating", "resolved", "dismissed"
	ResolvedBy   *string   `json:"resolved_by,omitempty" db:"resolved_by"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty" db:"resolved_at"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
}
