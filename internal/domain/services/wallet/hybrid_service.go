package wallet

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/adapters/bridge"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// BridgeClient interface for Bridge wallet operations
type BridgeClient interface {
	CreateWallet(ctx context.Context, customerID string, req *bridge.CreateWalletRequest) (*bridge.Wallet, error)
	GetWallet(ctx context.Context, customerID, walletID string) (*bridge.Wallet, error)
	ListWallets(ctx context.Context, customerID string) (*bridge.ListWalletsResponse, error)
	GetWalletBalance(ctx context.Context, customerID, walletID string) (*bridge.WalletBalance, error)
}

// HybridService handles wallet operations with Circle as primary, Bridge/Due as fallback
type HybridService struct {
	*Service // Embed existing Circle service as primary
	bridgeClient   BridgeClient
	customerIDRepo CustomerIDRepository
	logger         *zap.Logger
}

// CustomerIDRepository maps user IDs to Bridge customer IDs
type CustomerIDRepository interface {
	GetBridgeCustomerID(ctx context.Context, userID uuid.UUID) (string, error)
}

// NewHybridService creates a new hybrid wallet service
func NewHybridService(
	baseService *Service,
	bridgeClient BridgeClient,
	customerIDRepo CustomerIDRepository,
	logger *zap.Logger,
) *HybridService {
	return &HybridService{
		Service:        baseService,
		bridgeClient:   bridgeClient,
		customerIDRepo: customerIDRepo,
		logger:         logger,
	}
}

// CreateWalletsForUser creates wallets - Circle primary, Bridge/Due fallback for unsupported chains
func (s *HybridService) CreateWalletsForUser(ctx context.Context, userID uuid.UUID, chains []entities.WalletChain) error {
	s.logger.Info("Creating wallets for user (Circle primary, Bridge/Due fallback)",
		zap.String("userID", userID.String()),
		zap.Any("chains", chains))

	if len(chains) == 0 {
		chains = s.config.SupportedChains
	}

	// Group chains by provider
	circleChains := make([]entities.WalletChain, 0)
	bridgeChains := make([]entities.WalletChain, 0)
	dueChains := make([]entities.WalletChain, 0)

	for _, chain := range chains {
		provider := entities.GetProviderForChain(chain)
		switch provider {
		case entities.WalletProviderCircle:
			circleChains = append(circleChains, chain)
		case entities.WalletProviderBridge:
			bridgeChains = append(bridgeChains, chain)
		case entities.WalletProviderDue:
			dueChains = append(dueChains, chain)
		}
	}

	var lastErr error

	// 1. Create Circle wallets (primary)
	if len(circleChains) > 0 {
		s.logger.Info("Creating Circle wallets (primary)", zap.Any("chains", circleChains))
		if err := s.Service.CreateWalletsForUser(ctx, userID, circleChains); err != nil {
			s.logger.Error("Failed to create Circle wallets", zap.Error(err))
			lastErr = err
		}
	}

	// 2. Create Bridge wallets (fallback for ETH, Polygon, BSC, Base, Solana mainnet)
	if len(bridgeChains) > 0 {
		s.logger.Info("Creating Bridge wallets (fallback)", zap.Any("chains", bridgeChains))
		if err := s.createBridgeWallets(ctx, userID, bridgeChains); err != nil {
			s.logger.Error("Failed to create Bridge wallets", zap.Error(err))
			lastErr = err
		}
	}

	// 3. Due wallets for Starknet (created on-demand during deposits)
	if len(dueChains) > 0 {
		s.logger.Info("Due wallets will be created on-demand for Starknet deposits", zap.Any("chains", dueChains))
	}

	// Trigger onboarding completion if any wallets were created
	if s.onboardingService != nil && (len(circleChains) > 0 || len(bridgeChains) > 0) {
		if err := s.onboardingService.ProcessWalletCreationComplete(ctx, userID); err != nil {
			s.logger.Warn("Failed to process wallet creation complete", zap.Error(err))
		}
	}

	return lastErr
}

