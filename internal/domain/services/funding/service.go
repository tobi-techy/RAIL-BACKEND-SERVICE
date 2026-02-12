package funding

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
)

// LedgerBalanceView represents user balance from ledger
type LedgerBalanceView struct {
	USDCBalance       decimal.Decimal
	FiatExposure      decimal.Decimal
	PendingInvestment decimal.Decimal
	TotalValue        decimal.Decimal
}

// LedgerIntegration interface for ledger operations
type LedgerIntegration interface {
	RecordDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, depositID uuid.UUID, chain, txHash string) error
	GetUserBalance(ctx context.Context, userID uuid.UUID) (*LedgerBalanceView, error)
}

// LimitsService interface for transaction limit validation
type LimitsService interface {
	ValidateDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (*entities.LimitCheckResult, error)
	RecordDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
}

// CacheClient interface for caching operations
type CacheClient interface {
	Get(ctx context.Context, key string, dest interface{}) error
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
}

// AuditService interface for compliance audit logging
type AuditService interface {
	LogDeposit(ctx context.Context, userID uuid.UUID, depositID uuid.UUID, amount string, chain string, status string) error
}

// FundingNotificationService interface for sending funding-related notifications
type FundingNotificationService interface {
	NotifyDepositConfirmed(ctx context.Context, userID uuid.UUID, amount, chain, txHash string) error
	NotifyLargeBalanceChange(ctx context.Context, userID uuid.UUID, changeType string, amount decimal.Decimal, newBalance decimal.Decimal) error
}

// Service handles funding operations - deposit addresses, confirmations, balance conversion
type Service struct {
	depositRepo          DepositRepository
	walletRepo           WalletRepository
	managedWalletRepo    ManagedWalletRepository
	virtualAccountRepo   VirtualAccountRepository
	circleAPI            CircleAdapter
	bridgeVAService      *BridgeVirtualAccountService
	alpacaAPI            AlpacaAdapter
	ledgerIntegration    LedgerIntegration
	limitsService        LimitsService
	validationService    *ValidationService
	auditService         AuditService
	notificationService  FundingNotificationService
	allocationService    AllocationService
	cache                CacheClient
	config               *FundingConfig
	logger               *logger.Logger
}

// DepositRepository interface for deposit persistence
type DepositRepository interface {
	Create(ctx context.Context, deposit *entities.Deposit) error
	GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Deposit, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, confirmedAt *time.Time) error
	GetByTxHash(ctx context.Context, txHash string) (*entities.Deposit, error)
}

// WalletRepository interface for wallet operations
type WalletRepository interface {
	GetByUserAndChain(ctx context.Context, userID uuid.UUID, chain entities.Chain) (*entities.Wallet, error)
	GetByAddress(ctx context.Context, address string) (*entities.Wallet, error)
	Create(ctx context.Context, wallet *entities.Wallet) error
}

// ManagedWalletRepository interface for managed wallet operations
type ManagedWalletRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.ManagedWallet, error)
	GetByCircleWalletID(ctx context.Context, circleWalletID string) (*entities.ManagedWallet, error)
}

// CircleAdapter interface for Circle API integration
type CircleAdapter interface {
	GenerateDepositAddress(ctx context.Context, chain entities.Chain, userID uuid.UUID) (string, error)
	ValidateDeposit(ctx context.Context, txHash string, amount decimal.Decimal) (bool, error)
	ConvertToUSD(ctx context.Context, amount decimal.Decimal, token entities.Stablecoin) (decimal.Decimal, error)
	GetWalletBalances(ctx context.Context, walletID string, tokenAddress ...string) (*entities.CircleWalletBalancesResponse, error)
}

