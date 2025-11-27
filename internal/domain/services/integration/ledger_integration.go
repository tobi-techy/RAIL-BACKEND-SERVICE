package integration

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/internal/domain/services/ledger"
	"github.com/stack-service/stack_service/pkg/logger"
)

// LedgerIntegration provides a facade for legacy services to integrate with ledger
// It supports shadow mode where writes go to both ledger and legacy tables
type LedgerIntegration struct {
	ledgerService  *ledger.Service
	balanceRepo    BalanceRepository
	logger         *logger.Logger
	shadowMode     bool // If true, dual-write to both ledger and balances table
	strictMode     bool // If true, fail on discrepancies
}

// BalanceRepository represents the legacy balance repository interface
type BalanceRepository interface {
	Get(ctx context.Context, userID uuid.UUID) (*entities.Balance, error)
	UpdateBuyingPower(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
	UpdatePendingDeposits(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
}

// NewLedgerIntegration creates a new ledger integration helper
func NewLedgerIntegration(
	ledgerService *ledger.Service,
	balanceRepo BalanceRepository,
	logger *logger.Logger,
	shadowMode bool,
	strictMode bool,
) *LedgerIntegration {
	return &LedgerIntegration{
		ledgerService: ledgerService,
		balanceRepo:   balanceRepo,
		logger:        logger,
		shadowMode:    shadowMode,
		strictMode:    strictMode,
	}
}

// GetUserBalance retrieves user balance from ledger (or legacy in shadow mode)
func (i *LedgerIntegration) GetUserBalance(ctx context.Context, userID uuid.UUID) (*UserBalanceView, error) {
	// Get from ledger
	ledgerBalances, err := i.ledgerService.GetUserBalances(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get ledger balances: %w", err)
	}

	view := &UserBalanceView{
		UserID:             userID,
		USDCBalance:        ledgerBalances.USDCBalance,
		FiatExposure:       ledgerBalances.FiatExposure,
		PendingInvestment:  ledgerBalances.PendingInvestment,
		TotalValue:         ledgerBalances.TotalValue(),
	}

	// In shadow mode, compare with legacy balance
	if i.shadowMode {
		legacyBalance, err := i.balanceRepo.Get(ctx, userID)
		if err != nil {
			i.logger.Warn("Shadow mode: failed to get legacy balance",
				"user_id", userID,
				"error", err)
		} else {
			// Compare balances
			discrepancies := i.compareBalances(ledgerBalances, legacyBalance)
			if len(discrepancies) > 0 {
				i.logger.Warn("Shadow mode: balance discrepancies detected",
					"user_id", userID,
					"discrepancies", discrepancies)

				if i.strictMode {
					return nil, fmt.Errorf("balance discrepancy detected: %v", discrepancies)
				}
			}
		}
	}

	return view, nil
}

// CreditUserUSDC credits USDC to user's balance (e.g., deposit)
// Debits from system buffer, Credits to user
func (i *LedgerIntegration) CreditUserUSDC(
	ctx context.Context,
	userID uuid.UUID,
	amount decimal.Decimal,
	description string,
	referenceID *uuid.UUID,
	referenceType string,
) error {
	i.logger.Info("Crediting user USDC",
		"user_id", userID,
		"amount", amount,
		"description", description)

	// Get accounts
	userAccount, err := i.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeUSDCBalance)
	if err != nil {
		return fmt.Errorf("failed to get user account: %w", err)
	}

	systemAccount, err := i.ledgerService.GetSystemAccount(ctx, entities.AccountTypeSystemBufferUSDC)
	if err != nil {
		return fmt.Errorf("failed to get system account: %w", err)
	}

	// Create ledger transaction
	idempotencyKey := fmt.Sprintf("credit-usdc-%s-%d", userID.String(), amount.IntPart())
	if referenceID != nil {
		idempotencyKey = fmt.Sprintf("credit-usdc-%s", referenceID.String())
	}

	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeDeposit,
		ReferenceID:     referenceID,
		ReferenceType:   &referenceType,
		IdempotencyKey:  idempotencyKey,
		Description:     &description,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   userAccount.ID,
				EntryType:   entities.EntryTypeDebit, // Increase user balance
				Amount:      amount,
				Currency:    "USDC",
				Description: &description,
			},
			{
				AccountID:   systemAccount.ID,
				EntryType:   entities.EntryTypeCredit, // Decrease system buffer
				Amount:      amount,
				Currency:    "USDC",
				Description: &description,
			},
		},
	}

	_, err = i.ledgerService.CreateTransaction(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to create ledger transaction: %w", err)
	}

	// Shadow mode: also update legacy balance
	if i.shadowMode {
		if err := i.balanceRepo.UpdatePendingDeposits(ctx, userID, amount); err != nil {
			i.logger.Error("Shadow mode: failed to update legacy balance",
				"user_id", userID,
				"error", err)
			if i.strictMode {
				return fmt.Errorf("shadow mode legacy update failed: %w", err)
			}
		}
	}

	return nil
}

