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
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Adapter{client: client, logger: logger}
}

// NewSandboxAdapter creates a Circle adapter that uses testnet chains.
func NewSandboxAdapter(client Client, logger *zap.Logger) *Adapter {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Adapter{client: client, logger: logger, sandbox: true}
}

// --- Wallet Operations ---

// GetWallet returns a Circle wallet by ID.
func (a *Adapter) GetWallet(ctx context.Context, walletID string) (*Wallet, error) {
	wallet, err := a.client.GetWallet(ctx, walletID)
	if err != nil {
		return nil, fmt.Errorf("circle get wallet: %w", err)
	}
	return wallet, nil
}

// CreateWalletForUser creates a Circle wallet on the given chain and returns a domain ManagedWallet.
func (a *Adapter) CreateWalletForUser(ctx context.Context, userID uuid.UUID, walletSetID string, chain entities.WalletChain) (*entities.ManagedWallet, error) {
	bc := domainChainToCircleForEnv(chain, a.sandbox)
	if bc == "" {
		return nil, fmt.Errorf("unsupported chain for Circle: %s", chain)
	}

	existing, err := a.findExistingWallets(ctx, userID, walletSetID, []entities.WalletChain{chain})
	if err == nil && len(existing) > 0 {
		return existing[0], nil
	}
	if err != nil {
		a.logger.Warn("Failed to check existing Circle wallet before create",
			zap.Error(err),
			zap.String("userID", userID.String()),
			zap.String("chain", string(chain)))
	}

	accountType := circleAccountTypeForChain(chain)
	wallets, err := a.client.CreateWalletsWithType(ctx, walletSetID, []Blockchain{bc}, 1, accountType, []WalletMetadata{
		{Name: string(chain), RefID: userID.String()},
	})
	if err != nil {
		return nil, fmt.Errorf("circle create wallet: %w", err)
	}
	if len(wallets) == 0 {
		return nil, fmt.Errorf("circle returned no wallets")
	}

	return walletToDomainWithAccountType(wallets[0], userID, entities.WalletAccountType(accountType)), nil
}

// CreateMultiChainWallets creates wallets on all provided chains in a single API call.
func (a *Adapter) CreateMultiChainWallets(ctx context.Context, userID uuid.UUID, walletSetID string, chains []entities.WalletChain) ([]*entities.ManagedWallet, error) {
	if len(chains) == 0 {
		return nil, fmt.Errorf("no chains provided")
	}
	for _, ch := range chains {
		if domainChainToCircleForEnv(ch, a.sandbox) == "" {
			return nil, fmt.Errorf("unsupported chain for Circle: %s", ch)
		}
	}

	existing, err := a.findExistingWallets(ctx, userID, walletSetID, chains)
	if err != nil {
		a.logger.Warn("Failed to check existing Circle wallets before create",
			zap.Error(err),
			zap.String("userID", userID.String()))
	}
	existingByChain := make(map[entities.WalletChain]*entities.ManagedWallet, len(existing))
	for _, w := range existing {
		existingByChain[w.Chain] = w
	}

	// Split chains: Solana requires EOA, EVM chains support SCA
	var solChains, evmChains []Blockchain
	for _, ch := range chains {
		if _, ok := existingByChain[ch]; ok {
			continue
		}
		bc := domainChainToCircleForEnv(ch, a.sandbox)
		if ch == entities.WalletChainSolana {
			solChains = append(solChains, bc)
		} else {
			evmChains = append(evmChains, bc)
		}
	}
	if len(solChains) == 0 && len(evmChains) == 0 {
		if len(existing) > 0 {
			return existing, nil
		}
		return nil, fmt.Errorf("no supported chains provided")
	}

	metadata := []WalletMetadata{{RefID: userID.String()}}
	result := append([]*entities.ManagedWallet(nil), existing...)

	// Create Solana wallets as EOA (SCA not supported on SOL)
	if len(solChains) > 0 {
		wallets, err := a.client.CreateWalletsWithType(ctx, walletSetID, solChains, 1, "EOA", metadata)
		if err != nil {
			return nil, fmt.Errorf("circle create wallets: %w", err)
		}
		for _, w := range wallets {
			result = append(result, walletToDomainWithAccountType(w, userID, entities.AccountTypeEOA))
		}
	}

	// Create EVM wallets as SCA
	if len(evmChains) > 0 {
		wallets, err := a.client.CreateWalletsWithType(ctx, walletSetID, evmChains, 1, "SCA", metadata)
		if err != nil {
			return nil, fmt.Errorf("circle create wallets: %w", err)
		}
		for _, w := range wallets {
			result = append(result, walletToDomainWithAccountType(w, userID, entities.AccountTypeSCA))
		}
	}

	return result, nil
}

