package entities

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DueAccountStatus represents the status of a Due account
type DueAccountStatus string

const (
	DueAccountStatusPending    DueAccountStatus = "pending"
	DueAccountStatusActive     DueAccountStatus = "active"
	DueAccountStatusSuspended  DueAccountStatus = "suspended"
	DueAccountStatusClosed     DueAccountStatus = "closed"
)

// DueAccountType represents the type of Due account
type DueAccountType string

const (
	DueAccountTypeIndividual DueAccountType = "individual"
	DueAccountTypeBusiness   DueAccountType = "business"
)

// DueAccount represents a Due account for a user
type DueAccount struct {
	ID          uuid.UUID       `json:"id" db:"id"`
	UserID      uuid.UUID       `json:"user_id" db:"user_id"`
	DueID       string          `json:"due_id" db:"due_id"`             // Due's account ID
	Type        DueAccountType  `json:"type" db:"type"`
	Name        string          `json:"name" db:"name"`
	Email       string          `json:"email" db:"email"`
	Country     string          `json:"country" db:"country"`
	Category    string          `json:"category,omitempty" db:"category"`
	Status      DueAccountStatus `json:"status" db:"status"`
	KYCStatus   string          `json:"kyc_status" db:"kyc_status"`     // pending, passed, resubmission_required, failed
	TOSAccepted *time.Time      `json:"tos_accepted,omitempty" db:"tos_accepted"`
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at" db:"updated_at"`
}

// Validate validates the Due account
func (da *DueAccount) Validate() error {
	if da.ID == uuid.Nil {
		return fmt.Errorf("id is required")
	}

	if da.UserID == uuid.Nil {
		return fmt.Errorf("user_id is required")
	}

	if da.DueID == "" {
		return fmt.Errorf("due_id is required")
	}

	if da.Type == "" {
		return fmt.Errorf("type is required")
	}

	if da.Name == "" {
		return fmt.Errorf("name is required")
	}

	if da.Email == "" {
		return fmt.Errorf("email is required")
	}

	if da.Country == "" {
		return fmt.Errorf("country is required")
	}

	if da.Status == "" {
		return fmt.Errorf("status is required")
	}

	return nil
}

// IsActive checks if the Due account is active
func (da *DueAccount) IsActive() bool {
	return da.Status == DueAccountStatusActive
}

// CanCreateVirtualAccount checks if the Due account can create virtual accounts
func (da *DueAccount) CanCreateVirtualAccount() bool {
	return da.IsActive() && da.KYCStatus == "passed"
}

// CreateDueAccountRequest represents the request to create a Due account
type CreateDueAccountRequest struct {
	UserID   uuid.UUID      `json:"user_id" validate:"required"`
	Type     DueAccountType `json:"type" validate:"required,oneof=individual business"`
	Name     string         `json:"name" validate:"required"`
	Email    string         `json:"email" validate:"required,email"`
	Country  string         `json:"country" validate:"required,len=2"`
	Category string         `json:"category,omitempty"`
}

// Validate validates the create request
func (req *CreateDueAccountRequest) Validate() error {
	if req.UserID == uuid.Nil {
		return fmt.Errorf("user_id is required")
	}

	if req.Type == "" {
		return fmt.Errorf("type is required")
	}

	if req.Name == "" {
		return fmt.Errorf("name is required")
	}

	if req.Email == "" {
		return fmt.Errorf("email is required")
	}

	if req.Country == "" {
		return fmt.Errorf("country is required")
	}

	return nil
}

// DueLinkedWalletStatus represents the status of a linked wallet
type DueLinkedWalletStatus string

const (
	DueLinkedWalletStatusLinked     DueLinkedWalletStatus = "linked"
	DueLinkedWalletStatusMonitoring DueLinkedWalletStatus = "monitoring"
	DueLinkedWalletStatusSuspended  DueLinkedWalletStatus = "suspended"
	DueLinkedWalletStatusUnlinked   DueLinkedWalletStatus = "unlinked"
)

