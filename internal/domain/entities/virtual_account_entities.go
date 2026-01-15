package entities

import (
	"time"

	"github.com/google/uuid"
)

// VirtualAccountStatus represents the status of a virtual account
type VirtualAccountStatus string

const (
	VirtualAccountStatusPending VirtualAccountStatus = "pending"
	VirtualAccountStatusActive  VirtualAccountStatus = "active"
	VirtualAccountStatusClosed  VirtualAccountStatus = "closed"
	VirtualAccountStatusFailed  VirtualAccountStatus = "failed"
)

// VirtualAccount represents a virtual account linked to an Alpaca brokerage account
type VirtualAccount struct {
	ID              uuid.UUID            `json:"id" db:"id"`
	UserID          uuid.UUID            `json:"user_id" db:"user_id"`
	BridgeCustomerID string              `json:"bridge_customer_id" db:"bridge_customer_id"`
	AlpacaAccountID string               `json:"alpaca_account_id" db:"alpaca_account_id"`
	BridgeAccountID *string              `json:"bridge_account_id,omitempty" db:"bridge_account_id"`
	AccountNumber   string               `json:"account_number" db:"account_number"`
	RoutingNumber   string               `json:"routing_number" db:"routing_number"`
	Status          VirtualAccountStatus `json:"status" db:"status"`
	Currency        string               `json:"currency" db:"currency"`
	CreatedAt       time.Time            `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at" db:"updated_at"`
}

// CreateVirtualAccountRequest represents a request to create a virtual account
type CreateVirtualAccountRequest struct {
	UserID          uuid.UUID `json:"user_id"`
	AlpacaAccountID string    `json:"alpaca_account_id"`
}

// CreateVirtualAccountResponse represents the response from creating a virtual account
type CreateVirtualAccountResponse struct {
	VirtualAccount *VirtualAccount `json:"virtual_account"`
	Message        string          `json:"message"`
}

// CreateAccountRequest represents a request to create a Bridge account
type CreateAccountRequest struct {
	Email     string `json:"email" validate:"required,email"`
	FirstName string `json:"firstName" validate:"required"`
	LastName  string `json:"lastName" validate:"required"`
	Type      string `json:"type" validate:"required"`
	Country   string `json:"country" validate:"required,len=2"`
}

// CreateAccountResponse represents the response from creating a Bridge account
type CreateAccountResponse struct {
	AccountID string `json:"accountId"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}