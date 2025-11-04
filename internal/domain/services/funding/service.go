package funding

import (
"context"
"fmt"
"strconv"
"time"

"github.com/google/uuid"
"github.com/shopspring/decimal"
"github.com/stack-service/stack_service/internal/adapters/due"
"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/pkg/logger"
)

// Service handles funding operations - deposit addresses, confirmations, balance conversion
type Service struct {
	depositRepo       DepositRepository
	balanceRepo       BalanceRepository
	walletRepo        WalletRepository
	managedWalletRepo ManagedWalletRepository
	virtualAccountRepo VirtualAccountRepository
	circleAPI         CircleAdapter
	dueAPI            DueAdapter
	logger            *logger.Logger
}

// DepositRepository interface for deposit persistence
type DepositRepository interface {
	Create(ctx context.Context, deposit *entities.Deposit) error
	GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Deposit, error)
	GetByID(ctx context.Context, id uuid.UUID) (*entities.Deposit, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, confirmedAt *time.Time) error
	UpdateOffRampStatus(ctx context.Context, id uuid.UUID, status string, transferRef string) error
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
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.VirtualAccount, error)
	GetByID(ctx context.Context, id uuid.UUID) (*entities.VirtualAccount, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status entities.VirtualAccountStatus) error
	GetByDueAccountID(ctx context.Context, dueAccountID string) (*entities.VirtualAccount, error)
}

