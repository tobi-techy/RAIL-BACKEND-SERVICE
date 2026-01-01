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
