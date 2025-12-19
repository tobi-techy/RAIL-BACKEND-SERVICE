package entities

import (
	"time"

	"github.com/google/uuid"
)

type AuditAction string

const (
	AuditActionLogin            AuditAction = "login"
	AuditActionLogout           AuditAction = "logout"
	AuditActionDeposit          AuditAction = "deposit"
	AuditActionWithdrawal       AuditAction = "withdrawal"
	AuditActionTrade            AuditAction = "trade"
	AuditActionKYCSubmit        AuditAction = "kyc_submit"
	AuditActionKYCApprove       AuditAction = "kyc_approve"
	AuditActionKYCReject        AuditAction = "kyc_reject"
	AuditActionDataExport       AuditAction = "data_export"
	AuditActionDataDelete       AuditAction = "data_delete"
	AuditActionSettingsChange   AuditAction = "settings_change"
	AuditActionStatusTransition AuditAction = "status_transition"
)

type AuditLog struct {
	ID         uuid.UUID              `json:"id" db:"id"`
	UserID     uuid.UUID              `json:"user_id" db:"user_id"`
	Action     AuditAction            `json:"action" db:"action"`
	Resource   string                 `json:"resource" db:"resource"`
	ResourceID *uuid.UUID             `json:"resource_id,omitempty" db:"resource_id"`
	IPAddress  string                 `json:"ip_address" db:"ip_address"`
	UserAgent  string                 `json:"user_agent" db:"user_agent"`
	Metadata   map[string]interface{} `json:"metadata,omitempty" db:"metadata"`
	CreatedAt  time.Time              `json:"created_at" db:"created_at"`
}

// StatusTransitionLog represents a status change event for audit trail
type StatusTransitionLog struct {
	EntityID    uuid.UUID              `json:"entity_id"`
	EntityType  string                 `json:"entity_type"` // deposit, withdrawal, order, etc.
	FromStatus  string                 `json:"from_status"`
	ToStatus    string                 `json:"to_status"`
	TriggeredBy string                 `json:"triggered_by"` // webhook, user, system, scheduler
	Timestamp   time.Time              `json:"timestamp"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

type DataPrivacyRequest struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	UserID      uuid.UUID  `json:"user_id" db:"user_id"`
	RequestType string     `json:"request_type" db:"request_type"`
	Status      string     `json:"status" db:"status"`
	CompletedAt *time.Time `json:"completed_at,omitempty" db:"completed_at"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
}
