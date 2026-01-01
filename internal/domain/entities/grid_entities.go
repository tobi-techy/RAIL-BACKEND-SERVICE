package entities

import (
	"time"

	"github.com/google/uuid"
)

// GridAccountStatus represents the status of a Grid account
type GridAccountStatus string

const (
	GridAccountStatusPending  GridAccountStatus = "pending"
	GridAccountStatusActive   GridAccountStatus = "active"
	GridAccountStatusSuspended GridAccountStatus = "suspended"
)

// GridKYCStatus represents the KYC status for a Grid account
type GridKYCStatus string

const (
	GridKYCStatusNone     GridKYCStatus = "none"
	GridKYCStatusPending  GridKYCStatus = "pending"
	GridKYCStatusApproved GridKYCStatus = "approved"
	GridKYCStatusRejected GridKYCStatus = "rejected"
)

// GridAccount represents a Grid account linked to a user
type GridAccount struct {
	ID                     uuid.UUID         `json:"id" db:"id"`
	UserID                 uuid.UUID         `json:"user_id" db:"user_id"`
	Email                  string            `json:"email" db:"email"`
	Address                string            `json:"address" db:"address"` // Solana address
	Status                 GridAccountStatus `json:"status" db:"status"`
	KYCStatus              GridKYCStatus     `json:"kyc_status" db:"kyc_status"`
	EncryptedSessionSecret string            `json:"-" db:"encrypted_session_secret"` // AES-256-GCM encrypted
	CreatedAt              time.Time         `json:"created_at" db:"created_at"`
	UpdatedAt              time.Time         `json:"updated_at" db:"updated_at"`
}

// GridVirtualAccount represents a Grid virtual account for fiat on-ramp
type GridVirtualAccount struct {
	ID              uuid.UUID `json:"id" db:"id"`
	GridAccountID   uuid.UUID `json:"grid_account_id" db:"grid_account_id"`
	UserID          uuid.UUID `json:"user_id" db:"user_id"`
	ExternalID      string    `json:"external_id" db:"external_id"` // Grid's virtual account ID
	AccountNumber   string    `json:"account_number" db:"account_number"`
	RoutingNumber   string    `json:"routing_number" db:"routing_number"`
	BankName        string    `json:"bank_name" db:"bank_name"`
	Currency        string    `json:"currency" db:"currency"`
	Status          string    `json:"status" db:"status"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
}

// GridPaymentIntent represents a payment intent for off-ramp
type GridPaymentIntent struct {
	ID            uuid.UUID `json:"id" db:"id"`
	GridAccountID uuid.UUID `json:"grid_account_id" db:"grid_account_id"`
	UserID        uuid.UUID `json:"user_id" db:"user_id"`
	ExternalID    string    `json:"external_id" db:"external_id"` // Grid's payment intent ID
	Amount        string    `json:"amount" db:"amount"`           // Stored as string for precision
	Currency      string    `json:"currency" db:"currency"`
	Status        string    `json:"status" db:"status"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time `json:"updated_at" db:"updated_at"`
}

// GridKYCWebhook represents a KYC status update webhook payload
type GridKYCWebhook struct {
	Address   string   `json:"address"`
	Status    string   `json:"status"`
	Reasons   []string `json:"reasons,omitempty"`
	Timestamp string   `json:"timestamp"`
}