// DueLinkedWallet represents a Circle wallet linked to a Due account
type DueLinkedWallet struct {
	ID                  uuid.UUID             `json:"id" db:"id"`
	DueAccountID        string                `json:"due_account_id" db:"due_account_id"`         // References due_accounts.due_id
	UserID              uuid.UUID             `json:"user_id" db:"user_id"`
	ManagedWalletID     uuid.UUID             `json:"managed_wallet_id" db:"managed_wallet_id"`   // References managed_wallets.id
	DueWalletID         string                `json:"due_wallet_id" db:"due_wallet_id"`           // Due's wallet ID
	WalletAddress       string                `json:"wallet_address" db:"wallet_address"`
	FormattedAddress    string                `json:"formatted_address" db:"formatted_address"`   // e.g., "evm:0x123..."
	Blockchain          string                `json:"blockchain" db:"blockchain"`
	Status              DueLinkedWalletStatus `json:"status" db:"status"`
	LinkedAt            time.Time             `json:"linked_at" db:"linked_at"`
	LastMonitoredAt     *time.Time            `json:"last_monitored_at,omitempty" db:"last_monitored_at"`
	ComplianceCheckedAt *time.Time            `json:"compliance_checked_at,omitempty" db:"compliance_checked_at"`
	CreatedAt           time.Time             `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time             `json:"updated_at" db:"updated_at"`

	// Related entities (not stored in DB)
	DueAccount    *DueAccount    `json:"due_account,omitempty"`
	ManagedWallet *ManagedWallet `json:"managed_wallet,omitempty"`
}

// Validate validates the Due linked wallet
func (dlw *DueLinkedWallet) Validate() error {
	if dlw.ID == uuid.Nil {
		return fmt.Errorf("id is required")
	}

	if dlw.DueAccountID == "" {
		return fmt.Errorf("due_account_id is required")
	}

	if dlw.UserID == uuid.Nil {
		return fmt.Errorf("user_id is required")
	}

	if dlw.ManagedWalletID == uuid.Nil {
		return fmt.Errorf("managed_wallet_id is required")
	}

	if dlw.DueWalletID == "" {
		return fmt.Errorf("due_wallet_id is required")
	}

	if dlw.WalletAddress == "" {
		return fmt.Errorf("wallet_address is required")
	}

	if dlw.FormattedAddress == "" {
		return fmt.Errorf("formatted_address is required")
	}

	if dlw.Blockchain == "" {
		return fmt.Errorf("blockchain is required")
	}

	if dlw.Status == "" {
		return fmt.Errorf("status is required")
	}

	return nil
}

// IsActive checks if the linked wallet is active and can be used for transfers
func (dlw *DueLinkedWallet) IsActive() bool {
	return dlw.Status == DueLinkedWalletStatusLinked || dlw.Status == DueLinkedWalletStatusMonitoring
}

// CanTransfer checks if the linked wallet can initiate transfers
func (dlw *DueLinkedWallet) CanTransfer() bool {
	return dlw.Status == DueLinkedWalletStatusLinked
}

// LinkWalletToDueRequest represents the request to link a Circle wallet to Due account
type LinkWalletToDueRequest struct {
	UserID          uuid.UUID `json:"user_id" validate:"required"`
	ManagedWalletID uuid.UUID `json:"managed_wallet_id" validate:"required"`
}

// Validate validates the link request
func (req *LinkWalletToDueRequest) Validate() error {
	if req.UserID == uuid.Nil {
		return fmt.Errorf("user_id is required")
	}

	if req.ManagedWalletID == uuid.Nil {
		return fmt.Errorf("managed_wallet_id is required")
	}

	return nil
}

// LinkWalletToDueResponse represents the response after linking a wallet
type LinkWalletToDueResponse struct {
	LinkedWallet *DueLinkedWallet `json:"linked_wallet"`
	Message      string           `json:"message"`
}
