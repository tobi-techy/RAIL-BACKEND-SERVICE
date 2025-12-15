package due

import (
	"context"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// DueClient interface defines all Due API operations needed for RAIL
type DueClient interface {
	// Account Management
	CreateAccount(ctx context.Context, req *entities.CreateAccountRequest) (*entities.CreateAccountResponse, error)
	GetAccount(ctx context.Context, accountID string) (*entities.CreateAccountResponse, error)
	GetAccountCategories(ctx context.Context) (*AccountCategoriesResponse, error)

	// Wallet Management
	LinkWallet(ctx context.Context, req *LinkWalletRequest) (*LinkWalletResponse, error)
	ListWallets(ctx context.Context) (*ListWalletsResponse, error)
	GetWallet(ctx context.Context, walletID string) (*LinkWalletResponse, error)
	GetWalletBalance(ctx context.Context, walletID string) (*WalletBalanceResponse, error)

	// Virtual Accounts
	CreateVirtualAccount(ctx context.Context, req *CreateVirtualAccountRequest) (*CreateVirtualAccountResponse, error)
	GetVirtualAccount(ctx context.Context, reference string) (*CreateVirtualAccountResponse, error)
	ListVirtualAccounts(ctx context.Context, filters *VirtualAccountFilters) (*ListVirtualAccountsResponse, error)
	UpdateVirtualAccount(ctx context.Context, key string, req *UpdateVirtualAccountRequest) (*CreateVirtualAccountResponse, error)

	// Recipients
	CreateRecipient(ctx context.Context, req *CreateRecipientRequest) (*CreateRecipientResponse, error)
	ListRecipients(ctx context.Context, limit, offset int) (*ListRecipientsResponse, error)
	GetRecipient(ctx context.Context, recipientID string) (*CreateRecipientResponse, error)

	// Transfers
	CreateTransfer(ctx context.Context, req *CreateTransferRequest) (*CreateTransferResponse, error)
	GetTransfer(ctx context.Context, transferID string) (*CreateTransferResponse, error)
	ListTransfers(ctx context.Context, filters *TransferFilters) (*ListTransfersResponse, error)
	CreateQuote(ctx context.Context, req *CreateQuoteRequest) (*QuoteResponse, error)
	CreateFundingAddress(ctx context.Context, transferID string, req *FundingAddressRequest) (*FundingAddressResponse, error)

	// KYC
	GetKYCStatus(ctx context.Context, accountID string) (*KYCStatusResponse, error)
	InitiateKYC(ctx context.Context, accountID string) (*KYCInitiateResponse, error)
	GetKYCLink(ctx context.Context, accountID string) (*KYCLinkResponse, error)
	CreateKYCSession(ctx context.Context, accountID string, req *KYCSessionRequest) (*KYCSessionResponse, error)

	// Financial Institutions
	ListFinancialInstitutions(ctx context.Context, country, schema string) (*FinancialInstitutionsResponse, error)
	GetFinancialInstitution(ctx context.Context, institutionID string) (*FinancialInstitution, error)

	// Webhooks
	CreateWebhookEndpoint(ctx context.Context, req *CreateWebhookRequest) (*WebhookEndpointResponse, error)
	ListWebhookEndpoints(ctx context.Context) (*ListWebhookEndpointsResponse, error)
	DeleteWebhookEndpoint(ctx context.Context, webhookID string) error
	ListWebhookEvents(ctx context.Context, filters *WebhookEventFilters) (*ListWebhookEventsResponse, error)

	// FX
	GetFXMarkets(ctx context.Context) (*FXMarketsResponse, error)
	CreateFXQuote(ctx context.Context, req *FXQuoteRequest) (*FXQuoteResponse, error)

	// Terms of Service
	GetTOSData(ctx context.Context, token string) (*TOSDataResponse, error)
	AcceptTermsOfService(ctx context.Context, accountID, tosToken string) (*TOSAcceptResponse, error)

	// Channels
	GetChannels(ctx context.Context) (*ChannelsResponse, error)

	// Sandbox (for testing)
	SimulatePayIn(ctx context.Context, req *SimulatePayInRequest) (*SimulatePayInResponse, error)

	// On-ramp specific
	CreateOnRampQuote(ctx context.Context, req *OnRampQuoteRequest) (*OnRampQuoteResponse, error)
	CreateOnRampTransfer(ctx context.Context, req *OnRampTransferRequest) (*OnRampTransferResponse, error)
}

// Ensure Client implements DueClient interface
var _ DueClient = (*Client)(nil)
