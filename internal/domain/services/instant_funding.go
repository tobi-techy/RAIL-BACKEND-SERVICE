package services

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// VirtualAccountRepository interface for virtual account operations
type VirtualAccountRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.VirtualAccount, error)
}

// InstantFundingService handles instant USD transfer to Alpaca accounts
type InstantFundingService struct {
	alpacaService      *alpaca.Service
	virtualAccountRepo VirtualAccountRepository
	logger             *zap.Logger
	firmAccountNumber  string
}

func NewInstantFundingService(
	alpacaService *alpaca.Service,
	virtualAccountRepo VirtualAccountRepository,
	logger *zap.Logger,
	firmAccountNumber string,
) *InstantFundingService {
	return &InstantFundingService{
		alpacaService:      alpacaService,
		virtualAccountRepo: virtualAccountRepo,
		logger:             logger,
		firmAccountNumber:  firmAccountNumber,
	}
}

// FundBrokerageAccount journals USD from firm account to user's Alpaca account
func (s *InstantFundingService) FundBrokerageAccount(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	s.logger.Info("Initiating instant brokerage funding",
		zap.String("user_id", userID.String()),
		zap.String("amount", amount.String()))

	virtualAccounts, err := s.virtualAccountRepo.GetByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get virtual account: %w", err)
	}

	if len(virtualAccounts) == 0 || virtualAccounts[0].AlpacaAccountID == "" {
		return fmt.Errorf("user has no Alpaca account")
	}

	virtualAccount := virtualAccounts[0]

	journal, err := s.alpacaService.CreateJournal(ctx, &entities.AlpacaJournalRequest{
		FromAccount: s.firmAccountNumber,
		ToAccount:   virtualAccount.AlpacaAccountID,
		EntryType:   "JNLC", // Cash journal
		Amount:      amount,
		Description: fmt.Sprintf("Stablecoin deposit funding for user %s", userID.String()),
	})
	if err != nil {
		s.logger.Error("Failed to create journal entry",
			zap.String("user_id", userID.String()),
			zap.Error(err))
		return fmt.Errorf("create journal: %w", err)
	}

	s.logger.Info("Journal created successfully",
		zap.String("journal_id", journal.ID),
		zap.String("status", journal.Status))

	s.logger.Info("Instant funding completed",
		zap.String("user_id", userID.String()),
		zap.String("amount", amount.String()),
		zap.String("journal_id", journal.ID))

	return nil
}
