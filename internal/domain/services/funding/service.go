package funding

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/pkg/logger"
)

// Service handles funding operations - deposit addresses, confirmations, balance conversion
type Service struct {
	depositRepo         DepositRepository
	balanceRepo         BalanceRepository
	walletRepo          WalletRepository
	managedWalletRepo   ManagedWalletRepository
	virtualAccountRepo  VirtualAccountRepository
	circleAPI           CircleAdapter
	dueAPI              DueAdapter
	alpacaAPI           AlpacaAdapter
	logger              *logger.Logger
}

// DepositRepository interface for deposit persistence
type DepositRepository interface {
	Create(ctx context.Context, deposit *entities.Deposit) error
	GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Deposit, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, confirmedAt *time.Time) error
	GetByTxHash(ctx context.Context, txHash string) (*entities.Deposit, error)
}

// BalanceRepository interface for balance management
type BalanceRepository interface {
	Get(ctx context.Context, userID uuid.UUID) (*entities.Balance, error)
	UpdateBuyingPower(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
	UpdatePendingDeposits(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
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
	GetByID(ctx context.Context, id uuid.UUID) (*entities.VirtualAccount, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.VirtualAccount, error)
	GetByAlpacaAccountID(ctx context.Context, alpacaAccountID string) (*entities.VirtualAccount, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status entities.VirtualAccountStatus) error
	ExistsByUserAndAlpacaAccount(ctx context.Context, userID uuid.UUID, alpacaAccountID string) (bool, error)
}

// DueAdapter interface for Due API integration
type DueAdapter interface {
	CreateVirtualAccount(ctx context.Context, userID uuid.UUID, alpacaAccountID string) (*entities.VirtualAccount, error)
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
	balanceRepo BalanceRepository,
	walletRepo WalletRepository,
	managedWalletRepo ManagedWalletRepository,
	virtualAccountRepo VirtualAccountRepository,
	circleAPI CircleAdapter,
	dueAPI DueAdapter,
	alpacaAPI AlpacaAdapter,
	logger *logger.Logger,
) *Service {
	return &Service{
		depositRepo:         depositRepo,
		balanceRepo:         balanceRepo,
		walletRepo:          walletRepo,
		managedWalletRepo:   managedWalletRepo,
		virtualAccountRepo:  virtualAccountRepo,
		circleAPI:           circleAPI,
		dueAPI:              dueAPI,
		alpacaAPI:           alpacaAPI,
		logger:              logger,
	}
}

// CreateDepositAddress generates or retrieves deposit address for a chain
func (s *Service) CreateDepositAddress(ctx context.Context, userID uuid.UUID, chain entities.Chain) (*entities.DepositAddressResponse, error) {
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

// GetBalance returns user's current balance with real-time Circle wallet balances
func (s *Service) GetBalance(ctx context.Context, userID uuid.UUID) (*entities.BalancesResponse, error) {
	s.logger.Info("Fetching user balance with real-time Circle wallet data", "user_id", userID.String())

	// Get user's managed wallets
	managedWallets, err := s.managedWalletRepo.GetByUserID(ctx, userID)
	if err != nil {
		s.logger.Error("Failed to get managed wallets", "error", err, "user_id", userID.String())
		// Fallback to database balance
		return s.getDatabaseBalance(ctx, userID)
	}

	if len(managedWallets) == 0 {
		s.logger.Info("No managed wallets found for user, returning zero balance", "user_id", userID.String())
		return &entities.BalancesResponse{
			BuyingPower:     "0.00",
			PendingDeposits: "0.00",
			Currency:        "USD",
		}, nil
	}

	// Aggregate USDC balance from all Circle wallets
	totalUSDCBalance := decimal.Zero
	walletsProcessed := 0

	for _, wallet := range managedWallets {
		if wallet.CircleWalletID == "" || wallet.Status != entities.WalletStatusLive {
			s.logger.Debug("Skipping wallet - not ready",
				"wallet_id", wallet.ID.String(),
				"circle_wallet_id", wallet.CircleWalletID,
				"status", wallet.Status)
			continue
		}

		// Fetch real-time balance from Circle API
		balanceResp, err := s.circleAPI.GetWalletBalances(ctx, wallet.CircleWalletID)
		if err != nil {
			s.logger.Warn("Failed to fetch Circle wallet balance, skipping",
				"error", err,
				"wallet_id", wallet.ID.String(),
				"circle_wallet_id", wallet.CircleWalletID,
				"chain", wallet.Chain)
			continue
		}

		// Extract USDC balance
		usdcBalanceStr := balanceResp.GetUSDCBalance()
		if usdcBalanceStr != "0" {
			usdcBalance, err := decimal.NewFromString(usdcBalanceStr)
			if err != nil {
				s.logger.Warn("Failed to parse USDC balance",
					"error", err,
					"balance_str", usdcBalanceStr,
					"circle_wallet_id", wallet.CircleWalletID)
				continue
			}

			totalUSDCBalance = totalUSDCBalance.Add(usdcBalance)
			walletsProcessed++

			s.logger.Info("Retrieved wallet balance",
				"circle_wallet_id", wallet.CircleWalletID,
				"chain", wallet.Chain,
				"usdc_balance", usdcBalanceStr,
				"running_total", totalUSDCBalance.String())
		}
	}

	s.logger.Info("Aggregated Circle wallet balances",
		"user_id", userID.String(),
		"total_usdc", totalUSDCBalance.String(),
		"wallets_processed", walletsProcessed,
		"total_wallets", len(managedWallets))

	// Get pending deposits from database
	pendingDeposits := decimal.Zero
	dbBalance, err := s.balanceRepo.Get(ctx, userID)
	if err == nil {
		pendingDeposits = dbBalance.PendingDeposits
	}

	// USDC is 1:1 with USD, so buying power = USDC balance
	return &entities.BalancesResponse{
		BuyingPower:     totalUSDCBalance.String(),
		PendingDeposits: pendingDeposits.String(),
		Currency:        "USD",
	}, nil
}

// getDatabaseBalance retrieves balance from database as fallback
func (s *Service) getDatabaseBalance(ctx context.Context, userID uuid.UUID) (*entities.BalancesResponse, error) {
	balance, err := s.balanceRepo.Get(ctx, userID)
	if err != nil {
		if err.Error() == "balance not found" {
			// Return zero balance for new users
			return &entities.BalancesResponse{
				BuyingPower:     "0.00",
				PendingDeposits: "0.00",
				Currency:        "USD",
			}, nil
		}
		return nil, fmt.Errorf("failed to get balance: %w", err)
	}

	return &entities.BalancesResponse{
		BuyingPower:     balance.BuyingPower.String(),
		PendingDeposits: balance.PendingDeposits.String(),
		Currency:        balance.Currency,
	}, nil
}

// ProcessChainDeposit processes incoming chain deposit webhook
func (s *Service) ProcessChainDeposit(ctx context.Context, webhook *entities.ChainDepositWebhook) error {
	s.logger.Info("Processing chain deposit", "chain", webhook.Chain, "tx_hash", webhook.TxHash, "amount", webhook.Amount)

	// Validate the deposit with Circle
	amountFloat, err := strconv.ParseFloat(webhook.Amount, 64)
	if err != nil {
		return fmt.Errorf("invalid deposit amount %q: %w", webhook.Amount, err)
	}
	amount := decimal.NewFromFloat(amountFloat)
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

	// Update user's buying power
	if err := s.balanceRepo.UpdateBuyingPower(ctx, wallet.UserID, usdAmount); err != nil {
		return fmt.Errorf("failed to update buying power: %w", err)
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

	// Create virtual account via Due API
	virtualAccount, err := s.dueAPI.CreateVirtualAccount(ctx, req.UserID, req.AlpacaAccountID)
	if err != nil {
		return nil, fmt.Errorf("failed to create virtual account via Due API: %w", err)
	}

	// Store virtual account in database
	if err := s.virtualAccountRepo.Create(ctx, virtualAccount); err != nil {
		return nil, fmt.Errorf("failed to store virtual account: %w", err)
	}

	s.logger.Info("Virtual account created successfully",
		"virtual_account_id", virtualAccount.ID.String(),
		"due_account_id", virtualAccount.DueAccountID,
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
