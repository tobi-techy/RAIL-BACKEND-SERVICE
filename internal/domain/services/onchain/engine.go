package onchain

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/rail-service/rail_service/internal/infrastructure/circle"

	"github.com/rail-service/rail_service/pkg/logger"
)

// AllocationService defines the interface for allocation operations
type AllocationService interface {
	GetMode(ctx context.Context, userID uuid.UUID) (*entities.SmartAllocationMode, error)
	ProcessIncomingFunds(ctx context.Context, req *entities.IncomingFundsRequest) error
}

// Engine handles all blockchain and Circle wallet interactions
type Engine struct {
	ledgerService      *ledger.Service
	allocationService  AllocationService
	circleClient       *circle.Client
	depositRepo        DepositRepository
	withdrawalRepo     WithdrawalRepository
	walletRepo         WalletRepository
	managedWalletRepo  ManagedWalletRepository
	logger             *logger.Logger
	config             *EngineConfig
}

// EngineConfig holds onchain engine configuration
type EngineConfig struct {
	// Deposit monitoring
	DepositPollInterval     time.Duration
	ConfirmationBlocks      map[entities.Chain]int
	MinDepositAmount        decimal.Decimal
	
	// Withdrawal execution
	WithdrawalGasBuffer     decimal.Decimal // Extra gas to ensure tx success
	WithdrawalRetryAttempts int
	WithdrawalTimeout       time.Duration
	
	// Buffer monitoring
	BufferCheckInterval     time.Duration
	BufferAlertThreshold    decimal.Decimal // Alert if USDC buffer below this
}

// DepositRepository handles deposit persistence
type DepositRepository interface {
	Create(ctx context.Context, deposit *entities.Deposit) error
	GetByTxHash(ctx context.Context, txHash string) (*entities.Deposit, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, confirmedAt *time.Time) error
	GetPendingDeposits(ctx context.Context) ([]*entities.Deposit, error)
}

// WithdrawalRepository handles withdrawal persistence
type WithdrawalRepository interface {
	Create(ctx context.Context, withdrawal *entities.Withdrawal) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.Withdrawal, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status entities.WithdrawalStatus) error
	GetPendingWithdrawals(ctx context.Context) ([]*entities.Withdrawal, error)
	UpdateTxHash(ctx context.Context, id uuid.UUID, txHash string) error
}

// WalletRepository handles wallet operations
type WalletRepository interface {
	GetByUserAndChain(ctx context.Context, userID uuid.UUID, chain entities.Chain) (*entities.Wallet, error)
	Create(ctx context.Context, wallet *entities.Wallet) error
}

// ManagedWalletRepository handles managed wallet operations
type ManagedWalletRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.ManagedWallet, error)
	GetByCircleWalletID(ctx context.Context, circleWalletID string) (*entities.ManagedWallet, error)
	GetAll(ctx context.Context) ([]*entities.ManagedWallet, error)
}

// NewEngine creates a new onchain engine
func NewEngine(
	ledgerService *ledger.Service,
	allocationService AllocationService,
	circleClient *circle.Client,
	depositRepo DepositRepository,
	withdrawalRepo WithdrawalRepository,
	walletRepo WalletRepository,
	managedWalletRepo ManagedWalletRepository,
	logger *logger.Logger,
	config *EngineConfig,
) *Engine {
	if config == nil {
		config = DefaultEngineConfig()
	}

	return &Engine{
		ledgerService:     ledgerService,
		allocationService: allocationService,
		circleClient:      circleClient,
		depositRepo:       depositRepo,
		withdrawalRepo:    withdrawalRepo,
		walletRepo:        walletRepo,
		managedWalletRepo: managedWalletRepo,
		logger:            logger,
		config:            config,
	}
}

// DefaultEngineConfig returns default configuration
func DefaultEngineConfig() *EngineConfig {
	return &EngineConfig{
		DepositPollInterval:     30 * time.Second,
		ConfirmationBlocks:      map[entities.Chain]int{
			entities.ChainSolana:  32,
			entities.ChainPolygon: 128,
		},
		MinDepositAmount:        decimal.NewFromFloat(1.0), // $1 minimum
		WithdrawalGasBuffer:     decimal.NewFromFloat(0.001), // Small buffer
		WithdrawalRetryAttempts: 3,
		WithdrawalTimeout:       10 * time.Minute,
		BufferCheckInterval:     1 * time.Minute,
		BufferAlertThreshold:    decimal.NewFromFloat(5000.0), // $5k alert
	}
}