// VirtualAccountRepository interface for virtual account persistence
type VirtualAccountRepository interface {
	Create(ctx context.Context, account *entities.VirtualAccount) error
	Update(ctx context.Context, account *entities.VirtualAccount) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.VirtualAccount, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.VirtualAccount, error)
	GetByAlpacaAccountID(ctx context.Context, alpacaAccountID string) (*entities.VirtualAccount, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status entities.VirtualAccountStatus) error
	ExistsByUserAndAlpacaAccount(ctx context.Context, userID uuid.UUID, alpacaAccountID string) (bool, error)
}

// AlpacaAdapter interface for Alpaca API integration
type AlpacaAdapter interface {
	GetAccount(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error)
	InitiateInstantFunding(ctx context.Context, req *entities.AlpacaInstantFundingRequest) (*entities.AlpacaInstantFundingResponse, error)
	GetInstantFundingStatus(ctx context.Context, transferID string) (*entities.AlpacaInstantFundingResponse, error)
	GetAccountBalance(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error)
	CreateJournal(ctx context.Context, req *entities.AlpacaJournalRequest) (*entities.AlpacaJournalResponse, error)
}

// NewService creates a new funding service
func NewService(
	depositRepo DepositRepository,
	walletRepo WalletRepository,
	managedWalletRepo ManagedWalletRepository,
	virtualAccountRepo VirtualAccountRepository,
	circleAPI CircleAdapter,
	alpacaAPI AlpacaAdapter,
	ledgerIntegration LedgerIntegration,
	logger *logger.Logger,
) *Service {
	return &Service{
		depositRepo:        depositRepo,
		walletRepo:         walletRepo,
		managedWalletRepo:  managedWalletRepo,
		virtualAccountRepo: virtualAccountRepo,
		circleAPI:          circleAPI,
		alpacaAPI:          alpacaAPI,
		ledgerIntegration:  ledgerIntegration,
		config:             DefaultFundingConfig(),
		logger:             logger,
	}
}

// SetValidationService sets the validation service (optional)
func (s *Service) SetValidationService(vs *ValidationService) {
	s.validationService = vs
}

// GetValidationService returns the validation service
func (s *Service) GetValidationService() *ValidationService {
	return s.validationService
}

// SetLimitsService sets the limits service for deposit/withdrawal validation (optional)
func (s *Service) SetLimitsService(ls LimitsService) {
	s.limitsService = ls
}

// SetCache sets the cache client (optional)
func (s *Service) SetCache(cache CacheClient) {
	s.cache = cache
}

// SetAuditService sets the audit service for compliance logging (optional)
func (s *Service) SetAuditService(as AuditService) {
	s.auditService = as
}

// SetNotificationService sets the notification service (optional)
func (s *Service) SetNotificationService(ns FundingNotificationService) {
	s.notificationService = ns
}

// SetBridgeVAService sets the Bridge virtual account service (optional)
func (s *Service) SetBridgeVAService(bva *BridgeVirtualAccountService) {
	s.bridgeVAService = bva
}

// SetAllocationService sets the allocation service for automatic 70/30 split (optional)
func (s *Service) SetAllocationService(as AllocationService) {
	s.allocationService = as
}

