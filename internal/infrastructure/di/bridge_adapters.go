package di

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// ErrBridgeCustomerNotFound indicates the user has no Bridge customer ID
var ErrBridgeCustomerNotFound = errors.New("bridge customer ID not found for user")

// UserProfileRepository interface for fetching user profile
type UserProfileRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
}

// BridgeKYCAdapter implements KYC operations using Bridge API
type BridgeKYCAdapter struct {
	adapter  *bridge.Adapter
	userRepo UserProfileRepository
}

// NewBridgeKYCAdapter creates a new Bridge KYC adapter
func NewBridgeKYCAdapter(adapter *bridge.Adapter, userRepo UserProfileRepository) *BridgeKYCAdapter {
	return &BridgeKYCAdapter{
		adapter:  adapter,
		userRepo: userRepo,
	}
}

// SubmitKYC implements KYCProvider interface for Bridge
func (a *BridgeKYCAdapter) SubmitKYC(ctx context.Context, userID uuid.UUID, documents []entities.KYCDocumentUpload, personalInfo *entities.KYCPersonalInfo) (string, error) {
	customerID, err := a.getBridgeCustomerID(ctx, userID)
	if err != nil {
		return "", err
	}

	kycLink, err := a.adapter.GetKYCLinkForCustomer(ctx, customerID)
	if err != nil {
		return "", err
	}

	return kycLink.KYCLink, nil
}

// GetKYCStatus implements KYCProvider interface for Bridge
func (a *BridgeKYCAdapter) GetKYCStatus(ctx context.Context, providerRef string) (*entities.KYCSubmission, error) {
	status, err := a.adapter.GetCustomerStatus(ctx, providerRef)
	if err != nil {
		return nil, err
	}

	return &entities.KYCSubmission{
		Status:      status.KYCStatus,
		ProviderRef: providerRef,
	}, nil
}

// GenerateKYCURL implements KYCProvider interface for Bridge
func (a *BridgeKYCAdapter) GenerateKYCURL(ctx context.Context, userID uuid.UUID) (string, error) {
	customerID, err := a.getBridgeCustomerID(ctx, userID)
	if err != nil {
		return "", err
	}

	kycLink, err := a.adapter.GetKYCLinkForCustomer(ctx, customerID)
	if err != nil {
		return "", err
	}

	return kycLink.KYCLink, nil
}

// getBridgeCustomerID retrieves the Bridge customer ID from user profile
// TODO: Track issue to ensure BridgeCustomerID is stored during onboarding
// and handle the case where a Bridge customer does not yet exist
func (a *BridgeKYCAdapter) getBridgeCustomerID(ctx context.Context, userID uuid.UUID) (string, error) {
	profile, err := a.userRepo.GetByID(ctx, userID)
	if err != nil {
		return "", err
	}

	if profile.BridgeCustomerID == nil || *profile.BridgeCustomerID == "" {
		return "", ErrBridgeCustomerNotFound
	}

	return *profile.BridgeCustomerID, nil
}

// BridgeFundingAdapter implements funding operations using Bridge API
type BridgeFundingAdapter struct {
	adapter *bridge.Adapter
}

// NewBridgeFundingAdapter creates a new Bridge funding adapter
func NewBridgeFundingAdapter(adapter *bridge.Adapter) *BridgeFundingAdapter {
	return &BridgeFundingAdapter{
		adapter: adapter,
	}
}

// GenerateDepositAddress implements funding.CircleAdapter interface for Bridge
func (a *BridgeFundingAdapter) GenerateDepositAddress(ctx context.Context, chain entities.Chain, userID uuid.UUID) (string, error) {
	// Convert domain chain to Bridge payment rail
	paymentRail := mapChainToBridgePaymentRail(chain)
	if paymentRail == "" {
		return "", nil // TODO: Return proper error for unsupported chains
	}

	// For Bridge, wallets are created with customers
	// For now, return a placeholder implementation
	// In production, this would retrieve existing wallet or create new one
	return "0xbridge_placeholder_address", nil
}

// ValidateDeposit implements funding.CircleAdapter interface for Bridge
func (a *BridgeFundingAdapter) ValidateDeposit(ctx context.Context, txHash string, amount decimal.Decimal) (bool, error) {
	// Bridge transaction validation - placeholder implementation
	// In production, this would verify transaction on Bridge API
	return true, nil
}

// ConvertToUSD implements funding.CircleAdapter interface for Bridge
func (a *BridgeFundingAdapter) ConvertToUSD(ctx context.Context, amount decimal.Decimal, token entities.Stablecoin) (decimal.Decimal, error) {
	// Bridge USDC is already USD value, so return amount as-is
	return amount, nil
}

// GetWalletBalances implements funding.CircleAdapter interface for Bridge
func (a *BridgeFundingAdapter) GetWalletBalances(ctx context.Context, walletID string, tokenAddress ...string) (*entities.CircleWalletBalancesResponse, error) {
	// Get wallet balance from Bridge
	// For now, return placeholder implementation
	return &entities.CircleWalletBalancesResponse{
		TokenBalances: []entities.CircleTokenBalance{
			{
				Token: entities.CircleTokenInfo{
					Name:   "USDC",
					Symbol: "USDC",
				},
				Amount: "0.00",
			},
		},
	}, nil
}

// Helper functions

func mapChainToBridgePaymentRail(chain entities.Chain) bridge.PaymentRail {
	switch chain {
	case entities.ChainETH:
		return bridge.PaymentRailEthereum
	case entities.ChainMATIC:
		return bridge.PaymentRailPolygon
	case entities.ChainAVAX:
		return bridge.PaymentRailAvalanche
	case entities.ChainSOL:
		return bridge.PaymentRailSolana
	case entities.ChainARB:
		return bridge.PaymentRailArbitrum
	case entities.ChainBASE:
		return bridge.PaymentRailBase
	case entities.ChainOP:
		return bridge.PaymentRailOptimism
	default:
		return ""
	}
}