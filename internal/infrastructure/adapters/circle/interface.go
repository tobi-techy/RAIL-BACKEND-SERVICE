package circle

import "context"

// Client defines the Circle Developer-Controlled Wallets API surface.
type Client interface {
	// Wallet Sets
	CreateWalletSet(ctx context.Context, name string) (*WalletSet, error)

	// Wallets
	CreateWallets(ctx context.Context, walletSetID string, blockchains []Blockchain, count int, metadata []WalletMetadata) ([]Wallet, error)
	GetWallet(ctx context.Context, walletID string) (*Wallet, error)
	ListWallets(ctx context.Context, walletSetID string) ([]Wallet, error)
	ListWalletsByRefID(ctx context.Context, refID string) ([]Wallet, error)
	GetTokenBalance(ctx context.Context, walletID string) ([]TokenBalance, error)
	GetUSDCTokenID(ctx context.Context, walletID string) (string, error)

	// Transfers
	CreateTransfer(ctx context.Context, req *CreateTransferRequest) (*Transaction, error)
	GetTransaction(ctx context.Context, txID string) (*Transaction, error)
	EstimateTransferFee(ctx context.Context, req *EstimateFeeRequest) (*FeeEstimate, error)

	// Config
	GetEntityPublicKey(ctx context.Context) (string, error)

	// Health
	Ping(ctx context.Context) error
}