// CreateDepositAddress generates or retrieves deposit address for a chain
func (s *Service) CreateDepositAddress(ctx context.Context, userID uuid.UUID, chain entities.Chain) (*entities.DepositAddressResponse, error) {
	// Check rate limit for new address creation
	if s.validationService != nil {
		if err := s.validationService.CheckDepositRateLimit(ctx, userID); err != nil {
			return nil, err
		}
	}

	// Check if user already has a wallet for this chain
	wallet, err := s.walletRepo.GetByUserAndChain(ctx, userID, chain)
	if err != nil && err.Error() != "wallet not found" {
		return nil, fmt.Errorf("failed to check existing wallet: %w", err)
	}

	var address string
	if wallet != nil {
		address = wallet.Address
		s.logger.Info("Using existing wallet address", "user_id", userID, "chain", chain, "address", address)
	} else {
		// Generate new address through Circle
		address, err = s.circleAPI.GenerateDepositAddress(ctx, chain, userID)
		if err != nil {
			return nil, fmt.Errorf("failed to generate deposit address: %w", err)
		}

		// Create wallet record
		wallet = &entities.Wallet{
			ID:          uuid.New(),
			UserID:      userID,
			Chain:       chain,
			Address:     address,
			ProviderRef: fmt.Sprintf("circle-%s", address),
			Status:      "active",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		if err := s.walletRepo.Create(ctx, wallet); err != nil {
			return nil, fmt.Errorf("failed to create wallet record: %w", err)
		}

		s.logger.Info("Created new wallet address", "user_id", userID, "chain", chain, "address", address)
	}

	return &entities.DepositAddressResponse{
		Chain:   chain,
		Address: address,
		QRCode:  nil, // Could generate QR code URL here
	}, nil
}

// GetFundingConfirmations retrieves recent funding confirmations for user
func (s *Service) GetFundingConfirmations(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.FundingConfirmation, error) {
	deposits, err := s.depositRepo.GetByUserID(ctx, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get deposits: %w", err)
	}

	confirmations := make([]*entities.FundingConfirmation, len(deposits))
	for i, deposit := range deposits {
		var confirmedAt time.Time
		if deposit.ConfirmedAt != nil {
			confirmedAt = *deposit.ConfirmedAt
		} else {
			confirmedAt = deposit.CreatedAt
		}
		confirmations[i] = &entities.FundingConfirmation{
			ID:          deposit.ID,
			Chain:       deposit.Chain,
			TxHash:      deposit.TxHash,
			Token:       deposit.Token,
			Amount:      deposit.Amount.String(),
			Status:      deposit.Status,
			ConfirmedAt: confirmedAt,
		}
	}

	return confirmations, nil
}

// GetBalance returns user's current balance from ledger with caching
func (s *Service) GetBalance(ctx context.Context, userID uuid.UUID) (*entities.BalancesResponse, error) {
	// Try cache first
	if s.cache != nil {
		var cached entities.BalancesResponse
		cacheKey := BalanceCacheKey(userID)
		if err := s.cache.Get(ctx, cacheKey, &cached); err == nil {
			s.logger.Debug("Balance retrieved from cache", "user_id", userID.String())
			return &cached, nil
		}
	}

	s.logger.Info("Fetching user balance from ledger", "user_id", userID.String())

	// Get balance from ledger integration
	ledgerBalance, err := s.ledgerIntegration.GetUserBalance(ctx, userID)
	if err != nil {
		s.logger.Error("Failed to get ledger balance", "error", err, "user_id", userID.String())
		return &entities.BalancesResponse{
			BuyingPower:     "0.00",
			PendingDeposits: "0.00",
			Currency:        "USD",
		}, nil
	}

	// USDC balance is available funds, fiat exposure is buying power at broker
	// Total buying power = USDC + fiat exposure
	totalBuyingPower := ledgerBalance.USDCBalance.Add(ledgerBalance.FiatExposure)

	response := &entities.BalancesResponse{
		BuyingPower:     totalBuyingPower.String(),
		PendingDeposits: ledgerBalance.PendingInvestment.String(),
		Currency:        "USD",
	}

	// Cache the result
	if s.cache != nil && s.config != nil {
		cacheKey := BalanceCacheKey(userID)
		_ = s.cache.Set(ctx, cacheKey, response, s.config.BalanceCacheTTL)
	}

	s.logger.Info("Balance retrieved from ledger",
		"user_id", userID.String(),
		"usdc_balance", ledgerBalance.USDCBalance.String(),
		"fiat_exposure", ledgerBalance.FiatExposure.String(),
		"pending_investment", ledgerBalance.PendingInvestment.String(),
		"total_buying_power", totalBuyingPower.String())

	return response, nil
}

// ProcessChainDeposit processes incoming chain deposit webhook
func (s *Service) ProcessChainDeposit(ctx context.Context, webhook *entities.ChainDepositWebhook) error {
	s.logger.Info("Processing chain deposit", "chain", webhook.Chain, "tx_hash", webhook.TxHash, "amount", webhook.Amount)

	// Parse amount directly to decimal (never use float for money)
	amount, err := decimal.NewFromString(webhook.Amount)
	if err != nil {
		return fmt.Errorf("invalid deposit amount %q: %w", webhook.Amount, err)
	}

	// Validate minimum deposit amount
	if s.validationService != nil {
		if err := s.validationService.ValidateDepositAmount(amount); err != nil {
			s.logger.Warn("Deposit below minimum amount", "tx_hash", webhook.TxHash, "amount", amount.String())
			return err
		}
	} else if amount.LessThan(decimal.NewFromFloat(entities.MinDepositAmountUSDC)) {
		return fmt.Errorf("deposit amount %s is below minimum %v USDC", amount.String(), entities.MinDepositAmountUSDC)
	}

	// Validate the deposit with Circle
	isValid, err := s.circleAPI.ValidateDeposit(ctx, webhook.TxHash, amount)
	if err != nil {
		return fmt.Errorf("failed to validate deposit: %w", err)
	}

	if !isValid {
		s.logger.Warn("Invalid deposit received", "tx_hash", webhook.TxHash)
		return fmt.Errorf("invalid deposit signature or amount")
	}

	// Check if deposit already exists (idempotency check)
	existingDeposit, err := s.depositRepo.GetByTxHash(ctx, webhook.TxHash)
	if err != nil && err.Error() != "deposit not found" {
		return fmt.Errorf("failed to check existing deposit: %w", err)
	}

	if existingDeposit != nil {
		s.logger.Info("Deposit already processed", "tx_hash", webhook.TxHash)
		return nil
	}

	// Find the wallet to get user ID
	wallet, err := s.walletRepo.GetByAddress(ctx, webhook.Address)
	if err != nil {
		return fmt.Errorf("failed to find wallet for address %s: %w", webhook.Address, err)
	}

	// Convert stablecoin to USD buying power
	usdAmount, err := s.circleAPI.ConvertToUSD(ctx, amount, webhook.Token)
	if err != nil {
		return fmt.Errorf("failed to convert to USD: %w", err)
	}

	// Validate against user's deposit limits (if limits service is configured)
	if s.limitsService != nil {
		result, err := s.limitsService.ValidateDeposit(ctx, wallet.UserID, usdAmount)
		if err != nil {
			s.logger.Warn("Deposit limit validation failed",
				"user_id", wallet.UserID.String(),
				"amount", usdAmount.String(),
				"error", err.Error(),
				"limit_type", result.LimitType,
			)
			return fmt.Errorf("deposit limit exceeded: %w", err)
		}
	}

	// Create deposit record
	now := time.Now()
	deposit := &entities.Deposit{
		ID:          uuid.New(),
		UserID:      wallet.UserID,
		Chain:       webhook.Chain,
		TxHash:      webhook.TxHash,
		Token:       webhook.Token,
		Amount:      amount,
		Status:      "confirmed",
		ConfirmedAt: &now,
		CreatedAt:   now,
	}

	if err := s.depositRepo.Create(ctx, deposit); err != nil {
		return fmt.Errorf("failed to create deposit record: %w", err)
	}

	// Record deposit in ledger (replaces legacy balance update)
	if err := s.ledgerIntegration.RecordDeposit(ctx, wallet.UserID, usdAmount, deposit.ID, string(webhook.Chain), webhook.TxHash); err != nil {
		return fmt.Errorf("failed to record deposit in ledger: %w", err)
	}

	// Process automatic 70/30 allocation split
	// This is the core system rule: every deposit is automatically split
	if s.allocationService != nil {
		allocationReq := &entities.IncomingFundsRequest{
			UserID:     wallet.UserID,
			Amount:     usdAmount,
			EventType:  entities.AllocationEventTypeCryptoDeposit,
			DepositID:  &deposit.ID,
			SourceTxID: &webhook.TxHash,
			Metadata: map[string]any{
				"source":   "crypto",
				"chain":    string(webhook.Chain),
				"token":    string(webhook.Token),
				"tx_hash":  webhook.TxHash,
			},
		}
		if err := s.allocationService.ProcessIncomingFunds(ctx, allocationReq); err != nil {
			s.logger.Error("Failed to process allocation split",
				"user_id", wallet.UserID,
				"amount", usdAmount,
				"error", err)
			// Don't fail the deposit - allocation can be retried
			// The funds are safely in the ledger
		} else {
			s.logger.Info("Automatic 70/30 allocation split completed",
				"user_id", wallet.UserID,
				"amount", usdAmount)
		}
	}

	// Record deposit usage against limits
	if s.limitsService != nil {
		if err := s.limitsService.RecordDeposit(ctx, wallet.UserID, usdAmount); err != nil {
			s.logger.Warn("Failed to record deposit usage", "error", err, "user_id", wallet.UserID.String())
			// Don't fail the deposit, just log the warning
		}
	}

	// Create audit log entry for compliance
	if s.auditService != nil {
		if err := s.auditService.LogDeposit(ctx, wallet.UserID, deposit.ID, usdAmount.String(), string(webhook.Chain), deposit.Status); err != nil {
			s.logger.Warn("Failed to create audit log for deposit", "error", err, "deposit_id", deposit.ID.String())
			// Don't fail the deposit, audit logging is non-critical
		}
	}

	// Send deposit confirmation notification
	if s.notificationService != nil {
		if err := s.notificationService.NotifyDepositConfirmed(ctx, wallet.UserID, usdAmount.String(), string(webhook.Chain), webhook.TxHash); err != nil {
			s.logger.Warn("Failed to send deposit notification", "error", err, "user_id", wallet.UserID.String())
		}
		// Notify for large deposits (>= $1000)
		largeDepositThreshold := decimal.NewFromInt(1000)
		if usdAmount.GreaterThanOrEqual(largeDepositThreshold) {
			// Get new balance for notification
			if balance, err := s.ledgerIntegration.GetUserBalance(ctx, wallet.UserID); err == nil {
				_ = s.notificationService.NotifyLargeBalanceChange(ctx, wallet.UserID, "deposit", usdAmount, balance.TotalValue)
			}
		}
	}

	s.logger.Info("Deposit processed successfully",
		"user_id", wallet.UserID,
		"amount", webhook.Amount,
		"usd_amount", usdAmount.String(),
		"tx_hash", webhook.TxHash,
	)

	return nil
}

// CreateVirtualAccount creates a virtual account linked to an Alpaca brokerage account
// Now uses Bridge API instead of Due
func (s *Service) CreateVirtualAccount(ctx context.Context, req *entities.CreateVirtualAccountRequest) (*entities.CreateVirtualAccountResponse, error) {
	s.logger.Info("Creating virtual account", "user_id", req.UserID.String(), "alpaca_account_id", req.AlpacaAccountID)

	// Check if virtual account already exists for this user and Alpaca account
	exists, err := s.virtualAccountRepo.ExistsByUserAndAlpacaAccount(ctx, req.UserID, req.AlpacaAccountID)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing virtual account: %w", err)
	}

	if exists {
		s.logger.Info("Virtual account already exists", "user_id", req.UserID.String(), "alpaca_account_id", req.AlpacaAccountID)
		return nil, fmt.Errorf("virtual account already exists for this Alpaca account")
	}

	// Verify Alpaca account exists and is accessible
	alpacaAccount, err := s.alpacaAPI.GetAccount(ctx, req.AlpacaAccountID)
	if err != nil {
		return nil, fmt.Errorf("failed to verify Alpaca account: %w", err)
	}

	if alpacaAccount.Status != entities.AlpacaAccountStatusActive {
		return nil, fmt.Errorf("Alpaca account is not active: %s", alpacaAccount.Status)
	}

	// Bridge virtual account service must be configured
	if s.bridgeVAService == nil {
		return nil, fmt.Errorf("bridge virtual account service not configured")
	}

	// Get deposit instructions from Bridge (virtual account already created during onboarding)
	accounts, err := s.bridgeVAService.GetVirtualAccounts(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to get virtual accounts: %w", err)
	}

	var virtualAccount *entities.VirtualAccount
	for _, acc := range accounts {
		if acc.Currency == "USD" && acc.Status == entities.VirtualAccountStatusActive {
			virtualAccount = acc
			break
		}
	}

	if virtualAccount == nil {
		return nil, fmt.Errorf("no active USD virtual account found for user")
	}

	// Update existing virtual account with Alpaca account ID
	virtualAccount.AlpacaAccountID = req.AlpacaAccountID
	if err := s.virtualAccountRepo.Update(ctx, virtualAccount); err != nil {
		return nil, fmt.Errorf("failed to update virtual account: %w", err)
	}

	s.logger.Info("Virtual account linked successfully",
		"virtual_account_id", virtualAccount.ID.String(),
		"bridge_account_id", virtualAccount.BridgeAccountID,
		"alpaca_account_id", virtualAccount.AlpacaAccountID)

	return &entities.CreateVirtualAccountResponse{
		VirtualAccount: virtualAccount,
		Message:        "Virtual account created successfully",
	}, nil
}

