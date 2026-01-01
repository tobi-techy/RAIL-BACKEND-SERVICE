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

	// KYC Operations
	RequestKYCLink(ctx context.Context, address string) (*KYCLinkResponse, error)
	GetKYCStatus(ctx context.Context, address string) (*KYCStatus, error)

	// Virtual Accounts (On-ramp)
	RequestVirtualAccount(ctx context.Context, address string) (*VirtualAccount, error)
	ListVirtualAccounts(ctx context.Context, address string) ([]VirtualAccount, error)

	// Payment Intents (Off-ramp)
	CreatePaymentIntent(ctx context.Context, req *PaymentIntentRequest) (*PaymentIntent, error)

	// Health
	Ping(ctx context.Context) error
}

// Ensure Client implements GridClient interface
var _ GridClient = (*Client)(nil)
