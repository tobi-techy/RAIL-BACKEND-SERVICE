package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// VirtualAccountStatus represents the status of a virtual account
type VirtualAccountStatus string

const (
	VirtualAccountStatusPending VirtualAccountStatus = "pending"
	VirtualAccountStatusActive  VirtualAccountStatus = "active"
	VirtualAccountStatusClosed  VirtualAccountStatus = "closed"
	VirtualAccountStatusFailed  VirtualAccountStatus = "failed"
)

// VirtualAccountProvider identifies which external provider issued the account.
type VirtualAccountProvider string

const (
	VirtualAccountProviderBridge VirtualAccountProvider = "bridge"
	VirtualAccountProviderGraph  VirtualAccountProvider = "graph"
)

// VirtualAccount represents a virtual account linked to an Alpaca brokerage account
type VirtualAccount struct {
	ID               uuid.UUID              `json:"id" db:"id"`
	UserID           uuid.UUID              `json:"user_id" db:"user_id"`
	Provider         VirtualAccountProvider `json:"provider" db:"provider"`
	BridgeCustomerID string                 `json:"bridge_customer_id" db:"bridge_customer_id"`
	AlpacaAccountID  string                 `json:"alpaca_account_id" db:"alpaca_account_id"`
	BridgeAccountID  *string                `json:"bridge_account_id,omitempty" db:"bridge_account_id"`
	GraphPersonID    *string                `json:"graph_person_id,omitempty" db:"graph_person_id"`
	GraphAccountID   *string                `json:"graph_account_id,omitempty" db:"graph_account_id"`
	AccountNumber    string                 `json:"account_number" db:"account_number"`
	RoutingNumber    string                 `json:"routing_number" db:"routing_number"`
	BankCode         string                 `json:"bank_code" db:"bank_code"`
	BankName         string                 `json:"bank_name" db:"bank_name"`
	BeneficiaryName  string                 `json:"beneficiary_name" db:"beneficiary_name"`
	BankAddress      string                 `json:"bank_address" db:"bank_address"`
	BeneficiaryAddr  string                 `json:"beneficiary_address" db:"beneficiary_address"`
	PaymentRails     pq.StringArray         `json:"payment_rails" db:"payment_rails"`
	Status           VirtualAccountStatus   `json:"status" db:"status"`
	Currency         string                 `json:"currency" db:"currency"`
	CreatedAt        time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at" db:"updated_at"`
}

// CreateVirtualAccountRequest represents a request to create a virtual account
type CreateVirtualAccountRequest struct {
	UserID           uuid.UUID `json:"user_id"`
	AlpacaAccountID  string    `json:"alpaca_account_id"`
	BridgeCustomerID string    `json:"bridge_customer_id"`
	Currency         string    `json:"currency"` // USD, EUR, GBP — defaults to USD
}

// CreateVirtualAccountResponse represents the response from creating a virtual account
type CreateVirtualAccountResponse struct {
	VirtualAccount *VirtualAccount `json:"virtual_account"`
	Message        string          `json:"message"`
}

// CreateAccountRequest represents a request to create a Bridge account
type CreateAccountRequest struct {
	Email             string     `json:"email" validate:"required,email"`
	FirstName         string     `json:"firstName" validate:"required"`
	LastName          string     `json:"lastName" validate:"required"`
	Type              string     `json:"type" validate:"required"`
	Country           string     `json:"country" validate:"required,len=2"`
	Address           *Address   `json:"address,omitempty"`
	DateOfBirth       *time.Time `json:"dateOfBirth,omitempty"`
	Phone             *string    `json:"phone,omitempty"`
	SSN               string     `json:"ssn,omitempty"`
	SignedAgreementID string     `json:"signedAgreementId,omitempty"`
}

// CreateAccountResponse represents the response from creating a Bridge account
type CreateAccountResponse struct {
	AccountID string `json:"accountId"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}