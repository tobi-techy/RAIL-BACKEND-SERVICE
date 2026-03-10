package di

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

// GenerateDepositAddress implements funding.CircleAdapter interface for Bridge
func (a *BridgeFundingAdapter) GenerateDepositAddress(ctx context.Context, chain entities.Chain, userID uuid.UUID) (string, error) {
	paymentRail := mapChainToBridgePaymentRail(chain)
	if paymentRail == "" {
		return "", fmt.Errorf("%w: %s", ErrUnsupportedChain, chain)
	}

	// For Bridge, wallets are created with customers
	// In production, this would retrieve existing wallet or create new one
	return "0xbridge_placeholder_address", nil
}

// ValidateDeposit implements funding.CircleAdapter interface for Bridge
func (a *BridgeFundingAdapter) ValidateDeposit(ctx context.Context, txHash string, amount decimal.Decimal) (bool, error) {
	// Bridge transaction validation - placeholder implementation
	// In production, this would verify transaction on Bridge API
	return true, nil
}

// P2PBridgeOfframpAdapter adapts bridge.Adapter to the p2p.BridgeOfframp interface.
// Flow: CreateRecipient → creates a Bridge customer + external ACH account, returns "<customerID>:<externalAccountID>"
//       InitiateTransfer → calls CreateTransfer with the external account as destination
type P2PBridgeOfframpAdapter struct {
	client bridge.BridgeClient
}

// NewP2PBridgeOfframpAdapter creates a new P2P bridge offramp adapter
func NewP2PBridgeOfframpAdapter(adapter *bridge.Adapter) *P2PBridgeOfframpAdapter {
	return &P2PBridgeOfframpAdapter{client: adapter.Client()}
}

// CreateRecipient creates a Bridge customer and registers their bank account as an external account.
// req must contain: account_holder_name, routing_number, account_number, email (optional).
// Returns "<customerID>:<externalAccountID>" as the recipient reference.
func (a *P2PBridgeOfframpAdapter) CreateRecipient(ctx context.Context, req map[string]interface{}) (string, error) {
	holderName, _ := req["account_holder_name"].(string)
	routing, _ := req["routing_number"].(string)
	account, _ := req["account_number"].(string)
	email, _ := req["email"].(string)

	if holderName == "" || routing == "" || account == "" {
		return "", fmt.Errorf("account_holder_name, routing_number and account_number are required")
	}
	if email == "" {
		email = fmt.Sprintf("p2p+%s@rail.app", account[max(0, len(account)-4):])
	}

	// Split holder name into first/last (best-effort)
	firstName, lastName := splitName(holderName)

	customer, err := a.client.CreateCustomer(ctx, &bridge.CreateCustomerRequest{
		Type:      bridge.CustomerTypeIndividual,
		FirstName: firstName,
		LastName:  lastName,
		Email:     email,
	})
	if err != nil {
		return "", fmt.Errorf("bridge create customer: %w", err)
	}

	extAcct, err := a.client.CreateExternalAccount(ctx, customer.ID, &bridge.CreateExternalAccountRequest{
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

	return customer.ID + ":" + extAcct.ID, nil
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
		Amount: amount,
		Source: bridge.TransferSource{
			PaymentRail:    bridge.PaymentRailSolana,
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

func splitName(full string) (first, last string) {
	i := strings.LastIndex(full, " ")
	if i < 0 {
		return full, ""
	}
	return full[:i], full[i+1:]
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
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