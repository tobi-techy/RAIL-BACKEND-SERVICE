package circle

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// Adapter wraps the Circle Client and provides domain-level operations.
type Adapter struct {
	client  Client
	logger  *zap.Logger
	sandbox bool
}

// NewAdapter creates a new Circle adapter.
func NewAdapter(client Client, logger *zap.Logger) *Adapter {
	return &Adapter{client: client, logger: logger}
}

// NewSandboxAdapter creates a Circle adapter that uses testnet chains.
func NewSandboxAdapter(client Client, logger *zap.Logger) *Adapter {
	return &Adapter{client: client, logger: logger, sandbox: true}
}

// --- Wallet Operations ---

// CreateWalletForUser creates a Circle wallet on the given chain and returns a domain ManagedWallet.
func (a *Adapter) CreateWalletForUser(ctx context.Context, userID uuid.UUID, walletSetID string, chain entities.WalletChain) (*entities.ManagedWallet, error) {
	bc := domainChainToCircleForEnv(chain, a.sandbox)
	if bc == "" {
		return nil, fmt.Errorf("unsupported chain for Circle: %s", chain)
	}

	wallets, err := a.client.CreateWallets(ctx, walletSetID, []Blockchain{bc}, 1, []WalletMetadata{
		{Name: string(chain), RefID: userID.String()},
	})
	if err != nil {
		return nil, fmt.Errorf("circle create wallet: %w", err)
	}
	if len(wallets) == 0 {
		return nil, fmt.Errorf("circle returned no wallets")
	}

	return walletToDomain(wallets[0], userID), nil
}

// CreateMultiChainWallets creates wallets on all provided chains in a single API call.
func (a *Adapter) CreateMultiChainWallets(ctx context.Context, userID uuid.UUID, walletSetID string, chains []entities.WalletChain) ([]*entities.ManagedWallet, error) {
	var blockchains []Blockchain
	for _, ch := range chains {
		bc := domainChainToCircleForEnv(ch, a.sandbox)
		if bc != "" {
			blockchains = append(blockchains, bc)
		}
	}
	if len(blockchains) == 0 {
		return nil, fmt.Errorf("no supported chains provided")
	}

	wallets, err := a.client.CreateWallets(ctx, walletSetID, blockchains, 1, []WalletMetadata{
		{RefID: userID.String()},
	})
	if err != nil {
		return nil, fmt.Errorf("circle create wallets: %w", err)
	}

	var result []*entities.ManagedWallet
	for _, w := range wallets {
		result = append(result, walletToDomain(w, userID))
	}
	return result, nil
}

// GetWalletBalance returns the USDC balance for a Circle wallet.
func (a *Adapter) GetWalletBalance(ctx context.Context, walletID string) (string, error) {
	balances, err := a.client.GetTokenBalance(ctx, walletID)
	if err != nil {
		return "0", fmt.Errorf("circle get balance: %w", err)
	}

	for _, b := range balances {
		if strings.EqualFold(b.Token.Symbol, "USDC") {
			return b.Amount, nil
		}
	}
	return "0", nil
}

// ListWallets returns all wallets in a wallet set as domain entities.
func (a *Adapter) ListWallets(ctx context.Context, walletSetID string, userID uuid.UUID) ([]*entities.ManagedWallet, error) {
	wallets, err := a.client.ListWallets(ctx, walletSetID)
	if err != nil {
		return nil, fmt.Errorf("circle list wallets: %w", err)
	}

	var result []*entities.ManagedWallet
	for _, w := range wallets {
		result = append(result, walletToDomain(w, userID))
	}
	return result, nil
}

// TransferUSDC initiates a USDC transfer from a Circle wallet using walletId + tokenId (REST API style).
func (a *Adapter) TransferUSDC(ctx context.Context, walletID, tokenAddress, destinationAddress, amount string) (*Transaction, error) {
	// Circle REST API requires tokenId (UUID), not tokenAddress.
	// Look up the tokenId from the wallet's balances.
	tokenID, err := a.client.GetUSDCTokenID(ctx, walletID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve USDC tokenId for wallet %s: %w", walletID, err)
	}

	req := &CreateTransferRequest{
		WalletID:           walletID,
		TokenID:            tokenID,
		DestinationAddress: destinationAddress,
		Amounts:            []string{amount},
		Fee:                FeeConfig{Type: "level", Config: FeeConfigLevel{FeeLevel: "MEDIUM"}},
	}
	return a.client.CreateTransfer(ctx, req)
}

