package grid

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/grid"
)

// Repository defines data access for Grid accounts
type Repository interface {
	// Account operations
	CreateAccount(ctx context.Context, account *entities.GridAccount) error
	GetAccountByUserID(ctx context.Context, userID uuid.UUID) (*entities.GridAccount, error)
	GetAccountByAddress(ctx context.Context, address string) (*entities.GridAccount, error)
	GetAccountByEmail(ctx context.Context, email string) (*entities.GridAccount, error)
	UpdateAccount(ctx context.Context, account *entities.GridAccount) error

	// Virtual account operations
	CreateVirtualAccount(ctx context.Context, va *entities.GridVirtualAccount) error
	GetVirtualAccountsByUserID(ctx context.Context, userID uuid.UUID) ([]entities.GridVirtualAccount, error)
	GetVirtualAccountByExternalID(ctx context.Context, externalID string) (*entities.GridVirtualAccount, error)

	// Payment intent operations
	CreatePaymentIntent(ctx context.Context, pi *entities.GridPaymentIntent) error
	GetPaymentIntentByExternalID(ctx context.Context, externalID string) (*entities.GridPaymentIntent, error)
	UpdatePaymentIntent(ctx context.Context, pi *entities.GridPaymentIntent) error
}

// GridClient defines the Grid API client interface
type GridClient interface {
	CreateAccount(ctx context.Context, email string) (*grid.AccountCreationResponse, error)
	VerifyOTP(ctx context.Context, email, otp string, sessionSecrets *grid.SessionSecrets) (*grid.Account, error)
	GetAccount(ctx context.Context, address string) (*grid.Account, error)
	GenerateSessionSecrets() (*grid.SessionSecrets, error)
	RequestKYCLink(ctx context.Context, address string) (*grid.KYCLinkResponse, error)
	GetKYCStatus(ctx context.Context, address string) (*grid.KYCStatus, error)
	RequestVirtualAccount(ctx context.Context, address string) (*grid.VirtualAccount, error)
	ListVirtualAccounts(ctx context.Context, address string) ([]grid.VirtualAccount, error)
	CreatePaymentIntent(ctx context.Context, req *grid.PaymentIntentRequest) (*grid.PaymentIntent, error)
}
