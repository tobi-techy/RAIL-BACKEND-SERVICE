package entities

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// WithdrawalStatus represents the status of a withdrawal request
type WithdrawalStatus string

const (
	WithdrawalStatusPending    WithdrawalStatus = "pending"
	WithdrawalStatusApproved   WithdrawalStatus = "approved"
	WithdrawalStatusProcessing WithdrawalStatus = "processing"
	WithdrawalStatusCompleted  WithdrawalStatus = "completed"
	WithdrawalStatusFailed     WithdrawalStatus = "failed"
	WithdrawalStatusRejected   WithdrawalStatus = "rejected"
	WithdrawalStatusExpired    WithdrawalStatus = "expired"
	WithdrawalStatusCancelled  WithdrawalStatus = "cancelled"
)

// IsValid checks if the withdrawal status is valid
func (s WithdrawalStatus) IsValid() bool {
	switch s {
	case WithdrawalStatusPending, WithdrawalStatusApproved, WithdrawalStatusProcessing,
		WithdrawalStatusCompleted, WithdrawalStatusFailed, WithdrawalStatusRejected,
		WithdrawalStatusExpired, WithdrawalStatusCancelled:
		return true
	}
	return false
}

// WithdrawalRequest represents a withdrawal request
type WithdrawalRequest struct {
	ID                 uuid.UUID              `json:"id" db:"id"`
	UserID             uuid.UUID              `json:"user_id" db:"user_id"`
	WalletID           uuid.UUID              `json:"wallet_id" db:"wallet_id"`
	Amount             decimal.Decimal        `json:"amount" db:"amount"`
	Currency           string                 `json:"currency" db:"currency"`
	DestinationAddress string                 `json:"destination_address" db:"destination_address"`
	Blockchain         string                 `json:"blockchain" db:"blockchain"`
	Status             WithdrawalStatus       `json:"status" db:"status"`
	ApprovalRequired   bool                   `json:"approval_required" db:"approval_required"`
	ApprovedBy         *uuid.UUID             `json:"approved_by,omitempty" db:"approved_by"`
	ApprovedAt         *time.Time             `json:"approved_at,omitempty" db:"approved_at"`
	ExpiresAt          time.Time              `json:"expires_at" db:"expires_at"`
	RejectionReason    *string                `json:"rejection_reason,omitempty" db:"rejection_reason"`
	RejectedBy         *uuid.UUID             `json:"rejected_by,omitempty" db:"rejected_by"`
	RejectedAt         *time.Time             `json:"rejected_at,omitempty" db:"rejected_at"`
	IdempotencyKey     *string                `json:"idempotency_key,omitempty" db:"idempotency_key"`
	Metadata           map[string]interface{} `json:"metadata,omitempty" db:"metadata"`
	CreatedAt          time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time              `json:"updated_at" db:"updated_at"`

	// Related entities (not stored in DB)
	Wallet *ManagedWallet `json:"wallet,omitempty"`
	User   *User          `json:"user,omitempty"`
}

// Validate validates the withdrawal request
func (wr *WithdrawalRequest) Validate() error {
	if wr.UserID == uuid.Nil {
		return fmt.Errorf("user_id is required")
	}
	if wr.WalletID == uuid.Nil {
		return fmt.Errorf("wallet_id is required")
	}
	if wr.Amount.IsZero() || wr.Amount.IsNegative() {
		return fmt.Errorf("amount must be positive")
	}
	if wr.Currency == "" {
		return fmt.Errorf("currency is required")
	}
	if wr.DestinationAddress == "" {
		return fmt.Errorf("destination_address is required")
	}
	if wr.Blockchain == "" {
		return fmt.Errorf("blockchain is required")
	}
	if !wr.Status.IsValid() {
		return fmt.Errorf("invalid status: %s", wr.Status)
	}
	if wr.ExpiresAt.IsZero() {
		return fmt.Errorf("expires_at is required")
	}
	return nil
}

// IsExpired checks if the withdrawal request has expired
func (wr *WithdrawalRequest) IsExpired() bool {
	return time.Now().After(wr.ExpiresAt)
}

// CanBeApproved checks if the withdrawal can be approved
func (wr *WithdrawalRequest) CanBeApproved() bool {
	return wr.Status == WithdrawalStatusPending && !wr.IsExpired()
}

// CanBeRejected checks if the withdrawal can be rejected
func (wr *WithdrawalRequest) CanBeRejected() bool {
	return wr.Status == WithdrawalStatusPending && !wr.IsExpired()
}

// WithdrawalApproval represents an approval for a withdrawal request
type WithdrawalApproval struct {
	ID                  uuid.UUID `json:"id" db:"id"`
	WithdrawalRequestID uuid.UUID `json:"withdrawal_request_id" db:"withdrawal_request_id"`
	ApproverID          uuid.UUID `json:"approver_id" db:"approver_id"`
	ApprovalLevel       int       `json:"approval_level" db:"approval_level"`
	ApprovedAt          time.Time `json:"approved_at" db:"approved_at"`
	Notes               *string   `json:"notes,omitempty" db:"notes"`
	CreatedAt           time.Time `json:"created_at" db:"created_at"`

	// Related entities (not stored in DB)
	Approver *User `json:"approver,omitempty"`
}

