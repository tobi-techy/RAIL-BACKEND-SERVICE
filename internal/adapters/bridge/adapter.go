package bridge

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// Adapter implements business logic layer for Bridge API
type Adapter struct {
	client BridgeClient
	logger *zap.Logger
}

// NewAdapter creates a new Bridge adapter
func NewAdapter(client BridgeClient, logger *zap.Logger) *Adapter {
	return &Adapter{
		client: client,
		logger: logger,
	}
}

// Client returns the underlying Bridge client
func (a *Adapter) Client() BridgeClient {
	return a.client
}

// CreateCustomerWithWallet creates a customer and associated wallet in one operation
func (a *Adapter) CreateCustomerWithWallet(ctx context.Context, req *CreateCustomerWithWalletRequest) (*CreateCustomerWithWalletResponse, error) {
	a.logger.Info("Creating Bridge customer with wallet", 
		zap.String("email", req.Email), 
		zap.String("first_name", req.FirstName),
		zap.String("last_name", req.LastName),
		zap.String("chain", string(req.Chain)))

	// Create customer first
	customerReq := &CreateCustomerRequest{
		Type:      CustomerTypeIndividual,
		FirstName: req.FirstName,
		LastName:  req.LastName,
		Email:     req.Email,
		Phone:     req.Phone,
	}

	customer, err := a.client.CreateCustomer(ctx, customerReq)
	if err != nil {
		a.logger.Error("Failed to create Bridge customer", zap.Error(err))
		return nil, fmt.Errorf("create customer failed: %w", err)
	}

	// Create wallet for the customer
	walletReq := &CreateWalletRequest{
		Chain:      req.Chain,
		Currency:   CurrencyUSDC,
		WalletType: WalletTypeUser,
	}

	wallet, err := a.client.CreateWallet(ctx, customer.ID, walletReq)
	if err != nil {
		a.logger.Error("Failed to create Bridge wallet", 
			zap.String("customer_id", customer.ID), 
			zap.Error(err))
		return nil, fmt.Errorf("create wallet failed: %w", err)
	}

	a.logger.Info("Successfully created customer and wallet",
		zap.String("customer_id", customer.ID),
		zap.String("wallet_id", wallet.ID),
		zap.String("address", wallet.Address))

	return &CreateCustomerWithWalletResponse{
		Customer: customer,
		Wallet:   wallet,
	}, nil
}

// CreateVirtualAccountForCustomer creates a virtual account for a customer
func (a *Adapter) CreateVirtualAccountForCustomer(ctx context.Context, customerID string, req *CreateVirtualAccountRequest) (*VirtualAccount, error) {
	a.logger.Info("Creating Bridge virtual account", zap.String("customer_id", customerID))

	virtualAccount, err := a.client.CreateVirtualAccount(ctx, customerID, req)
	if err != nil {
		a.logger.Error("Failed to create Bridge virtual account", 
			zap.String("customer_id", customerID), 
			zap.Error(err))
		return nil, fmt.Errorf("create virtual account failed: %w", err)
	}

	a.logger.Info("Successfully created virtual account",
		zap.String("customer_id", customerID),
		zap.String("virtual_account_id", virtualAccount.ID),
		zap.String("status", string(virtualAccount.Status)))

	return virtualAccount, nil
}

// GetCustomerStatus retrieves and maps customer status to domain KYC status
func (a *Adapter) GetCustomerStatus(ctx context.Context, customerID string) (*CustomerStatusResponse, error) {
	a.logger.Info("Getting Bridge customer status", zap.String("customer_id", customerID))

	customer, err := a.client.GetCustomer(ctx, customerID)
	if err != nil {
		a.logger.Error("Failed to get Bridge customer", 
			zap.String("customer_id", customerID), 
			zap.Error(err))
		return nil, fmt.Errorf("get customer failed: %w", err)
	}

	// Map Bridge customer status to domain KYC status
	kycStatus := mapBridgeCustomerStatusToKYCStatus(customer.Status)
	onboardingStatus := mapBridgeCustomerStatusToOnboardingStatus(customer.Status)

	return &CustomerStatusResponse{
		CustomerID:      customer.ID,
		BridgeStatus:    customer.Status,
		KYCStatus:       kycStatus,
		OnboardingStatus: onboardingStatus,
	}, nil
}

// GetKYCLinkForCustomer retrieves KYC verification link
func (a *Adapter) GetKYCLinkForCustomer(ctx context.Context, customerID string) (*KYCLinkResponse, error) {
	a.logger.Info("Getting Bridge KYC link", zap.String("customer_id", customerID))

	kycLink, err := a.client.GetKYCLink(ctx, customerID)
	if err != nil {
		a.logger.Error("Failed to get Bridge KYC link", 
			zap.String("customer_id", customerID), 
			zap.Error(err))
		return nil, fmt.Errorf("get KYC link failed: %w", err)
	}

	a.logger.Info("Successfully retrieved KYC link",
		zap.String("customer_id", customerID),
		zap.String("link_url", kycLink.KYCLink))

	return kycLink, nil
}