// ============================================================================
// DEPOSIT PROCESSING
// ============================================================================

// ProcessDeposit handles a new deposit detected on-chain
// This is called when Circle webhook notifies us of an incoming transfer
func (e *Engine) ProcessDeposit(ctx context.Context, req *DepositRequest) error {
	e.logger.Info("Processing deposit",
		"tx_hash", req.TxHash,
		"chain", req.Chain,
		"amount", req.Amount,
		"user_id", req.UserID)

	// Check if deposit already exists (idempotency)
	existing, err := e.depositRepo.GetByTxHash(ctx, req.TxHash)
	if err == nil && existing != nil {
		e.logger.Info("Deposit already processed (idempotent)",
			"deposit_id", existing.ID,
			"status", existing.Status)
		return nil
	}

	// Validate deposit amount
	if req.Amount.LessThan(e.config.MinDepositAmount) {
		return fmt.Errorf("deposit amount %s below minimum %s", 
			req.Amount, e.config.MinDepositAmount)
	}

	// Get user's managed wallet to verify ownership
	managedWallet, err := e.managedWalletRepo.GetByCircleWalletID(ctx, req.CircleWalletID)
	if err != nil {
		return fmt.Errorf("failed to get managed wallet: %w", err)
	}

	if managedWallet.UserID != req.UserID {
		return fmt.Errorf("wallet user mismatch: expected %s, got %s", 
			managedWallet.UserID, req.UserID)
	}

	// Create deposit record
	now := time.Now()
	deposit := &entities.Deposit{
		ID:              uuid.New(),
		UserID:          req.UserID,
		Chain:           req.Chain,
		TxHash:          req.TxHash,
		Token:           req.Token,
		Amount:          req.Amount,
		Status:          "pending",
		CreatedAt:       now,
	}

	if err := e.depositRepo.Create(ctx, deposit); err != nil {
		return fmt.Errorf("failed to create deposit record: %w", err)
	}

	// Post ledger entries immediately (optimistic)
	if err := e.postDepositLedgerEntries(ctx, deposit); err != nil {
		e.logger.Error("Failed to post deposit ledger entries",
			"deposit_id", deposit.ID,
			"error", err)
		return fmt.Errorf("failed to post ledger entries: %w", err)
	}

	// Update deposit status
	confirmedAt := time.Now()
	if err := e.depositRepo.UpdateStatus(ctx, deposit.ID, "confirmed", &confirmedAt); err != nil {
		e.logger.Error("Failed to update deposit status",
			"deposit_id", deposit.ID,
			"error", err)
		// Don't fail - ledger entries are already posted
	}

	e.logger.Info("Deposit processed successfully",
		"deposit_id", deposit.ID,
		"user_id", req.UserID,
		"amount", req.Amount)

	return nil
}

