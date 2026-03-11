package bridge

import "context"

// BridgeClient interface defines all Bridge API operations needed for RAIL
type BridgeClient interface {
	// Customer Management
	CreateCustomer(ctx context.Context, req *CreateCustomerRequest) (*Customer, error)
	GetCustomer(ctx context.Context, customerID string) (*Customer, error)
	GetCustomerByEmail(ctx context.Context, email string) (*Customer, error)
	UpdateCustomer(ctx context.Context, customerID string, req *UpdateCustomerRequest) (*Customer, error)
	ListCustomers(ctx context.Context, cursor string, limit int) (*ListCustomersResponse, error)

	// KYC
	GetKYCLink(ctx context.Context, customerID string) (*KYCLinkResponse, error)
	GetTOSLink(ctx context.Context, customerID string) (*TOSLinkResponse, error)

	// Virtual Accounts
	CreateVirtualAccount(ctx context.Context, customerID string, req *CreateVirtualAccountRequest) (*VirtualAccount, error)
	GetVirtualAccount(ctx context.Context, customerID, virtualAccountID string) (*VirtualAccount, error)
	ListVirtualAccounts(ctx context.Context, customerID string) (*ListVirtualAccountsResponse, error)
	DeactivateVirtualAccount(ctx context.Context, customerID, virtualAccountID string) (*VirtualAccount, error)

	// Wallets
	CreateWallet(ctx context.Context, customerID string, req *CreateWalletRequest) (*Wallet, error)
	GetWallet(ctx context.Context, customerID, walletID string) (*Wallet, error)
	ListWallets(ctx context.Context, customerID string) (*ListWalletsResponse, error)
	GetWalletBalance(ctx context.Context, customerID, walletID string) (*WalletBalance, error)

	// Cards
	EnableCards(ctx context.Context, req *EnableCardsRequest) error
	CreateCardAccount(ctx context.Context, customerID string, req *CreateCardAccountRequest) (*CardAccount, error)
	GetCardAccount(ctx context.Context, customerID, cardAccountID string) (*CardAccount, error)
	FreezeCardAccount(ctx context.Context, customerID, cardAccountID string) (*CardAccount, error)
	UnfreezeCardAccount(ctx context.Context, customerID, cardAccountID string) (*CardAccount, error)

	// External Accounts (ACH payout destinations)
	CreateExternalAccount(ctx context.Context, customerID string, req *CreateExternalAccountRequest) (*ExternalAccount, error)
	GetExternalAccount(ctx context.Context, customerID, externalAccountID string) (*ExternalAccount, error)

	// Transfers
	CreateTransfer(ctx context.Context, req *CreateTransferRequest) (*Transfer, error)
	GetTransfer(ctx context.Context, transferID string) (*Transfer, error)
	ListTransfers(ctx context.Context, customerID string) (*ListTransfersResponse, error)

	// Liquidation Addresses
	CreateLiquidationAddress(ctx context.Context, customerID string, req *CreateLiquidationAddressRequest) (*LiquidationAddress, error)
	ListLiquidationAddresses(ctx context.Context, customerID string) (*ListLiquidationAddressesResponse, error)
	GetDrains(ctx context.Context, customerID, liquidationAddressID string) (*ListDrainsResponse, error)

	// Health
	Ping(ctx context.Context) error
	Config() Config
}

// Ensure Client implements BridgeClient interface
var _ BridgeClient = (*Client)(nil)