// CreateCardAccountForCustomer creates a card account for a customer
func (a *Adapter) CreateCardAccountForCustomer(ctx context.Context, customerID string, req *CreateCardAccountRequest) (*CardAccount, error) {
	a.logger.Info("Creating Bridge card account", zap.String("customer_id", customerID))

	cardAccount, err := a.client.CreateCardAccount(ctx, customerID, req)
	if err != nil {
		a.logger.Error("Failed to create Bridge card account", 
			zap.String("customer_id", customerID), 
			zap.Error(err))
		return nil, fmt.Errorf("create card account failed: %w", err)
	}

	a.logger.Info("Successfully created card account",
		zap.String("customer_id", customerID),
		zap.String("card_account_id", cardAccount.ID))

	return cardAccount, nil
}

// TransferFunds transfers funds between wallets or accounts
func (a *Adapter) TransferFunds(ctx context.Context, req *CreateTransferRequest) (*Transfer, error) {
	a.logger.Info("Creating Bridge transfer", 
		zap.String("amount", req.Amount))

	transfer, err := a.client.CreateTransfer(ctx, req)
	if err != nil {
		a.logger.Error("Failed to create Bridge transfer", zap.Error(err))
		return nil, fmt.Errorf("create transfer failed: %w", err)
	}

	a.logger.Info("Successfully created transfer",
		zap.String("transfer_id", transfer.ID),
		zap.String("status", string(transfer.Status)))

	return transfer, nil
}

// GetWalletBalance retrieves wallet balance
func (a *Adapter) GetWalletBalance(ctx context.Context, customerID, walletID string) (*WalletBalance, error) {
	a.logger.Info("Getting Bridge wallet balance", 
		zap.String("customer_id", customerID), 
		zap.String("wallet_id", walletID))

	balance, err := a.client.GetWalletBalance(ctx, customerID, walletID)
	if err != nil {
		a.logger.Error("Failed to get Bridge wallet balance", 
			zap.String("customer_id", customerID), 
			zap.String("wallet_id", walletID), 
			zap.Error(err))
		return nil, fmt.Errorf("get wallet balance failed: %w", err)
	}

	return balance, nil
}

// HealthCheck checks Bridge API connectivity
func (a *Adapter) HealthCheck(ctx context.Context) error {
	err := a.client.Ping(ctx)
	if err != nil {
		a.logger.Error("Bridge health check failed", zap.Error(err))
		return fmt.Errorf("bridge health check failed: %w", err)
	}

	a.logger.Info("Bridge health check passed")
	return nil
}

// Domain entity conversion functions

