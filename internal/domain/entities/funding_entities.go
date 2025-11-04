package entities

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// VirtualAccountStatus represents the status of a virtual account
type VirtualAccountStatus string

const (
	VirtualAccountStatusCreating VirtualAccountStatus = "creating"
	VirtualAccountStatusActive   VirtualAccountStatus = "active"
	VirtualAccountStatusInactive VirtualAccountStatus = "inactive"
	VirtualAccountStatusFailed   VirtualAccountStatus = "failed"
)

// VirtualAccount represents a Due API virtual account for USDC to USD conversion
type VirtualAccount struct {
	ID                 uuid.UUID            `json:"id" db:"id"`
	UserID             uuid.UUID            `json:"user_id" db:"user_id"`
	DueAccountID       string               `json:"due_account_id" db:"due_account_id"`
	BrokerageAccountID string               `json:"brokerage_account_id,omitempty" db:"brokerage_account_id"`
	Status             VirtualAccountStatus `json:"status" db:"status"`
	CreatedAt          time.Time            `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time            `json:"updated_at" db:"updated_at"`
}

// Validate validates the virtual account
func (va *VirtualAccount) Validate() error {
	if va.ID == uuid.Nil {
		return fmt.Errorf("id is required")
	}

	if va.UserID == uuid.Nil {
		return fmt.Errorf("user_id is required")
	}

	if va.DueAccountID == "" {
		return fmt.Errorf("due_account_id is required")
	}

	if va.Status == "" {
		return fmt.Errorf("status is required")
	}

	return nil
}

// CanBeLinked checks if the virtual account can be linked to a brokerage account
func (va *VirtualAccount) CanBeLinked() bool {
	return va.Status == VirtualAccountStatusActive && va.BrokerageAccountID == ""
}

// IsActive checks if the virtual account is in an active state
func (va *VirtualAccount) IsActive() bool {
	return va.Status == VirtualAccountStatusActive
}
