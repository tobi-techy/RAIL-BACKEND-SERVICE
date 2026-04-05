package onchain

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/shopspring/decimal"

	"github.com/rail-service/rail_service/pkg/logger"
)

// AllocationService defines the interface for allocation operations
type AllocationService interface {
	GetMode(ctx context.Context, userID uuid.UUID) (*entities.SmartAllocationMode, error)
	ProcessIncomingFunds(ctx context.Context, req *entities.IncomingFundsRequest) error
}

// Engine handles all blockchain and Bridge wallet interactions
type Engine struct {
	ledgerService     *ledger.Service
	allocationService AllocationService
	bridgeAdapter     *bridge.Adapter
	depositRepo       DepositRepository
	withdrawalRepo    WithdrawalRepository
	walletRepo        WalletRepository
	managedWalletRepo ManagedWalletRepository
	logger            *logger.Logger
	config            *EngineConfig
}

// EngineConfig holds onchain engine configuration
type EngineConfig struct {
	// Deposit monitoring
	DepositPollInterval time.Duration
	ConfirmationBlocks  map[entities.Chain]int
	MinDepositAmount    decimal.Decimal

	// Withdrawal execution
	WithdrawalGasBuffer     decimal.Decimal // Extra gas to ensure tx success
	WithdrawalRetryAttempts int
	WithdrawalTimeout       time.Duration

	// Buffer monitoring
	BufferCheckInterval  time.Duration
	BufferAlertThreshold decimal.Decimal // Alert if USDC buffer below this
}

// DepositRepository handles deposit persistence
type DepositRepository interface {
	Create(ctx context.Context, deposit *entities.Deposit) error
	GetByTxHash(ctx context.Context, txHash string) (*entities.Deposit, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, confirmedAt *time.Time) error
	GetPendingDeposits(ctx context.Context) ([]*entities.Deposit, error)
	DeletePendingDeposit(ctx context.Context, id uuid.UUID) error
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
	GetByBridgeWalletID(ctx context.Context, bridgeWalletID string) (*entities.ManagedWallet, error)
	GetAll(ctx context.Context) ([]*entities.ManagedWallet, error)
}