// postDepositLedgerEntries creates ledger entries for a deposit
// Checks for allocation mode and splits funds 70/30 if active, otherwise uses legacy flow
func (e *Engine) postDepositLedgerEntries(ctx context.Context, deposit *entities.Deposit) error {
	e.logger.Info("Posting deposit ledger entries",
		"deposit_id", deposit.ID,
		"user_id", deposit.UserID,
		"amount", deposit.Amount)

	// Check if user has smart allocation mode active
	mode, err := e.allocationService.GetMode(ctx, deposit.UserID)
	if err != nil {
		e.logger.Warn("Failed to check allocation mode, falling back to legacy flow",
			"error", err,
			"user_id", deposit.UserID)
		// Continue with legacy flow on error
		mode = nil
	}

	if mode != nil && mode.Active {
		// Smart allocation mode is active - use allocation service to split funds
		e.logger.Info("Processing deposit with smart allocation split",
			"deposit_id", deposit.ID,
			"user_id", deposit.UserID,
			"spending_ratio", mode.RatioSpending,
			"stash_ratio", mode.RatioStash)

		txHash := deposit.TxHash
		allocationReq := &entities.IncomingFundsRequest{
			UserID:     deposit.UserID,
			Amount:     deposit.Amount,
			EventType:  entities.AllocationEventTypeDeposit,
			SourceTxID: &txHash,
			Metadata: map[string]any{
				"deposit_id": deposit.ID.String(),
				"chain":      deposit.Chain,
				"token":      deposit.Token,
			},
			DepositID: &deposit.ID,
		}

		if err := e.allocationService.ProcessIncomingFunds(ctx, allocationReq); err != nil {
			return fmt.Errorf("failed to process allocation: %w", err)
		}

		e.logger.Info("Deposit processed with allocation split",
			"deposit_id", deposit.ID,
			"user_id", deposit.UserID)

		return nil
	}

	// Legacy flow: No allocation mode active, credit to usdc_balance
	e.logger.Debug("Processing deposit with legacy flow (no allocation)",
		"deposit_id", deposit.ID,
		"user_id", deposit.UserID)

	// Get or create user's USDC balance account
	userAccount, err := e.ledgerService.GetOrCreateUserAccount(ctx, deposit.UserID, entities.AccountTypeUSDCBalance)
	if err != nil {
		return fmt.Errorf("failed to get user account: %w", err)
	}

	// Get system buffer account
	systemAccount, err := e.ledgerService.GetSystemAccount(ctx, entities.AccountTypeSystemBufferUSDC)
	if err != nil {
		return fmt.Errorf("failed to get system account: %w", err)
	}

	// Create ledger transaction
	desc := fmt.Sprintf("Deposit: %s USDC on %s (Tx: %s)", 
		deposit.Amount.String(), deposit.Chain, deposit.TxHash)
	
	metadata := map[string]interface{}{
		"deposit_id": deposit.ID.String(),
		"tx_hash":    deposit.TxHash,
		"chain":      deposit.Chain,
		"token":      deposit.Token,
	}

	ledgerReq := &entities.CreateTransactionRequest{
		UserID:          &deposit.UserID,
		TransactionType: entities.TransactionTypeDeposit,
		ReferenceID:     &deposit.ID,
		ReferenceType:   stringPtr("deposit"),
		IdempotencyKey:  fmt.Sprintf("deposit-%s", deposit.ID.String()),
		Description:     &desc,
		Metadata:        metadata,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   userAccount.ID,
				EntryType:   entities.EntryTypeDebit, // Increase user balance
				Amount:      deposit.Amount,
				Currency:    "USDC",
				Description: &desc,
			},
			{
				AccountID:   systemAccount.ID,
				EntryType:   entities.EntryTypeCredit, // Decrease system buffer
				Amount:      deposit.Amount,
				Currency:    "USDC",
				Description: &desc,
			},
		},
	}

	ledgerTx, err := e.ledgerService.CreateTransaction(ctx, ledgerReq)
	if err != nil {
		return fmt.Errorf("failed to create ledger transaction: %w", err)
	}

	e.logger.Info("Deposit ledger entries posted (legacy flow)",
		"deposit_id", deposit.ID,
		"ledger_tx_id", ledgerTx.ID,
		"user_account", userAccount.ID,
		"amount", deposit.Amount)

	return nil
}

// MonitorDeposits polls Circle for new deposits
// This is a fallback in case webhooks fail
func (e *Engine) MonitorDeposits(ctx context.Context) error {
	// Get all managed wallets
	wallets, err := e.managedWalletRepo.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("failed to get managed wallets: %w", err)
	}

	for _, wallet := range wallets {
		// Check Circle for wallet balance/transactions
		// This would query Circle API for recent transactions
		// and process any that aren't in our deposits table
		
		// Note: Actual implementation depends on Circle API capabilities
		e.logger.Debug("Monitoring wallet for deposits",
			"wallet_id", wallet.CircleWalletID,
			"user_id", wallet.UserID)
	}

	return nil
}

// ============================================================================
// WITHDRAWAL EXECUTION
// ============================================================================