// WithdrawalLimits represents user withdrawal limits
type WithdrawalLimits struct {
	ID                   uuid.UUID       `json:"id" db:"id"`
	UserID               uuid.UUID       `json:"user_id" db:"user_id"`
	DailyLimit           decimal.Decimal `json:"daily_limit" db:"daily_limit"`
	WeeklyLimit          decimal.Decimal `json:"weekly_limit" db:"weekly_limit"`
	MonthlyLimit         decimal.Decimal `json:"monthly_limit" db:"monthly_limit"`
	RequireDualAuthAbove decimal.Decimal `json:"require_dual_auth_above" db:"require_dual_auth_above"`
	IsActive             bool            `json:"is_active" db:"is_active"`
	CreatedAt            time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at" db:"updated_at"`
}

// Validate validates the withdrawal limits
func (wl *WithdrawalLimits) Validate() error {
	if wl.UserID == uuid.Nil {
		return fmt.Errorf("user_id is required")
	}
	if wl.DailyLimit.IsNegative() {
		return fmt.Errorf("daily_limit cannot be negative")
	}
	if wl.WeeklyLimit.IsNegative() {
		return fmt.Errorf("weekly_limit cannot be negative")
	}
	if wl.MonthlyLimit.IsNegative() {
		return fmt.Errorf("monthly_limit cannot be negative")
	}
	if wl.RequireDualAuthAbove.IsNegative() {
		return fmt.Errorf("require_dual_auth_above cannot be negative")
	}
	return nil
}

// WithdrawalTracking represents withdrawal usage tracking
type WithdrawalTracking struct {
	ID               uuid.UUID       `json:"id" db:"id"`
	UserID           uuid.UUID       `json:"user_id" db:"user_id"`
	Date             time.Time       `json:"date" db:"date"`
	DailyTotal       decimal.Decimal `json:"daily_total" db:"daily_total"`
	WeeklyTotal      decimal.Decimal `json:"weekly_total" db:"weekly_total"`
	MonthlyTotal     decimal.Decimal `json:"monthly_total" db:"monthly_total"`
	LastWithdrawalAt *time.Time      `json:"last_withdrawal_at,omitempty" db:"last_withdrawal_at"`
	CreatedAt        time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at" db:"updated_at"`
}

// === API Request/Response Models ===

// CreateWithdrawalRequest represents a withdrawal request
type CreateWithdrawalRequest struct {
	WalletID           uuid.UUID `json:"walletId" validate:"required"`
	Amount             string    `json:"amount" validate:"required,gt=0"`
	Currency           string    `json:"currency" validate:"required,oneof=USDC"`
	DestinationAddress string    `json:"destinationAddress" validate:"required"`
	Blockchain         string    `json:"blockchain" validate:"required,oneof=SOL-DEVNET"`
	IdempotencyKey     *string   `json:"idempotencyKey,omitempty"`
}

// WithdrawalResponse represents a withdrawal request response
type WithdrawalResponse struct {
	ID                 uuid.UUID        `json:"id"`
	UserID             uuid.UUID        `json:"userId"`
	WalletID           uuid.UUID        `json:"walletId"`
	Amount             string           `json:"amount"`
	Currency           string           `json:"currency"`
	DestinationAddress string           `json:"destinationAddress"`
	Blockchain         string           `json:"blockchain"`
	Status             WithdrawalStatus `json:"status"`
	ApprovalRequired   bool             `json:"approvalRequired"`
	ApprovedBy         *uuid.UUID       `json:"approvedBy,omitempty"`
	ApprovedAt         *time.Time       `json:"approvedAt,omitempty"`
	ExpiresAt          time.Time        `json:"expiresAt"`
	CreatedAt          time.Time        `json:"createdAt"`
	UpdatedAt          time.Time        `json:"updatedAt"`
}

// ApproveWithdrawalRequest represents an approval request
type ApproveWithdrawalRequest struct {
	WithdrawalID uuid.UUID `json:"withdrawalId" validate:"required"`
	Notes        *string   `json:"notes,omitempty"`
}

// RejectWithdrawalRequest represents a rejection request
type RejectWithdrawalRequest struct {
	WithdrawalID    uuid.UUID `json:"withdrawalId" validate:"required"`
	RejectionReason string    `json:"rejectionReason" validate:"required"`
	Notes           *string   `json:"notes,omitempty"`
}

// WithdrawalLimitsRequest represents a limits update request
type WithdrawalLimitsRequest struct {
	DailyLimit           *string `json:"dailyLimit,omitempty"`
	WeeklyLimit          *string `json:"weeklyLimit,omitempty"`
	MonthlyLimit         *string `json:"monthlyLimit,omitempty"`
	RequireDualAuthAbove *string `json:"requireDualAuthAbove,omitempty"`
}

// WithdrawalLimitsResponse represents withdrawal limits response
type WithdrawalLimitsResponse struct {
	DailyLimit           string `json:"dailyLimit"`
	WeeklyLimit          string `json:"weeklyLimit"`
	MonthlyLimit         string `json:"monthlyLimit"`
	RequireDualAuthAbove string `json:"requireDualAuthAbove"`
	DailyUsed            string `json:"dailyUsed"`
	WeeklyUsed           string `json:"weeklyUsed"`
	MonthlyUsed          string `json:"monthlyUsed"`
	DailyRemaining       string `json:"dailyRemaining"`
	WeeklyRemaining      string `json:"weeklyRemaining"`
	MonthlyRemaining     string `json:"monthlyRemaining"`
}

// WithdrawalListResponse represents a paginated list of withdrawals
type WithdrawalListResponse struct {
	Withdrawals []*WithdrawalResponse `json:"withdrawals"`
	NextCursor  *string               `json:"nextCursor,omitempty"`
	TotalCount  int                   `json:"totalCount"`
}