// HealthCheck verifies Circle API connectivity.
func (a *Adapter) HealthCheck(ctx context.Context) error {
	return a.client.Ping(ctx)
}

// --- Domain Mapping ---

// walletToDomain converts a Circle Wallet to a domain ManagedWallet.
func walletToDomain(w Wallet, userID uuid.UUID) *entities.ManagedWallet {
	return &entities.ManagedWallet{
		ID:             uuid.New(),
		UserID:         userID,
		Chain:          circleChainToDomain(w.Blockchain),
		Address:        w.Address,
		CircleWalletID: w.ID,
		AccountType:    entities.AccountTypeEOA,
		Status:         circleStateToDomainStatus(w.State),
		CreatedAt:      w.CreateDate,
		UpdatedAt:      w.UpdateDate,
	}
}

// domainChainToCircle maps domain WalletChain → Circle Blockchain identifier.
func domainChainToCircle(chain entities.WalletChain) Blockchain {
	return domainChainToCircleForEnv(chain, false)
}

// domainChainToCircleForEnv maps domain WalletChain → Circle Blockchain, using testnet chains when sandbox=true.
func domainChainToCircleForEnv(chain entities.WalletChain, sandbox bool) Blockchain {
	if sandbox {
		switch chain {
		case entities.WalletChainSolana:
			return BlockchainSOLDevnet
		case entities.WalletChainEthereum:
			return BlockchainETHSepolia
		case entities.WalletChainPolygon:
			return BlockchainMATICAmoy
		case entities.WalletChainBase:
			return BlockchainBASESepolia
		case entities.WalletChainAvalanche:
			return BlockchainAVAXFuji
		case entities.WalletChainArbitrum:
			return BlockchainARBSepolia
		case entities.WalletChainOptimism:
			return BlockchainOPSepolia
		default:
			return ""
		}
	}
	switch chain {
	case entities.WalletChainSolana:
		return BlockchainSOL
	case entities.WalletChainEthereum:
		return BlockchainETH
	case entities.WalletChainPolygon:
		return BlockchainMATIC
	case entities.WalletChainBase:
		return BlockchainBASE
	case entities.WalletChainAvalanche:
		return BlockchainAVAX
	case entities.WalletChainArbitrum:
		return BlockchainARB
	case entities.WalletChainOptimism:
		return BlockchainOP
	default:
		return ""
	}
}

// circleChainToDomain maps Circle Blockchain → domain WalletChain.
func circleChainToDomain(bc Blockchain) entities.WalletChain {
	switch bc {
	case BlockchainSOL, BlockchainSOLDevnet:
		return entities.WalletChainSolana
	case BlockchainETH, BlockchainETHSepolia:
		return entities.WalletChainEthereum
	case BlockchainMATIC, BlockchainMATICAmoy:
		return entities.WalletChainPolygon
	case BlockchainBASE, BlockchainBASESepolia:
		return entities.WalletChainBase
	case BlockchainAVAX, BlockchainAVAXFuji:
		return entities.WalletChainAvalanche
	case BlockchainARB, BlockchainARBSepolia:
		return entities.WalletChainArbitrum
	case BlockchainOP, BlockchainOPSepolia:
		return entities.WalletChainOptimism
	default:
		return entities.WalletChain(string(bc))
	}
}

// circleStateToDomainStatus maps Circle wallet state → domain WalletStatus.
func circleStateToDomainStatus(state WalletState) entities.WalletStatus {
	switch state {
	case WalletStateLive:
		return entities.WalletStatusLive
	case WalletStateFrozen:
		return entities.WalletStatusFailed
	default:
		return entities.WalletStatusCreating
	}
}