// ExecuteWithdrawal processes a withdrawal request
// Transfers USDC from system buffer to user's destination address
func (e *Engine) ExecuteWithdrawal(ctx context.Context, withdrawalID uuid.UUID) error {
	// Get withdrawal record
	withdrawal, err := e.withdrawalRepo.GetByID(ctx, withdrawalID)
	if err != nil {
		return fmt.Errorf("failed to get withdrawal: %w", err)
	}

	e.logger.Info("Executing withdrawal",
		"withdrawal_id", withdrawalID,
		"user_id", withdrawal.UserID,
		"amount", withdrawal.Amount,
		"chain", withdrawal.DestinationChain)

	// Check if already processed
	if withdrawal.Status != entities.WithdrawalStatusPending {
		e.logger.Warn("Withdrawal not in pending status",
			"withdrawal_id", withdrawalID,
			"status", withdrawal.Status)
		return fmt.Errorf("withdrawal not pending: status=%s", withdrawal.Status)
	}

	// Verify user has sufficient ledger balance
	balance, err := e.ledgerService.GetAccountBalance(ctx, withdrawal.UserID, entities.AccountTypeUSDCBalance)
	if err != nil {
		return fmt.Errorf("failed to get user balance: %w", err)
	}

	if balance.LessThan(withdrawal.Amount) {
		e.logger.Error("Insufficient balance for withdrawal",
			"withdrawal_id", withdrawalID,
			"balance", balance,
			"requested", withdrawal.Amount)
		
		// Mark as failed
		if err := e.withdrawalRepo.UpdateStatus(ctx, withdrawalID, entities.WithdrawalStatusFailed); err != nil {
			e.logger.Error("Failed to update withdrawal status", "error", err)
		}
		return fmt.Errorf("insufficient balance: have %s, need %s", balance, withdrawal.Amount)
	}

	// Post ledger entries first (debit user balance, credit system buffer)
	if err := e.postWithdrawalLedgerEntries(ctx, withdrawal); err != nil {
		return fmt.Errorf("failed to post ledger entries: %w", err)
	}

	// Execute on-chain transfer via Circle
	txHash, err := e.executeCircleTransfer(ctx, withdrawal)
	if err != nil {
		e.logger.Error("Failed to execute Circle transfer",
			"withdrawal_id", withdrawalID,
			"error", err)
		
		// Reversal should be handled separately
		// For now, mark as failed
		if err := e.withdrawalRepo.UpdateStatus(ctx, withdrawalID, entities.WithdrawalStatusFailed); err != nil {
			e.logger.Error("Failed to update withdrawal status", "error", err)
		}
		return fmt.Errorf("failed to execute transfer: %w", err)
	}

	// Update withdrawal with tx hash
	if err := e.withdrawalRepo.UpdateTxHash(ctx, withdrawalID, txHash); err != nil {
		e.logger.Error("Failed to update withdrawal tx hash",
			"withdrawal_id", withdrawalID,
			"tx_hash", txHash,
			"error", err)
	}

	// Mark as completed
	if err := e.withdrawalRepo.UpdateStatus(ctx, withdrawalID, entities.WithdrawalStatusCompleted); err != nil {
		e.logger.Error("Failed to update withdrawal status", "error", err)
		// Don't fail - transfer is done
	}

	e.logger.Info("Withdrawal executed successfully",
		"withdrawal_id", withdrawalID,
		"tx_hash", txHash,
		"amount", withdrawal.Amount)

	return nil
}

