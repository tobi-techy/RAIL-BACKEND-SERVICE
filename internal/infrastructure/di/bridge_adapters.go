package di

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/shopspring/decimal"
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

// getBridgeCustomerID retrieves the Bridge customer ID from user profile.
// If no Bridge customer exists, it creates one automatically.
func (a *BridgeKYCAdapter) getBridgeCustomerID(ctx context.Context, userID uuid.UUID) (string, error) {
	profile, err := a.userRepo.GetByID(ctx, userID)
	if err != nil {
		return "", err
	}

	if profile.BridgeCustomerID != nil && *profile.BridgeCustomerID != "" {
		return *profile.BridgeCustomerID, nil
	}

	// No Bridge customer exists - this is a data integrity issue
	// The BridgeCustomerID should be set during onboarding when CreateCustomerWithWallet is called
	return "", ErrBridgeCustomerNotFound
}

// ErrUnsupportedChain indicates the chain is not supported by Bridge
var ErrUnsupportedChain = errors.New("chain not supported by Bridge")

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

// GenerateDepositAddress implements deposit address creation using Bridge
func (a *BridgeFundingAdapter) GenerateDepositAddress(ctx context.Context, chain entities.Chain, userID uuid.UUID) (string, error) {
	paymentRail := mapChainToBridgePaymentRail(chain)
	if paymentRail == "" {
		return "", fmt.Errorf("%w: %s", ErrUnsupportedChain, chain)
	}

	// For Bridge, wallets are created with customers
	// In production, this would retrieve existing wallet or create new one
	return "", fmt.Errorf("GetWalletAddress not implemented")
}

// ValidateDeposit implements deposit validation using Bridge
func (a *BridgeFundingAdapter) ValidateDeposit(ctx context.Context, txHash string, amount decimal.Decimal) (bool, error) {
	// Bridge transaction validation - placeholder implementation
	// In production, this would verify transaction on Bridge API
	return true, nil
}

// P2PBridgeOfframpAdapter adapts bridge.Adapter to the p2p.BridgeOfframp interface.
// Flow: CreateRecipient → registers an external ACH account under the sender's Bridge customer,
//
//	InitiateTransfer → calls CreateTransfer with the external account as destination.
type P2PBridgeOfframpAdapter struct {
	client bridge.BridgeClient
}

// NewP2PBridgeOfframpAdapter creates a new P2P bridge offramp adapter
func NewP2PBridgeOfframpAdapter(adapter *bridge.Adapter) *P2PBridgeOfframpAdapter {
	return &P2PBridgeOfframpAdapter{client: adapter.Client()}
}

// CreateRecipient registers a bank account as an external account under the provided Bridge customer.
// req must contain: customer_id, account_holder_name, routing_number, account_number.
// Returns "<customerID>:<externalAccountID>" as the recipient reference.
func (a *P2PBridgeOfframpAdapter) CreateRecipient(ctx context.Context, req map[string]interface{}) (string, error) {
	customerID, _ := req["customer_id"].(string)
	holderName, _ := req["account_holder_name"].(string)
	routing, _ := req["routing_number"].(string)
	account, _ := req["account_number"].(string)

	if customerID == "" || holderName == "" || routing == "" || account == "" {
		return "", fmt.Errorf("customer_id, account_holder_name, routing_number and account_number are required")
	}

	extAcct, err := a.client.CreateExternalAccount(ctx, customerID, &bridge.CreateExternalAccountRequest{
		Currency: bridge.CurrencyUSD,
		BankDetails: bridge.ExternalAccountBankDetails{
			AccountOwnerName: holderName,
			AccountType:      bridge.ExternalAccountChecking,
			RoutingNumber:    routing,
			AccountNumber:    account,
		},
	})
	if err != nil {
		return "", fmt.Errorf("bridge create external account: %w", err)
	}

	return customerID + ":" + extAcct.ID, nil
}