func (a *Adapter) findExistingWallets(ctx context.Context, userID uuid.UUID, walletSetID string, chains []entities.WalletChain) ([]*entities.ManagedWallet, error) {
	wallets, err := a.ListWalletsForUser(ctx, userID, walletSetID)
	if err != nil {
		return nil, err
	}

	needed := make(map[entities.WalletChain]struct{}, len(chains))
	for _, chain := range chains {
		needed[chain] = struct{}{}
	}

	var result []*entities.ManagedWallet
	for _, w := range preferredWalletsByChain(wallets) {
		if _, ok := needed[w.Chain]; ok {
			result = append(result, w)
		}
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

// GetNativeBalance returns the native gas token balance (e.g. SOL) for a Circle wallet.
func (a *Adapter) GetNativeBalance(ctx context.Context, walletID string) (string, error) {
	return a.client.GetNativeTokenBalance(ctx, walletID)
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

// ListCircleWallets returns raw Circle wallets in a wallet set.
func (a *Adapter) ListCircleWallets(ctx context.Context, walletSetID string) ([]Wallet, error) {
	return a.client.ListWallets(ctx, walletSetID)
}

// ListCircleWalletsByRefID returns raw Circle wallets matching a user refId.
func (a *Adapter) ListCircleWalletsByRefID(ctx context.Context, refID string) ([]Wallet, error) {
	return a.client.ListWalletsByRefID(ctx, refID)
}

// GetTokenBalance returns raw Circle token balances for a wallet.
func (a *Adapter) GetTokenBalance(ctx context.Context, walletID string) ([]TokenBalance, error) {
	return a.client.GetTokenBalance(ctx, walletID)
}

// GetTokenBalanceOnchain returns on-chain (includeAll) token balances — the reliable
// source for funds delivered by an external bridge/contract that Circle's indexer misses.
func (a *Adapter) GetTokenBalanceOnchain(ctx context.Context, walletID string) ([]TokenBalance, error) {
	return a.client.GetTokenBalanceOnchain(ctx, walletID)
}

// GetUSDCTokenIDOnchain resolves the USDC token ID from the on-chain (includeAll) balance.
func (a *Adapter) GetUSDCTokenIDOnchain(ctx context.Context, walletID string) (string, error) {
	return a.client.GetUSDCTokenIDOnchain(ctx, walletID)
}

func (a *Adapter) ListWalletsForUser(ctx context.Context, userID uuid.UUID, walletSetID string) ([]*entities.ManagedWallet, error) {
	wallets, err := a.client.ListWalletsByRefID(ctx, userID.String())
	if err != nil {
		return nil, fmt.Errorf("circle list wallets by refId: %w", err)
	}

	result := make([]*entities.ManagedWallet, 0, len(wallets))
	for _, w := range wallets {
		if walletSetID != "" && w.WalletSetID != walletSetID {
			continue
		}
		result = append(result, walletToDomain(w, userID))
	}
	return result, nil
}

// TransferUSDC initiates a USDC transfer from a Circle wallet using walletId + tokenId (REST API style).
func (a *Adapter) TransferUSDC(ctx context.Context, walletID, tokenID, destinationAddress, amount string) (*Transaction, error) {
	return a.TransferUSDCWithIdempotency(ctx, walletID, tokenID, destinationAddress, amount, "")
}

// TransferUSDCWithIdempotency initiates an idempotent USDC transfer from a Circle wallet.
func (a *Adapter) TransferUSDCWithIdempotency(ctx context.Context, walletID, tokenID, destinationAddress, amount, idempotencyKey string) (*Transaction, error) {
	req := &CreateTransferRequest{
		IdempotencyKey:     idempotencyKey,
		WalletID:           walletID,
		TokenID:            tokenID,
		DestinationAddress: destinationAddress,
		Amounts:            []string{amount},
		FeeLevel:           "MEDIUM",
	}
	return a.client.CreateTransfer(ctx, req)
}

// GetTokenSymbol returns the symbol for a token currently visible in a Circle wallet balance.
func (a *Adapter) GetTokenSymbol(ctx context.Context, walletID, tokenID string) (string, error) {
	balances, err := a.client.GetTokenBalance(ctx, walletID)
	if err != nil {
		return "", fmt.Errorf("circle get token balances: %w", err)
	}
	for _, b := range balances {
		if strings.EqualFold(b.Token.ID, tokenID) {
			return b.Token.Symbol, nil
		}
	}
	return "", fmt.Errorf("token %s not found in wallet %s balances", tokenID, walletID)
}

// ReturnUnsupportedToken sends an inbound unsupported token back to its source address.
func (a *Adapter) ReturnUnsupportedToken(ctx context.Context, walletID, tokenID, destinationAddress string, amounts []string, idempotencyKey string) error {
	if len(amounts) == 0 {
		return fmt.Errorf("amounts are required")
	}
	_, err := a.client.CreateTransfer(ctx, &CreateTransferRequest{
		IdempotencyKey:     idempotencyKey,
		WalletID:           walletID,
		TokenID:            tokenID,
		DestinationAddress: destinationAddress,
		Amounts:            amounts,
		FeeLevel:           "MEDIUM",
	})
	if err != nil {
		return fmt.Errorf("circle return unsupported token: %w", err)
	}
	return nil
}

// GetUSDCTokenID returns Circle's token ID for USDC in the given wallet.
func (a *Adapter) GetUSDCTokenID(ctx context.Context, walletID string) (string, error) {
	return a.client.GetUSDCTokenID(ctx, walletID)
}

// GetTransaction returns a Circle transaction by ID.
func (a *Adapter) GetTransaction(ctx context.Context, txID string) (*Transaction, error) {
	return a.client.GetTransaction(ctx, txID)
}

// SignTransaction signs a raw transaction with a Circle developer-controlled wallet.
func (a *Adapter) SignTransaction(ctx context.Context, walletID, rawTransaction, memo string) (*SignedTransaction, error) {
	return a.client.SignTransaction(ctx, &SignTransactionRequest{
		WalletID:       walletID,
		RawTransaction: rawTransaction,
		Memo:           memo,
	})
}

// ExecuteContract submits an EVM contract call from a Circle wallet. Use for ERC20 approve,
// DeFi protocol deposits/withdrawals, etc. Provide CallData (hex) OR AbiFunctionSignature + AbiParameters.
func (a *Adapter) ExecuteContract(ctx context.Context, req *CreateContractExecutionRequest) (*Transaction, error) {
	return a.client.CreateContractExecution(ctx, req)
}

// ListTransactions returns transactions for a wallet, optionally filtered by operation and state.
func (a *Adapter) ListTransactions(ctx context.Context, walletID string, operation string, state string) ([]Transaction, error) {
	return a.client.ListTransactions(ctx, walletID, operation, state)
}

// FindWalletWithUSDC searches all wallets for a user (by refId) and returns the first
// wallet+tokenId that holds USDC. Prefers Solana (primary custody chain), then EVM fallback.
func (a *Adapter) FindWalletWithUSDC(ctx context.Context, userRefID string) (string, string, string, string, error) {
	wallets, err := a.client.ListWalletsByRefID(ctx, userRefID)
	if err != nil {
		return "", "", "", "", fmt.Errorf("list wallets: %w", err)
	}

	// First pass: prefer Solana (primary custody chain)
	for _, w := range wallets {
		if !isSolanaBlockchain(w.Blockchain) {
			continue
		}
		tokenID, err := a.client.GetUSDCTokenID(ctx, w.ID)
		if err == nil && tokenID != "" {
			return w.ID, tokenID, string(w.Blockchain), w.Address, nil
		}
	}

	// Second pass: fall back to EVM wallets
	for _, w := range wallets {
		if isSolanaBlockchain(w.Blockchain) {
			continue
		}
		tokenID, err := a.client.GetUSDCTokenID(ctx, w.ID)
		if err == nil && tokenID != "" {
			return w.ID, tokenID, string(w.Blockchain), w.Address, nil
		}
	}

	return "", "", "", "", fmt.Errorf("no wallet with USDC found for user %s", userRefID)
}

func isSolanaBlockchain(bc Blockchain) bool {
	return bc == BlockchainSOL || bc == BlockchainSOLDevnet
}

func circleAccountTypeForChain(chain entities.WalletChain) string {
	if chain == entities.WalletChainSolana {
		return string(entities.AccountTypeEOA)
	}
	return string(entities.AccountTypeSCA)
}

// HealthCheck verifies Circle API connectivity.
func (a *Adapter) HealthCheck(ctx context.Context) error {
	return a.client.Ping(ctx)
}

// --- Domain Mapping ---

// walletToDomain converts a Circle Wallet to a domain ManagedWallet.
func walletToDomain(w Wallet, userID uuid.UUID) *entities.ManagedWallet {
	return walletToDomainWithAccountType(w, userID, "")
}

func walletToDomainWithAccountType(w Wallet, userID uuid.UUID, fallback entities.WalletAccountType) *entities.ManagedWallet {
	accountType := entities.WalletAccountType(w.AccountType)
	if !accountType.IsValid() {
		accountType = fallback
	}
	if !accountType.IsValid() {
		accountType = entities.AccountTypeEOA
	}
	var walletSetID uuid.UUID
	if parsed, err := uuid.Parse(w.WalletSetID); err == nil {
		walletSetID = parsed
	}

	return &entities.ManagedWallet{
		ID:             uuid.New(),
		UserID:         userID,
		WalletSetID:    walletSetID,
		Chain:          circleChainToDomain(w.Blockchain),
		Address:        w.Address,
		CircleWalletID: w.ID,
		AccountType:    accountType,
		Status:         circleStateToDomainStatus(w.State),
		CreatedAt:      w.CreateDate,
		UpdatedAt:      w.UpdateDate,
	}
}

func preferredWalletsByChain(wallets []*entities.ManagedWallet) []*entities.ManagedWallet {
	byChain := make(map[entities.WalletChain]*entities.ManagedWallet)
	order := make([]entities.WalletChain, 0, len(wallets))

	for _, w := range wallets {
		if w == nil {
			continue
		}
		current, exists := byChain[w.Chain]
		if !exists {
			byChain[w.Chain] = w
			order = append(order, w.Chain)
			continue
		}
		if shouldPreferWallet(w, current) {
			byChain[w.Chain] = w
		}
	}

	preferred := make([]*entities.ManagedWallet, 0, len(byChain))
	for _, chain := range order {
		preferred = append(preferred, byChain[chain])
	}
	return preferred
}

func shouldPreferWallet(candidate, current *entities.ManagedWallet) bool {
	if current == nil {
		return true
	}
	if candidate == nil {
		return false
	}
	candidateReady := candidate.IsReady()
	currentReady := current.IsReady()
	if candidateReady != currentReady {
		return candidateReady
	}
	return candidate.UpdatedAt.After(current.UpdatedAt)
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
