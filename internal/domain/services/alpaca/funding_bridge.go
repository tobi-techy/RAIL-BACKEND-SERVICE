package alpaca

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	alpacaAdapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// InstantFundingRepository interface for instant funding persistence
type InstantFundingRepository interface {
	Create(ctx context.Context, funding *entities.AlpacaInstantFunding) error
	GetPendingByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.AlpacaInstantFunding, error)
	UpdateStatus(ctx context.Context, alpacaTransferID, status string, settlementID *string, settledAt *time.Time) error
	GetByTransferID(ctx context.Context, transferID string) (*entities.AlpacaInstantFunding, error)
}

// BalanceRepository interface for balance updates
type BalanceRepository interface {
	UpdateBuyingPower(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
	Get(ctx context.Context, userID uuid.UUID) (*entities.Balance, error)
}

// FundingBridge handles Circle to Alpaca funding transfers
type FundingBridge struct {
	alpacaClient    *alpacaAdapter.Client
	fundingAdapter  *alpacaAdapter.FundingAdapter
	accountRepo     AccountRepository
	fundingRepo     InstantFundingRepository
	balanceRepo     BalanceRepository
	firmAccountNo   string // Firm account for instant funding source
	logger          *zap.Logger
}

func NewFundingBridge(
	alpacaClient *alpacaAdapter.Client,
	accountRepo AccountRepository,
	fundingRepo InstantFundingRepository,
	balanceRepo BalanceRepository,
	firmAccountNo string,
	logger *zap.Logger,
) *FundingBridge {
	return &FundingBridge{
		alpacaClient:   alpacaClient,
		fundingAdapter: alpacaAdapter.NewFundingAdapter(alpacaClient, logger),
		accountRepo:    accountRepo,
		fundingRepo:    fundingRepo,
		balanceRepo:    balanceRepo,
		firmAccountNo:  firmAccountNo,
		logger:         logger,
	}
}

// TransferFromCircleToAlpaca initiates a transfer from Circle wallet to Alpaca buying power
// This uses Alpaca's Instant Funding to provide immediate buying power
func (b *FundingBridge) TransferFromCircleToAlpaca(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	// Get user's Alpaca account
	account, err := b.accountRepo.GetByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get Alpaca account: %w", err)
	}
	if account == nil {
		return fmt.Errorf("user has no Alpaca account")
	}

	if account.Status != entities.AlpacaAccountStatusActive {
		return fmt.Errorf("Alpaca account not active: %s", account.Status)
	}

	// Create instant funding transfer
	resp, err := b.CreateInstantFunding(ctx, account.AlpacaAccountNumber, amount)
	if err != nil {
		return fmt.Errorf("create instant funding: %w", err)
	}

	// Store funding record
	now := time.Now()
	funding := &entities.AlpacaInstantFunding{
		ID:               uuid.New(),
		UserID:           userID,
		AlpacaAccountID:  &account.ID,
		AlpacaTransferID: resp.ID,
		SourceAccountNo:  b.firmAccountNo,
		Amount:           amount,
		RemainingPayable: resp.RemainingPayable,
		TotalInterest:    resp.TotalInterest,
		Status:           resp.Status,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err := b.fundingRepo.Create(ctx, funding); err != nil {
		b.logger.Error("Failed to store instant funding record", zap.Error(err))
		return fmt.Errorf("store funding record: %w", err)
	}

	// Update local buying power
	currentBalance, err := b.balanceRepo.Get(ctx, userID)
	if err != nil {
		b.logger.Warn("Failed to get current balance", zap.Error(err))
	}

	newBuyingPower := amount
	if currentBalance != nil {
		newBuyingPower = currentBalance.BuyingPower.Add(amount)
	}

	if err := b.balanceRepo.UpdateBuyingPower(ctx, userID, newBuyingPower); err != nil {
		b.logger.Error("Failed to update buying power", zap.Error(err))
	}

	b.logger.Info("Instant funding transfer created",
		zap.String("user_id", userID.String()),
		zap.String("transfer_id", resp.ID),
		zap.String("amount", amount.String()))

	return nil
}

// CreateInstantFunding creates an instant funding transfer via Alpaca API
func (b *FundingBridge) CreateInstantFunding(ctx context.Context, alpacaAccountNo string, amount decimal.Decimal) (*entities.AlpacaInstantFundingResponse, error) {
	req := &entities.AlpacaInstantFundingRequest{
		AccountNo:       alpacaAccountNo,
		SourceAccountNo: b.firmAccountNo,
		Amount:          amount,
	}

	resp, err := b.fundingAdapter.InitiateInstantFunding(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("initiate instant funding: %w", err)
	}

	return resp, nil
}

// SettleInstantFunding settles pending instant funding transfers
func (b *FundingBridge) SettleInstantFunding(ctx context.Context, transferIDs []string) error {
	if len(transferIDs) == 0 {
		return nil
	}

	// Build settlement request with transmitter info
	transfers := make([]struct {
		InstantTransferID string                 `json:"instant_transfer_id"`
		TransmitterInfo   map[string]interface{} `json:"transmitter_info"`
	}, len(transferIDs))

	for i, id := range transferIDs {
		transfers[i].InstantTransferID = id
		transfers[i].TransmitterInfo = map[string]interface{}{
			"originator_full_name":         "RAIL Platform",
			"originator_street_address":    "123 Main St",
			"originator_city":              "San Francisco",
			"originator_postal_code":       "94105",
			"originator_country":           "USA",
			"originator_bank_account_number": b.firmAccountNo,
			"originator_bank_name":         "RAIL Treasury",
		}
	}

	// Note: Actual settlement requires wire transfer to Alpaca first
	// This marks transfers as ready for settlement
	now := time.Now()
	for _, id := range transferIDs {
		if err := b.fundingRepo.UpdateStatus(ctx, id, "SETTLEMENT_PENDING", nil, nil); err != nil {
			b.logger.Error("Failed to update funding status", zap.String("transfer_id", id), zap.Error(err))
		}
	}

	b.logger.Info("Marked instant funding transfers for settlement",
		zap.Int("count", len(transferIDs)),
		zap.Time("settlement_time", now))

	return nil
}

// GetPendingFunding returns pending instant funding transfers for a user
func (b *FundingBridge) GetPendingFunding(ctx context.Context, userID uuid.UUID) ([]*entities.AlpacaInstantFunding, error) {
	return b.fundingRepo.GetPendingByUserID(ctx, userID)
}

// GetInstantFundingLimits returns the current instant funding limits
func (b *FundingBridge) GetInstantFundingLimits(ctx context.Context) (*entities.AlpacaInstantFundingLimitsResponse, error) {
	return b.fundingAdapter.GetInstantFundingLimits(ctx)
}

// ProcessFundingSettlement processes a settlement completion event
func (b *FundingBridge) ProcessFundingSettlement(ctx context.Context, transferID, settlementID string) error {
	now := time.Now()
	return b.fundingRepo.UpdateStatus(ctx, transferID, "COMPLETED", &settlementID, &now)
}

// CreateJournal creates a journal entry to transfer funds between accounts
func (b *FundingBridge) CreateJournal(ctx context.Context, fromAccount, toAccount string, amount decimal.Decimal, description string) (*entities.AlpacaJournalResponse, error) {
	req := &entities.AlpacaJournalRequest{
		FromAccount: fromAccount,
		ToAccount:   toAccount,
		EntryType:   "JNLC", // Cash journal
		Amount:      amount,
		Description: description,
		TransmitterName:                 "RAIL Platform",
		TransmitterAccountNumber:        fromAccount,
		TransmitterFinancialInstitution: "RAIL Treasury",
	}

	return b.fundingAdapter.CreateJournal(ctx, req)
}
