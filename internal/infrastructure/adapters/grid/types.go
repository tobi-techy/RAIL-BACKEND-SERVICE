package grid

import (
	"time"

	"github.com/shopspring/decimal"
)

// Account represents a Grid account (Solana wallet)
type Account struct {
	Address   string    `json:"address"`
	Email     string    `json:"email"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// AccountCreationResponse is returned when initiating account creation
type AccountCreationResponse struct {
	Email     string    `json:"email"`
	OTPSent   bool      `json:"otp_sent"`
	ExpiresAt time.Time `json:"expires_at"`
	Status    string    `json:"status"`
}

// SessionSecrets holds Ed25519 keypairs for transaction signing
type SessionSecrets struct {
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"` // Must be stored encrypted
}

// Balance represents a token balance
type Balance struct {
	Token   string          `json:"token"`
	Amount  decimal.Decimal `json:"amount"`
	Mint    string          `json:"mint,omitempty"`
	Decimals int            `json:"decimals,omitempty"`
}

// Balances represents account balances
type Balances struct {
	Address  string    `json:"address"`
	Balances []Balance `json:"balances"`
}

// CreateAccountRequest is the request to create an account
type CreateAccountRequest struct {
	Email string `json:"email"`
}

// VerifyOTPRequest is the request to verify OTP
type VerifyOTPRequest struct {
	Email          string          `json:"email"`
	Code           string          `json:"code"`
	SessionSecrets *SessionSecrets `json:"sessionSecrets,omitempty"`
}

// VerifyOTPResponse is the response from OTP verification
type VerifyOTPResponse struct {
	Address string `json:"address"`
	Email   string `json:"email"`
	Status  string `json:"status"`
}

// APIResponse wraps Grid API responses
type APIResponse struct {
	Data    interface{} `json:"data,omitempty"`
	Error   *APIError   `json:"error,omitempty"`
	Message string      `json:"message,omitempty"`
}

// APIError represents a Grid API error
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// KYC Types

// KYCLinkResponse is returned when requesting a KYC link
type KYCLinkResponse struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// KYCStatus represents the current KYC status
type KYCStatus struct {
	Status    string    `json:"status"` // pending, approved, rejected
	UpdatedAt time.Time `json:"updated_at"`
	Reasons   []string  `json:"reasons,omitempty"` // Rejection reasons if rejected
}

// Virtual Account Types

// VirtualAccount represents a fiat virtual account for on-ramp
type VirtualAccount struct {
	ID            string `json:"id"`
	AccountNumber string `json:"account_number"`
	RoutingNumber string `json:"routing_number"`
	BankName      string `json:"bank_name"`
	Currency      string `json:"currency"` // USD
	Status        string `json:"status"`
}

// VirtualAccountsResponse wraps the list of virtual accounts
type VirtualAccountsResponse struct {
	VirtualAccounts []VirtualAccount `json:"virtual_accounts"`
}

// Payment Intent Types (Off-ramp)

// PaymentIntentRequest is the request to create a payment intent
type PaymentIntentRequest struct {
	AccountAddress string          `json:"account_address"`
	Amount         decimal.Decimal `json:"amount"`
	Currency       string          `json:"currency"`
	Destination    PaymentDest     `json:"destination"`
}

// PaymentDest represents the destination for a payment
type PaymentDest struct {
	Type          string `json:"type"` // ach, wire, sepa
	AccountNumber string `json:"account_number"`
	RoutingNumber string `json:"routing_number,omitempty"`
	IBAN          string `json:"iban,omitempty"`
	BIC           string `json:"bic,omitempty"`
}

// PaymentIntent represents a payment intent for off-ramp
type PaymentIntent struct {
	ID        string          `json:"id"`
	Status    string          `json:"status"`
	Amount    decimal.Decimal `json:"amount"`
	Currency  string          `json:"currency"`
	CreatedAt time.Time       `json:"created_at"`
}