// postWithdrawalLedgerEntries creates ledger entries for a withdrawal
// Debit user's usdc_balance, Credit system_buffer_usdc
func (e *Engine) postWithdrawalLedgerEntries(ctx context.Context, withdrawal *entities.Withdrawal) error {
	e.logger.Info("Posting withdrawal ledger entries",
		"withdrawal_id", withdrawal.ID,
		"user_id", withdrawal.UserID,
		"amount", withdrawal.Amount)

	// Get user's USDC balance account
	userAccount, err := e.ledgerService.GetOrCreateUserAccount(ctx, withdrawal.UserID, entities.AccountTypeUSDCBalance)
	if err != nil {
		return fmt.Errorf("failed to get user account: %w", err)
	}

	// Get system buffer account
	systemAccount, err := e.ledgerService.GetSystemAccount(ctx, entities.AccountTypeSystemBufferUSDC)
	if err != nil {
		return fmt.Errorf("failed to get system account: %w", err)
	}

	// Create ledger transaction
	desc := fmt.Sprintf("Withdrawal: %s USDC to %s on %s", 
		withdrawal.Amount.String(), withdrawal.DestinationAddress, withdrawal.DestinationChain)
	
	metadata := map[string]interface{}{
		"withdrawal_id":       withdrawal.ID.String(),
		"destination_address": withdrawal.DestinationAddress,
		"destination_chain":   withdrawal.DestinationChain,
	}

	ledgerReq := &entities.CreateTransactionRequest{
		UserID:          &withdrawal.UserID,
		TransactionType: entities.TransactionTypeWithdrawal,
		ReferenceID:     &withdrawal.ID,
		ReferenceType:   stringPtr("withdrawal"),
		IdempotencyKey:  fmt.Sprintf("withdrawal-%s", withdrawal.ID.String()),
		Description:     &desc,
		Metadata:        metadata,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   userAccount.ID,
				EntryType:   entities.EntryTypeCredit, // Decrease user balance
				Amount:      withdrawal.Amount,
				Currency:    "USDC",
				Description: &desc,
			},
			{
				AccountID:   systemAccount.ID,
				EntryType:   entities.EntryTypeDebit, // Increase system buffer
				Amount:      withdrawal.Amount,
				Currency:    "USDC",
				Description: &desc,
			},
		},
	}

	ledgerTx, err := e.ledgerService.CreateTransaction(ctx, ledgerReq)
	if err != nil {
		return fmt.Errorf("failed to create ledger transaction: %w", err)
	}

	e.logger.Info("Withdrawal ledger entries posted",
		"withdrawal_id", withdrawal.ID,
		"ledger_tx_id", ledgerTx.ID,
		"amount", withdrawal.Amount)

	return nil
}

// executeCircleTransfer executes the actual on-chain transfer via Circle
func (e *Engine) executeCircleTransfer(ctx context.Context, withdrawal *entities.Withdrawal) (string, error) {
	// Get user's managed wallet for the destination chain
	wallets, err := e.managedWalletRepo.GetByUserID(ctx, withdrawal.UserID)
	if err != nil {
		return "", fmt.Errorf("failed to get user wallets: %w", err)
	}

	// Find wallet for the withdrawal chain
	var sourceWallet *entities.ManagedWallet
	withdrawalChain := entities.WalletChain(withdrawal.DestinationChain)
	for _, w := range wallets {
		if w.Chain == withdrawalChain {
			sourceWallet = w
			break
		}
	}

	if sourceWallet == nil {
		return "", fmt.Errorf("no wallet found for chain %s", withdrawal.DestinationChain)
	}

	// Create Circle transfer request using existing entities
	transferReq := entities.CircleTransferRequest{
		WalletID:            sourceWallet.CircleWalletID,
		DestinationAddress:  withdrawal.DestinationAddress,
		Amounts:             []string{withdrawal.Amount.String()},
		TokenID:             getUSDCTokenIDForWalletChain(withdrawalChain),
		IDempotencyKey:      withdrawal.ID.String(),
	}

	// Execute transfer via Circle API
	response, err := e.circleClient.TransferFunds(ctx, transferReq)
	if err != nil {
		return "", fmt.Errorf("circle transfer failed: %w", err)
	}

	// Extract transfer ID from response
	transferID := ""
	if id, ok := response["id"].(string); ok {
		transferID = id
	} else if data, ok := response["data"].(map[string]interface{}); ok {
		if id, ok := data["id"].(string); ok {
			transferID = id
		}
	}

	e.logger.Info("Circle transfer initiated",
		"withdrawal_id", withdrawal.ID,
		"transfer_id", transferID,
		"wallet_id", sourceWallet.CircleWalletID)

	return transferID, nil
}

// getUSDCTokenIDForWalletChain returns the USDC token ID for a given wallet chain
func getUSDCTokenIDForWalletChain(chain entities.WalletChain) string {
	// Circle USDC token IDs vary by chain
	// These are placeholder values - actual IDs should come from config
	chainFamily := chain.GetChainFamily()
	switch chainFamily {
	case "SOL":
		return "usdc-sol"
	case "MATIC":
		return "usdc-polygon"
	case "ETH":
		return "usdc-eth"
	default:
		return "usdc"
	}
}