// NewEngine creates a new onchain engine
func NewEngine(
	ledgerService *ledger.Service,
	allocationService AllocationService,
	bridgeAdapter *bridge.Adapter,
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
		bridgeAdapter:     bridgeAdapter,
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
		DepositPollInterval: 30 * time.Second,
		ConfirmationBlocks: map[entities.Chain]int{
			entities.ChainSOL:   32,
			entities.ChainMATIC: 128,
		},
		MinDepositAmount:        decimal.NewFromFloat(1.0),   // $1 minimum
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
// This is called when Bridge webhook notifies us of an incoming transfer
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
	managedWallet, err := e.managedWalletRepo.GetByBridgeWalletID(ctx, req.BridgeWalletID)
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
		ID:        uuid.New(),
		UserID:    req.UserID,
		Chain:     req.Chain,
		TxHash:    req.TxHash,
		Token:     req.Token,
		Amount:    req.Amount,
		Status:    "pending",
		CreatedAt: now,
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

// MonitorDeposits polls Bridge for new deposits (fallback if webhooks fail)
func (e *Engine) MonitorDeposits(ctx context.Context) error {
	wallets, err := e.managedWalletRepo.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("failed to get managed wallets: %w", err)
	}

	for _, wallet := range wallets {
		e.logger.Debug("Monitoring wallet for deposits",
			"wallet_id", wallet.BridgeWalletID,
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

	// Only process crypto withdrawals in onchain engine
	if withdrawal.WithdrawalType != entities.WithdrawalTypeCrypto {
		return fmt.Errorf("onchain engine only handles crypto withdrawals, got: %s", withdrawal.WithdrawalType)
	}

	destAddr := ""
	if withdrawal.DestinationAddress != nil {
		destAddr = *withdrawal.DestinationAddress
	}

	e.logger.Info("Executing withdrawal",
		"withdrawal_id", withdrawalID,
		"user_id", withdrawal.UserID,
		"amount", withdrawal.Amount,
		"destination", destAddr)

	// Check if already processed
	if withdrawal.Status != entities.WithdrawalStatusPending {
		e.logger.Warn("Withdrawal not in pending status",
			"withdrawal_id", withdrawalID,
			"status", withdrawal.Status)
		return fmt.Errorf("withdrawal not pending: status=%s", withdrawal.Status)
	}

	// Balance pre-check removed: CreateTransaction uses SELECT FOR UPDATE internally,
	// which atomically checks and prevents overdraft. A pre-flight check here was a
	// TOCTOU race — balance could change between the check and the ledger debit.
	// The advisory log below helps with debugging but does not block the withdrawal.
	balance, err := e.ledgerService.GetAccountBalance(ctx, withdrawal.UserID, entities.AccountTypeUSDCBalance)
	if err != nil {
		e.logger.Warn("Advisory balance check failed, proceeding to atomic ledger debit",
			"withdrawal_id", withdrawalID, "error", err)
	} else if balance.LessThan(withdrawal.Amount) {
		e.logger.Warn("Advisory: balance may be insufficient, atomic ledger debit will be authoritative",
			"withdrawal_id", withdrawalID, "balance", balance, "requested", withdrawal.Amount)
	}

	// Post ledger entries first (debit user balance, credit system buffer)
	if err := e.postWithdrawalLedgerEntries(ctx, withdrawal); err != nil {
		return fmt.Errorf("failed to post ledger entries: %w", err)
	}

	// Execute on-chain transfer via Bridge
	txHash, err := e.executeBridgeTransfer(ctx, withdrawal)
	if err != nil {
		e.logger.Error("Failed to execute Bridge transfer",
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
	destAddr := ""
	if withdrawal.DestinationAddress != nil {
		destAddr = *withdrawal.DestinationAddress
	}
	desc := fmt.Sprintf("Withdrawal: %s USDC to %s",
		withdrawal.Amount.String(), destAddr)

	metadata := map[string]interface{}{
		"withdrawal_id":       withdrawal.ID.String(),
		"destination_address": destAddr,
		"withdrawal_type":     string(withdrawal.WithdrawalType),
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

// executeBridgeTransfer executes the actual on-chain transfer via Bridge
func (e *Engine) executeBridgeTransfer(ctx context.Context, withdrawal *entities.Withdrawal) (string, error) {
	if withdrawal.BridgeWalletID == nil || *withdrawal.BridgeWalletID == "" {
		return "", fmt.Errorf("bridge wallet ID not set on withdrawal")
	}

	destAddr := ""
	if withdrawal.DestinationAddress != nil {
		destAddr = *withdrawal.DestinationAddress
	}
	if destAddr == "" {
		return "", fmt.Errorf("destination address not set on withdrawal")
	}

	transfer, err := e.bridgeAdapter.TransferFunds(ctx, &bridge.CreateTransferRequest{
		OnBehalfOf: withdrawal.UserID.String(),
		Amount:     withdrawal.Amount.String(),
		Source: bridge.TransferSource{
			PaymentRail:    bridge.PaymentRail("bridge_wallet"),
			Currency:       bridge.CurrencyUSDC,
			BridgeWalletID: *withdrawal.BridgeWalletID,
		},
		Destination: bridge.TransferDestination{
			PaymentRail: mapChainToPaymentRail(withdrawal.DestinationChain),
			Currency:    bridge.CurrencyUSDC,
			ToAddress:   destAddr,
		},
	})
	if err != nil {
		return "", fmt.Errorf("bridge transfer failed: %w", err)
	}

	e.logger.Info("Bridge transfer initiated",
		"withdrawal_id", withdrawal.ID,
		"transfer_id", transfer.ID,
		"wallet_id", *withdrawal.BridgeWalletID)

	return transfer.ID, nil
}

// mapChainToPaymentRail maps a withdrawal destination chain to a Bridge PaymentRail
func mapChainToPaymentRail(chain string) bridge.PaymentRail {
	switch chain {
	case "solana", "SOL", "SOL-DEVNET":
		return bridge.PaymentRailSolana
	case "polygon", "MATIC", "MATIC-AMOY":
		return bridge.PaymentRailPolygon
	case "base", "BASE", "BASE-SEPOLIA":
		return bridge.PaymentRailBase
	case "avalanche", "AVAX", "AVAX-FUJI":
		return bridge.PaymentRailAvalanche
	case "ethereum", "ETH":
		return bridge.PaymentRailEthereum
	case "arbitrum":
		return bridge.PaymentRailArbitrum
	default:
		return bridge.PaymentRailBase
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

	// Get actual Bridge wallet balance
	actualBalance, err := e.getActualBridgeBalance(ctx)
	if err != nil {
		e.logger.Error("Failed to get actual Bridge balance", "error", err)
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
		e.logger.Warn("ALERT: Ledger-Bridge balance discrepancy detected",
			"ledger_balance", systemAccount.Balance,
			"actual_balance", actualBalance,
			"discrepancy", status.Discrepancy)
	}

	return status, nil
}

// getActualBridgeBalance queries Bridge for actual wallet balances
func (e *Engine) getActualBridgeBalance(ctx context.Context) (decimal.Decimal, error) {
	wallets, err := e.managedWalletRepo.GetAll(ctx)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to get wallets: %w", err)
	}

	total := decimal.Zero

	for _, wallet := range wallets {
		if wallet.BridgeWalletID == "" {
			continue
		}
		// BridgeCustomerID is stored on the user profile; we use wallet.UserID as a proxy key
		balance, err := e.bridgeAdapter.GetWalletBalance(ctx, wallet.UserID.String(), wallet.BridgeWalletID)
		if err != nil {
			e.logger.Error("Failed to get wallet balance from Bridge",
				"wallet_id", wallet.BridgeWalletID,
				"error", err)
			continue
		}

		amount, err := decimal.NewFromString(balance.GetUSDCAmount())
		if err != nil {
			continue
		}
		total = total.Add(amount)
	}

	return total, nil
}

// ============================================================================
// HELPER TYPES
// ============================================================================

// DepositRequest represents a deposit to process
type DepositRequest struct {
	UserID         uuid.UUID
	BridgeWalletID string
	Chain          entities.Chain
	TxHash         string
	Token          entities.Stablecoin
	Amount         decimal.Decimal
	FromAddress    string
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