// ToDomainUser converts Bridge Customer to domain User entity
func (c *Customer) ToDomainUser(domainUserID uuid.UUID) *entities.User {
	if c == nil {
		return nil
	}

	kycStatus := mapBridgeCustomerStatusToKYCStatus(c.Status)
	kycProviderRef := &c.ID
	onboardingStatus := mapBridgeCustomerStatusToOnboardingStatus(c.Status)

	return &entities.User{
		ID:              domainUserID,
		Email:           c.Email,
		Phone:           &c.Phone,
		KYCStatus:       string(kycStatus),
		KYCProviderRef:  kycProviderRef,
		OnboardingStatus: onboardingStatus,
		EmailVerified:    false, // Default to false, will be updated via verification flow
		PhoneVerified:    false, // Default to false, will be updated via verification flow
		Role:            "user", // Default role
		IsActive:         true, // Default to active
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
}

// ToDomainUserProfile converts Bridge Customer to domain UserProfile entity
func (c *Customer) ToDomainUserProfile(domainProfileID uuid.UUID) *entities.UserProfile {
	if c == nil {
		return nil
	}

	kycStatus := mapBridgeCustomerStatusToKYCStatus(c.Status)
	onboardingStatus := mapBridgeCustomerStatusToOnboardingStatus(c.Status)
	bridgeCustomerID := c.ID

	return &entities.UserProfile{
		ID:                domainProfileID,
		Email:             c.Email,
		FirstName:         &c.FirstName,
		LastName:          &c.LastName,
		Phone:             &c.Phone,
		OnboardingStatus:  onboardingStatus,
		KYCStatus:         string(kycStatus),
		BridgeCustomerID:  &bridgeCustomerID,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
}

// ToDomainManagedWallet converts Bridge Wallet to domain ManagedWallet entity
func (w *Wallet) ToDomainManagedWallet(domainWalletID uuid.UUID, userID uuid.UUID) *entities.ManagedWallet {
	if w == nil {
		return nil
	}

	chain := mapBridgePaymentRailToWalletChain(w.Chain)
	status := mapBridgeWalletStatusToWalletStatus(w.Status)

	return &entities.ManagedWallet{
		ID:          domainWalletID,
		UserID:      userID,
		Chain:       chain,
		Address:     w.Address,
		AccountType: entities.AccountTypeEOA,
		Status:      status,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
}

// ToDomainVirtualAccount converts Bridge VirtualAccount to domain VirtualAccount entity
func (va *VirtualAccount) ToDomainVirtualAccount(domainVirtualAccountID uuid.UUID, userID uuid.UUID) *entities.VirtualAccount {
	if va == nil {
		return nil
	}

	status := mapBridgeVirtualAccountStatusToVirtualAccountStatus(va.Status)

	return &entities.VirtualAccount{
		ID:               domainVirtualAccountID,
		UserID:           userID,
		BridgeAccountID:  &va.ID,
		Status:           status,
		Currency:         "USD", // Bridge virtual accounts are USD
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
}

// Status mapping functions

func mapBridgeCustomerStatusToKYCStatus(status CustomerStatus) entities.KYCStatus {
	switch status {
	case CustomerStatusActive:
		return entities.KYCStatusApproved
	case CustomerStatusUnderReview:
		return entities.KYCStatusProcessing
	case CustomerStatusRejected:
		return entities.KYCStatusRejected
	case CustomerStatusIncomplete, CustomerStatusNotStarted, CustomerStatusAwaitingQuestionnaire, CustomerStatusAwaitingUBO:
		return entities.KYCStatusPending
	default:
		return entities.KYCStatusPending
	}
}

func mapBridgeCustomerStatusToOnboardingStatus(status CustomerStatus) entities.OnboardingStatus {
	switch status {
	case CustomerStatusActive:
		return entities.OnboardingStatusCompleted
	case CustomerStatusUnderReview:
		return entities.OnboardingStatusKYCPending
	case CustomerStatusRejected:
		return entities.OnboardingStatusKYCRejected
	case CustomerStatusIncomplete, CustomerStatusAwaitingQuestionnaire, CustomerStatusAwaitingUBO:
		return entities.OnboardingStatusKYCPending
	case CustomerStatusNotStarted:
		return entities.OnboardingStatusStarted
	case CustomerStatusOffboarded, CustomerStatusPaused:
		return entities.OnboardingStatusKYCRejected
	default:
		return entities.OnboardingStatusStarted
	}
}

func mapBridgePaymentRailToWalletChain(rail PaymentRail) entities.WalletChain {
	switch rail {
	case PaymentRailEthereum:
		return entities.WalletChainEthereum
	case PaymentRailPolygon:
		return entities.WalletChainPolygon
	case PaymentRailArbitrum:
		return entities.WalletChainArbitrum
	case PaymentRailBase:
		return entities.WalletChainBase
	case PaymentRailOptimism:
		return entities.WalletChainOptimism
	case PaymentRailAvalanche:
		return entities.WalletChainAvalanche
	case PaymentRailSolana:
		return entities.WalletChainSolana
	default:
		return entities.WalletChainEthereum // Default to Ethereum
	}
}

func mapBridgeWalletStatusToWalletStatus(walletStatus string) entities.WalletStatus {
	switch walletStatus {
	case "active", "live":
		return entities.WalletStatusLive
	case "creating", "pending":
		return entities.WalletStatusCreating
	case "failed":
		return entities.WalletStatusFailed
	default:
		return entities.WalletStatusCreating
	}
}

func mapBridgeVirtualAccountStatusToVirtualAccountStatus(status VirtualAccountStatus) entities.VirtualAccountStatus {
	switch status {
	case VirtualAccountStatusActivated:
		return entities.VirtualAccountStatusActive
	case VirtualAccountStatusDeactivated:
		return entities.VirtualAccountStatusClosed
	default:
		return entities.VirtualAccountStatusPending
	}
}

// Request/Response types for adapter layer

type CreateCustomerWithWalletRequest struct {
	FirstName string        `json:"first_name"`
	LastName  string        `json:"last_name"`
	Email     string        `json:"email"`
	Phone     string        `json:"phone"`
	Chain     PaymentRail   `json:"chain"`
}

type CreateCustomerWithWalletResponse struct {
	Customer *Customer `json:"customer"`
	Wallet   *Wallet   `json:"wallet"`
}

type CustomerStatusResponse struct {
	CustomerID      string                    `json:"customer_id"`
	BridgeStatus    CustomerStatus            `json:"bridge_status"`
	KYCStatus       entities.KYCStatus        `json:"kyc_status"`
	OnboardingStatus entities.OnboardingStatus `json:"onboarding_status"`
}