// ProcessPendingWithdrawals processes all pending withdrawals
func (e *Engine) ProcessPendingWithdrawals(ctx context.Context) error {
	withdrawals, err := e.withdrawalRepo.GetPendingWithdrawals(ctx)
	if err != nil {
		return fmt.Errorf("failed to get pending withdrawals: %w", err)
	}

	for _, withdrawal := range withdrawals {
		if err := e.ExecuteWithdrawal(ctx, withdrawal.ID); err != nil {
			e.logger.Error("Failed to execute withdrawal",
				"withdrawal_id", withdrawal.ID,
				"error", err)
			// Continue with other withdrawals
		}
	}

	return nil
}

// ============================================================================
// BUFFER MONITORING
// ============================================================================

// CheckSystemBufferLevel checks if system USDC buffer is healthy
func (e *Engine) CheckSystemBufferLevel(ctx context.Context) (*BufferStatus, error) {
	// Get system buffer balance from ledger
	systemAccount, err := e.ledgerService.GetSystemAccount(ctx, entities.AccountTypeSystemBufferUSDC)
	if err != nil {
		return nil, fmt.Errorf("failed to get system buffer account: %w", err)
	}

	// Get actual Circle wallet balance
	// This would query all Circle wallets and sum balances
	actualBalance, err := e.getActualCircleBalance(ctx)
	if err != nil {
		e.logger.Error("Failed to get actual Circle balance", "error", err)
		// Use ledger balance as fallback
		actualBalance = systemAccount.Balance
	}

	status := &BufferStatus{
		LedgerBalance:  systemAccount.Balance,
		ActualBalance:  actualBalance,
		AlertThreshold: e.config.BufferAlertThreshold,
		IsHealthy:      actualBalance.GreaterThanOrEqual(e.config.BufferAlertThreshold),
		Discrepancy:    actualBalance.Sub(systemAccount.Balance),
	}

	// Alert if below threshold
	if !status.IsHealthy {
		e.logger.Warn("ALERT: System USDC buffer below threshold",
			"actual_balance", actualBalance,
			"alert_threshold", e.config.BufferAlertThreshold,
			"ledger_balance", systemAccount.Balance)
	}

	// Alert if significant discrepancy
	if status.Discrepancy.Abs().GreaterThan(decimal.NewFromFloat(100.0)) {
		e.logger.Warn("ALERT: Ledger-Circle balance discrepancy detected",
			"ledger_balance", systemAccount.Balance,
			"actual_balance", actualBalance,
			"discrepancy", status.Discrepancy)
	}

	return status, nil
}

// getActualCircleBalance queries Circle for actual wallet balances
func (e *Engine) getActualCircleBalance(ctx context.Context) (decimal.Decimal, error) {
	// Get all managed wallets
	wallets, err := e.managedWalletRepo.GetAll(ctx)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to get wallets: %w", err)
	}

	total := decimal.Zero

	for _, wallet := range wallets {
		// Query Circle for wallet balance
		balanceResp, err := e.circleClient.GetWalletBalances(ctx, wallet.CircleWalletID)
		if err != nil {
			e.logger.Error("Failed to get wallet balance from Circle",
				"wallet_id", wallet.CircleWalletID,
				"error", err)
			continue
		}

		// Sum USDC balances across all tokens/chains
		for _, token := range balanceResp.TokenBalances {
			if token.Token.Symbol == "USDC" {
				amount, err := decimal.NewFromString(token.Amount)
				if err != nil {
					e.logger.Error("Failed to parse balance amount",
						"amount", token.Amount,
						"error", err)
					continue
				}
				total = total.Add(amount)
			}
		}
	}

	return total, nil
}

// ============================================================================
// HELPER TYPES
// ============================================================================

// DepositRequest represents a deposit to process
type DepositRequest struct {
	UserID          uuid.UUID
	CircleWalletID  string
	Chain           entities.Chain
	TxHash          string
	Token           entities.Stablecoin
	Amount          decimal.Decimal
	FromAddress     string
}

// BufferStatus represents the status of the system USDC buffer
type BufferStatus struct {
	LedgerBalance  decimal.Decimal
	ActualBalance  decimal.Decimal
	AlertThreshold decimal.Decimal
	IsHealthy      bool
	Discrepancy    decimal.Decimal // Actual - Ledger
}

func stringPtr(s string) *string {
	return &s
}
