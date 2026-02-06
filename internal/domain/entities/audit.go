package entities

import (
	"crypto/sha256"
	"encoding/hex"
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
	AuditActionAccountCreate    AuditAction = "account_create"
	AuditActionAccountUpdate    AuditAction = "account_update"
	AuditActionAccountDelete    AuditAction = "account_delete"
	AuditActionAPIKeyCreate     AuditAction = "api_key_create"
	AuditActionAPIKeyRevoke     AuditAction = "api_key_revoke"
	AuditActionPasswordChange   AuditAction = "password_change"
	AuditActionMFAEnable        AuditAction = "mfa_enable"
	AuditActionMFADisable       AuditAction = "mfa_disable"
	AuditActionPermissionChange AuditAction = "permission_change"
	AuditActionAdminAction      AuditAction = "admin_action"
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

	// WORM (Write Once Read Many) integrity fields
	PreviousHash       string     `json:"previous_hash,omitempty" db:"previous_hash"`             // SHA-256 hash of previous log entry
	CurrentHash        string     `json:"current_hash,omitempty" db:"current_hash"`               // SHA-256 hash of this entry
	VerifiedAt         *time.Time `json:"verified_at,omitempty" db:"verified_at"`                 // Last integrity verification time
	VerificationStatus string     `json:"verification_status,omitempty" db:"verification_status"` // pending, verified, tampered
}

func (al *AuditLog) CalculateHash() string {
	hashInput := al.ID.String() +
		al.UserID.String() +
		string(al.Action) +
		al.Resource +
		al.IPAddress +
		al.CreatedAt.Format(time.RFC3339Nano) +
		al.PreviousHash

	hash := sha256.Sum256([]byte(hashInput))
	return hex.EncodeToString(hash[:])
}

func (al *AuditLog) SetIntegrityFields(previousHash string) {
	al.PreviousHash = previousHash
	al.CurrentHash = al.CalculateHash()
	al.VerificationStatus = "pending"
}

func (al *AuditLog) IsTampered() bool {
	if al.VerificationStatus == "verified" {
		return al.CurrentHash != al.CalculateHash()
	}
	return false
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

// AuditComplianceReport represents a compliance report for SOC2/PCI-DSS
type AuditComplianceReport struct {
	ID                   uuid.UUID        `json:"id"`
	ReportType           string           `json:"report_type"` // soc2, pci_dss
	PeriodStart          time.Time        `json:"period_start"`
	PeriodEnd            time.Time        `json:"period_end"`
	GeneratedAt          time.Time        `json:"generated_at"`
	TotalEvents          int64            `json:"total_events"`
	UniqueUsers          int64            `json:"unique_users"`
	ActionBreakdown      map[string]int64 `json:"action_breakdown"`
	SecurityEvents       int64            `json:"security_events"`
	FailedLogins         int64            `json:"failed_logins"`
	DataExports          int64            `json:"data_exports"`
	PermissionChanges    int64            `json:"permission_changes"`
	IntegrityCheckStatus string           `json:"integrity_check_status"`
	HashChainValid       bool             `json:"hash_chain_valid"`
}

// WORMStorageConfig holds configuration for WORM storage
type WORMStorageConfig struct {
	Enabled        bool   `mapstructure:"enabled"`
	S3Bucket       string `mapstructure:"s3_bucket"`
	S3Region       string `mapstructure:"s3_region"`
	AWSRoleARN     string `mapstructure:"aws_role_arn"`
	RetentionDays  int    `mapstructure:"retention_days"` // 2555 days (7 years) for compliance
	ImmutableTable string `mapstructure:"immutable_table"`
}