// MoveFundsToFiatExposure moves USDC to fiat exposure (after broker funding)
// Credits usdc_balance, Debits fiat_exposure
func (i *LedgerIntegration) MoveFundsToFiatExposure(
	ctx context.Context,
	userID uuid.UUID,
	amount decimal.Decimal,
	description string,
	referenceID *uuid.UUID,
) error {
	i.logger.Info("Moving funds to fiat exposure",
		"user_id", userID,
		"amount", amount)

	// Get accounts
	usdcAccount, err := i.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeUSDCBalance)
	if err != nil {
		return fmt.Errorf("failed to get usdc account: %w", err)
	}

	fiatAccount, err := i.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeFiatExposure)
	if err != nil {
		return fmt.Errorf("failed to get fiat account: %w", err)
	}

	// Create ledger transaction
	idempotencyKey := fmt.Sprintf("move-fiat-%s", referenceID.String())
	
	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeConversion,
		ReferenceID:     referenceID,
		IdempotencyKey:  idempotencyKey,
		Description:     &description,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   usdcAccount.ID,
				EntryType:   entities.EntryTypeCredit, // Decrease USDC
				Amount:      amount,
				Currency:    "USDC",
				Description: &description,
			},
			{
				AccountID:   fiatAccount.ID,
				EntryType:   entities.EntryTypeDebit, // Increase fiat exposure
				Amount:      amount,
				Currency:    "USD",
				Description: &description,
			},
		},
	}

	_, err = i.ledgerService.CreateTransaction(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to create ledger transaction: %w", err)
	}

	// Shadow mode: update legacy buying power
	if i.shadowMode {
		if err := i.balanceRepo.UpdateBuyingPower(ctx, userID, amount); err != nil {
			i.logger.Error("Shadow mode: failed to update legacy buying power",
				"user_id", userID,
				"error", err)
			if i.strictMode {
				return fmt.Errorf("shadow mode legacy update failed: %w", err)
			}
		}
	}

	return nil
}

// ReserveForInvestment reserves funds for investment
func (i *LedgerIntegration) ReserveForInvestment(
	ctx context.Context,
	userID uuid.UUID,
	amount decimal.Decimal,
) error {
	return i.ledgerService.ReserveForInvestment(ctx, userID, amount)
}

// ReleaseReservation releases reserved funds
func (i *LedgerIntegration) ReleaseReservation(
	ctx context.Context,
	userID uuid.UUID,
	amount decimal.Decimal,
) error {
	return i.ledgerService.ReleaseReservation(ctx, userID, amount)
}

// ExecuteInvestment executes investment (moves from pending to fiat exposure)
func (i *LedgerIntegration) ExecuteInvestment(
	ctx context.Context,
	userID uuid.UUID,
	amount decimal.Decimal,
	orderID uuid.UUID,
) error {
	i.logger.Info("Executing investment",
		"user_id", userID,
		"amount", amount,
		"order_id", orderID)

	// Get accounts
	pendingAccount, err := i.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypePendingInvestment)
	if err != nil {
		return fmt.Errorf("failed to get pending account: %w", err)
	}

	fiatAccount, err := i.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeFiatExposure)
	if err != nil {
		return fmt.Errorf("failed to get fiat account: %w", err)
	}

	// Create ledger transaction
	desc := fmt.Sprintf("Investment executed: Order %s", orderID.String())
	idempotencyKey := fmt.Sprintf("invest-%s", orderID.String())

	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInvestment,
		ReferenceID:     &orderID,
		ReferenceType:   stringPtr("order"),
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   pendingAccount.ID,
				EntryType:   entities.EntryTypeCredit, // Decrease pending
				Amount:      amount,
				Currency:    "USDC",
				Description: &desc,
			},
			{
				AccountID:   fiatAccount.ID,
				EntryType:   entities.EntryTypeDebit, // Increase fiat exposure
				Amount:      amount,
				Currency:    "USD",
				Description: &desc,
			},
		},
	}

	_, err = i.ledgerService.CreateTransaction(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to create ledger transaction: %w", err)
	}

	return nil
}

// compareBalances compares ledger balances with legacy balances
func (i *LedgerIntegration) compareBalances(
	ledger *entities.UserBalances,
	legacy *entities.Balance,
) []string {
	var discrepancies []string

	// Compare buying power (fiat exposure)
	if !ledger.FiatExposure.Equal(legacy.BuyingPower) {
		discrepancies = append(discrepancies,
			fmt.Sprintf("fiat_exposure: ledger=%s legacy=%s diff=%s",
				ledger.FiatExposure,
				legacy.BuyingPower,
				ledger.FiatExposure.Sub(legacy.BuyingPower)))
	}

	// Compare pending deposits (usdc balance + pending)
	legacyTotal := legacy.PendingDeposits
	ledgerTotal := ledger.USDCBalance.Add(ledger.PendingInvestment)
	
	if !ledgerTotal.Equal(legacyTotal) {
		discrepancies = append(discrepancies,
			fmt.Sprintf("total_usdc: ledger=%s legacy=%s diff=%s",
				ledgerTotal,
				legacyTotal,
				ledgerTotal.Sub(legacyTotal)))
	}

	return discrepancies
}

// UserBalanceView represents a user's balance from the ledger
type UserBalanceView struct {
	UserID            uuid.UUID
	USDCBalance       decimal.Decimal // Available USDC
	FiatExposure      decimal.Decimal // USD buying power at broker
	PendingInvestment decimal.Decimal // Reserved for in-flight trades
	TotalValue        decimal.Decimal // Total across all accounts
}

func stringPtr(s string) *string {
	return &s
}