// InitiateTransfer sends USDC → USD ACH via Bridge.
// req must contain: amount (string), recipient_id ("<customerID>:<externalAccountID>"), source_wallet_id.
func (a *P2PBridgeOfframpAdapter) InitiateTransfer(ctx context.Context, req map[string]interface{}) (map[string]interface{}, error) {
	amount, _ := req["amount"].(string)
	recipientID, _ := req["recipient_id"].(string)
	sourceWalletID, _ := req["source_wallet_id"].(string)

	parts := strings.SplitN(recipientID, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid recipient_id format, expected <customerID>:<externalAccountID>")
	}
	externalAccountID := parts[1]

	transfer, err := a.client.CreateTransfer(ctx, &bridge.CreateTransferRequest{
		OnBehalfOf: fmt.Sprintf("%v", req["on_behalf_of"]),
		Amount:     amount,
		Source: bridge.TransferSource{
			PaymentRail:    bridge.PaymentRail("bridge_wallet"),
			Currency:       bridge.CurrencyUSDC,
			BridgeWalletID: sourceWalletID,
		},
		Destination: bridge.TransferDestination{
			PaymentRail:       "us_ach",
			Currency:          bridge.CurrencyUSD,
			ExternalAccountID: externalAccountID,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("bridge initiate transfer: %w", err)
	}

	return map[string]interface{}{
		"id":     transfer.ID,
		"status": string(transfer.State),
	}, nil
}

// Helper functions

func mapChainToBridgePaymentRail(chain entities.Chain) bridge.PaymentRail {
	switch chain {
	case entities.ChainMATIC, entities.ChainMATICAmoy:
		return bridge.PaymentRailPolygon
	case entities.ChainAVAX, entities.ChainAVAXFuji:
		return bridge.PaymentRailAvalanche
	case entities.ChainSOL, entities.ChainSOLDevnet:
		return bridge.PaymentRailSolana
	case entities.ChainBASE, entities.ChainBASESepolia:
		return bridge.PaymentRailBase
	default:
		return ""
	}
}

// USDBBridgeClient implements usdstash.BridgeClient using the Bridge API.
type USDBBridgeClient struct {
	client *bridge.Client
}

func (a *USDBBridgeClient) GetOrCreateUSDBWallet(ctx context.Context, customerID string) (string, error) {
	resp, err := a.client.ListWallets(ctx, customerID)
	if err != nil {
		return "", err
	}
	for _, w := range resp.Data {
		if w.Currency == bridge.CurrencyUSDB {
			return w.ID, nil
		}
	}
	// No USDB wallet yet — create one on Solana (primary chain)
	w, err := a.client.CreateWallet(ctx, customerID, &bridge.CreateWalletRequest{
		Chain:    bridge.PaymentRailSolana,
		Currency: bridge.CurrencyUSDB,
	})
	if err != nil {
		return "", err
	}
	return w.ID, nil
}

func (a *USDBBridgeClient) GetUSDCWalletID(ctx context.Context, customerID string) (string, error) {
	resp, err := a.client.ListWallets(ctx, customerID)
	if err != nil {
		return "", err
	}
	for _, w := range resp.Data {
		if w.Currency == bridge.CurrencyUSDC {
			return w.ID, nil
		}
	}
	return "", fmt.Errorf("no USDC custody wallet found for customer %s", customerID)
}

func (a *USDBBridgeClient) TransferBetweenWallets(ctx context.Context, customerID, sourceWalletID, destWalletID string, amount decimal.Decimal, _ string) error {
	_, err := a.client.CreateTransfer(ctx, &bridge.CreateTransferRequest{
		OnBehalfOf: customerID,
		Amount:     amount.StringFixed(6),
		Source: bridge.TransferSource{
			PaymentRail:    "bridge_wallet",
			Currency:       bridge.CurrencyUSDC,
			BridgeWalletID: sourceWalletID,
		},
		Destination: bridge.TransferDestination{
			PaymentRail:    "bridge_wallet",
			Currency:       bridge.CurrencyUSDB,
			BridgeWalletID: destWalletID,
		},
	})
	return err
}
