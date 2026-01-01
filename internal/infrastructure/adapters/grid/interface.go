package grid

import "context"

// GridClient interface defines all Grid API operations needed for RAIL
type GridClient interface {
	// Account Management
	CreateAccount(ctx context.Context, email string) (*AccountCreationResponse, error)
	VerifyOTP(ctx context.Context, email, otp string, sessionSecrets *SessionSecrets) (*Account, error)
	GetAccount(ctx context.Context, address string) (*Account, error)
	GetAccountBalances(ctx context.Context, address string) (*Balances, error)

	// Session Management
	GenerateSessionSecrets() (*SessionSecrets, error)

	// Health
	Ping(ctx context.Context) error
}

// Ensure Client implements GridClient interface
var _ GridClient = (*Client)(nil)