// DueAdapter interface for Due API integration
type DueAdapter interface {
CreateVirtualAccount(ctx context.Context, userID string, destination string, schemaIn string, currencyIn string, railOut string, currencyOut string) (*entities.VirtualAccount, error)
ListVirtualAccounts(ctx context.Context, filters map[string]string) ([]due.VirtualAccountSummary, error)
GetVirtualAccount(ctx context.Context, reference string) (*due.GetVirtualAccountResponse, error)
// Transfer methods for off-ramp functionality
CreateQuote(ctx context.Context, req due.CreateQuoteRequest) (*due.CreateQuoteResponse, error)
CreateTransfer(ctx context.Context, req due.CreateTransferRequest) (*due.CreateTransferResponse, error)
CreateTransferIntent(ctx context.Context, transferID string) (*due.CreateTransferIntentResponse, error)
SubmitTransferIntent(ctx context.Context, req due.SubmitTransferIntentRequest) error
CreateFundingAddress(ctx context.Context, transferID string) (*due.CreateFundingAddressResponse, error)
GetTransfer(ctx context.Context, transferID string) (*due.GetTransferResponse, error)
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
	logger *logger.Logger,
) *Service {
	return &Service{
		depositRepo:       depositRepo,
		balanceRepo:       balanceRepo,
		walletRepo:        walletRepo,
		managedWalletRepo: managedWalletRepo,
		virtualAccountRepo: virtualAccountRepo,
		circleAPI:         circleAPI,
		dueAPI:            dueAPI,
		logger:            logger,
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

	// Create deposit record with initial confirmed_on_chain status
	deposit := &entities.Deposit{
		ID:          uuid.New(),
		UserID:      wallet.UserID,
		Chain:       webhook.Chain,
		TxHash:      webhook.TxHash,
		Token:       webhook.Token,
		Amount:      amount,
		Status:      "confirmed_on_chain",
		ConfirmedAt: &webhook.BlockTime,
		CreatedAt:   time.Now(),
	}

	if err := s.depositRepo.Create(ctx, deposit); err != nil {
		return fmt.Errorf("failed to create deposit record: %w", err)
	}

	// For USDC deposits, initiate Due off-ramp instead of direct buying power credit
	if webhook.Token == entities.StablecoinUSDC {
		// Get or create virtual account for the user
		virtualAccount, err := s.getOrCreateVirtualAccount(ctx, wallet.UserID)
		if err != nil {
			s.logger.Error("Failed to get virtual account for off-ramp", "error", err, "user_id", wallet.UserID)
			// Don't fail deposit processing - can retry later via background job
		} else {
			// Initiate Due transfer
			transferID, err := s.InitiateDueTransfer(ctx, deposit.ID, virtualAccount.ID)
			if err != nil {
				s.logger.Error("Failed to initiate Due transfer", "error", err, "deposit_id", deposit.ID)
				// Update deposit status to indicate off-ramp failure
				s.depositRepo.UpdateStatus(ctx, deposit.ID, "off_ramp_failed", nil)
			} else {
				s.logger.Info("Due off-ramp initiated for deposit",
					"deposit_id", deposit.ID,
					"transfer_id", transferID,
					"user_id", wallet.UserID)
			}
		}
	} else {
		// For non-USDC tokens, maintain existing behavior (direct buying power credit)
		if err := s.balanceRepo.UpdateBuyingPower(ctx, wallet.UserID, usdAmount); err != nil {
			return fmt.Errorf("failed to update buying power: %w", err)
		}
	}

	s.logger.Info("Deposit processed successfully",
		"user_id", wallet.UserID,
		"amount", webhook.Amount,
		"token", string(webhook.Token),
		"usd_amount", usdAmount.String(),
		"tx_hash", webhook.TxHash,
		"status", deposit.Status,
	)

	return nil
}

// getOrCreateVirtualAccount gets existing virtual account or creates a new one
func (s *Service) getOrCreateVirtualAccount(ctx context.Context, userID uuid.UUID) (*entities.VirtualAccount, error) {
	// Check if user already has a virtual account
	existingAccount, err := s.virtualAccountRepo.GetByUserID(ctx, userID)
	if err != nil && err.Error() != "virtual account not found" {
		return nil, fmt.Errorf("failed to check existing virtual account: %w", err)
	}

	if existingAccount != nil {
		return existingAccount, nil
	}

	// Create new virtual account
	return s.CreateVirtualAccount(ctx, userID)
}

// CreateVirtualAccount creates a new virtual account through the Due API
func (s *Service) CreateVirtualAccount(ctx context.Context, userID uuid.UUID) (*entities.VirtualAccount, error) {
	s.logger.Info("Creating virtual account", "user_id", userID)

	// Check if user already has a virtual account
	existingAccount, err := s.virtualAccountRepo.GetByUserID(ctx, userID)
	if err != nil && err.Error() != "virtual account not found" {
		return nil, fmt.Errorf("failed to check existing virtual account: %w", err)
	}

	if existingAccount != nil {
		s.logger.Info("User already has virtual account", "user_id", userID, "account_id", existingAccount.ID)
		return existingAccount, nil
	}

	// Create virtual account through Due API
	// TODO: Make these parameters configurable based on user preferences
 	destination := "wlt_" + userID.String() // Placeholder - should be actual wallet address
 	schemaIn := "bank_sepa"
 	currencyIn := "EUR"
 	railOut := "ethereum"
 	currencyOut := "USDC"
 	virtualAccount, err := s.dueAPI.CreateVirtualAccount(ctx, userID.String(), destination, schemaIn, currencyIn, railOut, currencyOut)
	if err != nil {
		s.logger.Error("Failed to create virtual account with Due API", "error", err, "user_id", userID)
		return nil, fmt.Errorf("failed to create virtual account: %w", err)
	}

	// Persist to database
	if err := s.virtualAccountRepo.Create(ctx, virtualAccount); err != nil {
		s.logger.Error("Failed to persist virtual account", "error", err, "user_id", userID)
		return nil, fmt.Errorf("failed to persist virtual account: %w", err)
	}

	s.logger.Info("Virtual account created successfully",
		"user_id", userID,
		"virtual_account_id", virtualAccount.ID,
		"due_account_id", virtualAccount.DueAccountID,
		"status", virtualAccount.Status)

		return virtualAccount, nil
}

// InitiateDueTransfer handles the complete off-ramp flow for converting USDC deposits to USD
func (s *Service) InitiateDueTransfer(ctx context.Context, depositID uuid.UUID, virtualAccountID uuid.UUID) (transferID string, err error) {
	s.logger.Info("Initiating Due off-ramp transfer",
		"deposit_id", depositID,
		"virtual_account_id", virtualAccountID)

	// 1. Get deposit details
	deposit, err := s.depositRepo.GetByID(ctx, depositID)
	if err != nil {
		return "", fmt.Errorf("failed to get deposit: %w", err)
	}

	// 2. Validate deposit status is 'confirmed_on_chain'
	if deposit.Status != "confirmed_on_chain" {
		return "", fmt.Errorf("deposit status is %s, expected confirmed_on_chain", deposit.Status)
	}

	// 3. Get recipient details from virtual account
	// For now, we'll use a placeholder recipient - in production this would be configured
	recipientID := "recipient_" + virtualAccountID.String() // Placeholder

	// 4. Determine sender wallet address from deposit
	// TODO: Get actual wallet address from deposit or user wallet lookup
	senderAddress := "0x" + deposit.UserID.String() // Use full UUID for now as placeholder

	// 5. Create Due transfer quote
	quoteReq := due.CreateQuoteRequest{
		Source: due.QuoteSide{
			Rail:     "ethereum", // Assuming Ethereum for now - should be dynamic based on deposit chain
			Currency: "USDC",
			Amount:   deposit.Amount.String(),
		},
		Destination: due.QuoteSide{
			Rail:     "ach", // Assuming ACH for now - should be configurable
			Currency: "USD",
		},
	}

	quote, err := s.dueAPI.CreateQuote(ctx, quoteReq)
	if err != nil {
		return "", fmt.Errorf("failed to create transfer quote: %w", err)
	}

	// 6. Create transfer with quote
	transferReq := due.CreateTransferRequest{
		Quote:     quote.Token,
		Sender:    senderAddress,
		Recipient: recipientID,
		Memo:      fmt.Sprintf("Off-ramp deposit: %s", depositID.String()),
	}

	transfer, err := s.dueAPI.CreateTransfer(ctx, transferReq)
	if err != nil {
		return "", fmt.Errorf("failed to create transfer: %w", err)
	}

	// 7. Update deposit status to off_ramp_initiated
	err = s.depositRepo.UpdateOffRampStatus(ctx, depositID, "off_ramp_initiated", transfer.ID)
	if err != nil {
		s.logger.Error("Failed to update deposit status",
			"error", err,
			"deposit_id", depositID)
		// Don't fail the operation - transfer is created, just logging issue
	}

	s.logger.Info("Due off-ramp transfer initiated successfully",
		"deposit_id", depositID,
		"transfer_id", transfer.ID,
		"amount_usdc", deposit.Amount.String(),
		"expected_usd", transfer.Destination.Amount)

	return transfer.ID, nil
}