// createBridgeWallets creates wallets via Bridge API for unsupported Circle chains
func (s *HybridService) createBridgeWallets(ctx context.Context, userID uuid.UUID, chains []entities.WalletChain) error {
	customerID, err := s.customerIDRepo.GetBridgeCustomerID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get Bridge customer ID: %w", err)
	}

	for _, chain := range chains {
		if err := s.createBridgeWalletForChain(ctx, userID, customerID, chain); err != nil {
			s.logger.Error("Failed to create Bridge wallet",
				zap.String("chain", string(chain)),
				zap.Error(err))
			// Continue with other chains
		}
	}

	return nil
}

// createBridgeWalletForChain creates a single wallet via Bridge
func (s *HybridService) createBridgeWalletForChain(ctx context.Context, userID uuid.UUID, customerID string, chain entities.WalletChain) error {
	// Check if wallet already exists
	existing, err := s.walletRepo.GetByUserAndChain(ctx, userID, chain)
	if err == nil && existing != nil {
		s.logger.Info("Wallet already exists", zap.String("chain", string(chain)))
		return nil
	}

	paymentRail := chainToPaymentRail(chain)
	req := &bridge.CreateWalletRequest{
		Chain:    paymentRail,
		Currency: bridge.CurrencyUSDC,
	}

	wallet, err := s.bridgeClient.CreateWallet(ctx, customerID, req)
	if err != nil {
		return fmt.Errorf("Bridge wallet creation failed: %w", err)
	}

	managedWallet := &entities.ManagedWallet{
		ID:             uuid.New(),
		UserID:         userID,
		Chain:          chain,
		Address:        wallet.Address,
		BridgeWalletID: wallet.ID,
		Provider:       entities.WalletProviderBridge,
		AccountType:    entities.AccountTypeEOA,
		Status:         entities.WalletStatusLive,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := s.walletRepo.Create(ctx, managedWallet); err != nil {
		return fmt.Errorf("failed to store wallet: %w", err)
	}

	s.logger.Info("Created Bridge wallet (fallback)",
		zap.String("userID", userID.String()),
		zap.String("chain", string(chain)),
		zap.String("address", wallet.Address))

	return nil
}

// GetWalletAddresses returns wallet addresses from unified repository
func (s *HybridService) GetWalletAddresses(ctx context.Context, userID uuid.UUID, chain *entities.WalletChain) (*entities.WalletAddressesResponse, error) {
	return s.Service.GetWalletAddresses(ctx, userID, chain)
}

// GetWalletBalance returns balance routing to appropriate provider
func (s *HybridService) GetWalletBalance(ctx context.Context, userID uuid.UUID, chain entities.WalletChain) (string, error) {
	wallet, err := s.walletRepo.GetByUserAndChain(ctx, userID, chain)
	if err != nil {
		return "", fmt.Errorf("wallet not found: %w", err)
	}

	switch wallet.Provider {
	case entities.WalletProviderBridge:
		customerID, err := s.customerIDRepo.GetBridgeCustomerID(ctx, userID)
		if err != nil {
			return "", err
		}
		balance, err := s.bridgeClient.GetWalletBalance(ctx, customerID, wallet.BridgeWalletID)
		if err != nil {
			return "", err
		}
		return balance.Amount, nil
	default:
		// Circle/Due wallets - balance tracked in ledger
		return "0", nil
	}
}

// chainToPaymentRail converts WalletChain to Bridge PaymentRail
func chainToPaymentRail(chain entities.WalletChain) bridge.PaymentRail {
	switch chain {
	case entities.WalletChainEthereum:
		return bridge.PaymentRailEthereum
	case entities.WalletChainPolygon:
		return bridge.PaymentRailPolygon
	case entities.WalletChainBase:
		return bridge.PaymentRailBase
	case entities.WalletChainSolana:
		return bridge.PaymentRailSolana
	default:
		return bridge.PaymentRailEthereum
	}
}