// InitiateBrokerFunding initiates funding to Alpaca brokerage account after off-ramp completion
func (s *Service) InitiateBrokerFunding(ctx context.Context, depositID uuid.UUID, alpacaAccountID string, amount decimal.Decimal) error {
	s.logger.Info("Initiating broker funding",
		"deposit_id", depositID.String(),
		"alpaca_account_id", alpacaAccountID,
		"amount", amount.String())

	// Verify Alpaca account is active
	alpacaAccount, err := s.alpacaAPI.GetAccount(ctx, alpacaAccountID)
	if err != nil {
		s.logger.Error("Failed to get Alpaca account", "error", err, "alpaca_account_id", alpacaAccountID)
		return fmt.Errorf("failed to get Alpaca account: %w", err)
	}

	if alpacaAccount.Status != entities.AlpacaAccountStatusActive {
		s.logger.Error("Alpaca account not active",
			"alpaca_account_id", alpacaAccountID,
			"status", alpacaAccount.Status)
		return fmt.Errorf("Alpaca account not active: %s", alpacaAccount.Status)
	}

	// Create instant funding transfer to extend buying power immediately
	instantFundingReq := &entities.AlpacaInstantFundingRequest{
		AccountNo:       alpacaAccount.AccountNumber,
		SourceAccountNo: "SI", // Source account for instant funding
		Amount:          amount,
	}

	instantFundingResp, err := s.alpacaAPI.InitiateInstantFunding(ctx, instantFundingReq)
	if err != nil {
		s.logger.Error("Failed to initiate instant funding",
			"error", err,
			"alpaca_account_id", alpacaAccountID,
			"amount", amount.String())
		return fmt.Errorf("failed to initiate instant funding: %w", err)
	}

	s.logger.Info("Instant funding initiated successfully",
		"transfer_id", instantFundingResp.ID,
		"status", instantFundingResp.Status,
		"deadline", instantFundingResp.Deadline,
		"alpaca_account_id", alpacaAccountID)

	// Update deposit status to broker_funded
	now := time.Now()
	if err := s.depositRepo.UpdateStatus(ctx, depositID, "broker_funded", &now); err != nil {
		s.logger.Error("Failed to update deposit status",
			"error", err,
			"deposit_id", depositID.String())
		return fmt.Errorf("failed to update deposit status: %w", err)
	}

	s.logger.Info("Broker funding completed",
		"deposit_id", depositID.String(),
		"transfer_id", instantFundingResp.ID,
		"alpaca_account_id", alpacaAccountID)

	return nil
}
