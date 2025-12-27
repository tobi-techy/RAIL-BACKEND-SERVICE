package card

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

var (
	ErrCardNotFound       = errors.New("card not found")
	ErrCardAlreadyExists  = errors.New("user already has an active card of this type")
	ErrCardFrozen         = errors.New("card is frozen")
	ErrCardCancelled      = errors.New("card is cancelled")
	ErrInsufficientFunds  = errors.New("insufficient spend balance")
	ErrCustomerNotFound   = errors.New("bridge customer not found")
	ErrWalletNotFound     = errors.New("wallet not found for card creation")
)

// CardRepository defines card persistence operations
type CardRepository interface {
	Create(ctx context.Context, card *entities.BridgeCard) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.BridgeCard, error)
	GetByBridgeCardID(ctx context.Context, bridgeCardID string) (*entities.BridgeCard, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.BridgeCard, error)
	GetActiveVirtualCard(ctx context.Context, userID uuid.UUID) (*entities.BridgeCard, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status entities.CardStatus) error
	CreateTransaction(ctx context.Context, tx *entities.BridgeCardTransaction) error
	GetTransactionByBridgeID(ctx context.Context, bridgeTransID string) (*entities.BridgeCardTransaction, error)
	GetTransactionsByCardID(ctx context.Context, cardID uuid.UUID, limit, offset int) ([]*entities.BridgeCardTransaction, error)
	GetTransactionsByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.BridgeCardTransaction, error)
	UpdateTransactionStatus(ctx context.Context, id uuid.UUID, status string, declineReason *string) error
	CountByUserID(ctx context.Context, userID uuid.UUID) (int, error)
}

// UserProfileProvider provides user profile data
type UserProfileProvider interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
}

// WalletProvider provides wallet data
type WalletProvider interface {
	GetUserWalletByChain(ctx context.Context, userID uuid.UUID, chain string) (*entities.ManagedWallet, error)
}

// BalanceProvider provides balance checking
type BalanceProvider interface {
	GetSpendBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
	DeductSpendBalance(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reference string) error
}

// LedgerService provides ledger operations for card transactions
type LedgerService interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
	GetOrCreateUserAccount(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (*entities.LedgerAccount, error)
	CreateTransaction(ctx context.Context, req *entities.CreateTransactionRequest) (*entities.LedgerTransaction, error)
}

// Service handles card business logic
type Service struct {
	repo            CardRepository
	bridgeAdapter   *bridge.Adapter
	userProvider    UserProfileProvider
	walletProvider  WalletProvider
	balanceProvider BalanceProvider
	ledgerService   LedgerService
	logger          *zap.Logger
	defaultChain    string
}

// NewService creates a new card service
func NewService(
	repo CardRepository,
	bridgeAdapter *bridge.Adapter,
	userProvider UserProfileProvider,
	walletProvider WalletProvider,
	balanceProvider BalanceProvider,
	logger *zap.Logger,
) *Service {
	return &Service{
		repo:            repo,
		bridgeAdapter:   bridgeAdapter,
		userProvider:    userProvider,
		walletProvider:  walletProvider,
		balanceProvider: balanceProvider,
		logger:          logger,
		defaultChain:    "ethereum", // Default chain for card funding
	}
}

// SetLedgerService sets the ledger service after initialization.
// This is used to break circular dependencies in the DI container.
func (s *Service) SetLedgerService(ledgerService LedgerService) {
	s.ledgerService = ledgerService
}

// CreateVirtualCard creates a virtual card for a user on first funding
func (s *Service) CreateVirtualCard(ctx context.Context, userID uuid.UUID) (*entities.BridgeCard, error) {
	s.logger.Info("Creating virtual card", zap.String("user_id", userID.String()))

	// Check if user already has an active virtual card
	existing, err := s.repo.GetActiveVirtualCard(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing card: %w", err)
	}
	if existing != nil {
		return existing, nil // Return existing card
	}

	// Get user profile for Bridge customer ID
	profile, err := s.userProvider.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user profile: %w", err)
	}
	if profile.BridgeCustomerID == nil || *profile.BridgeCustomerID == "" {
		return nil, ErrCustomerNotFound
	}

	// Get user's wallet for card funding
	wallet, err := s.walletProvider.GetUserWalletByChain(ctx, userID, s.defaultChain)
	if err != nil || wallet == nil {
		return nil, ErrWalletNotFound
	}

	// Create card account on Bridge
	bridgeReq := &bridge.CreateCardAccountRequest{
		Currency: bridge.CurrencyUSDC,
		Chain:    bridge.PaymentRailEthereum,
		CryptoAccount: bridge.CryptoAccount{
			Type:    "wallet",
			Address: wallet.Address,
		},
	}

	bridgeCard, err := s.bridgeAdapter.CreateCardAccountForCustomer(ctx, *profile.BridgeCustomerID, bridgeReq)
	if err != nil {
		s.logger.Error("Failed to create Bridge card account", zap.Error(err))
		return nil, fmt.Errorf("failed to create card on Bridge: %w", err)
	}

	// Create local card record
	card := &entities.BridgeCard{
		ID:               uuid.New(),
		UserID:           userID,
		BridgeCardID:     bridgeCard.ID,
		BridgeCustomerID: *profile.BridgeCustomerID,
		Type:             entities.CardTypeVirtual,
		Status:           mapBridgeCardStatus(bridgeCard.Status),
		Last4:            bridgeCard.CardDetails.Last4,
		Expiry:           bridgeCard.CardDetails.Expiry,
		CardImageURL:     nilIfEmpty(bridgeCard.CardImageURL),
		Currency:         string(bridgeCard.Currency),
		Chain:            string(bridgeCard.Chain),
		WalletAddress:    wallet.Address,
	}

	if err := s.repo.Create(ctx, card); err != nil {
		return nil, fmt.Errorf("failed to save card: %w", err)
	}

	s.logger.Info("Virtual card created",
		zap.String("card_id", card.ID.String()),
		zap.String("bridge_card_id", card.BridgeCardID))

	return card, nil
}

// GetUserCards retrieves all cards for a user
func (s *Service) GetUserCards(ctx context.Context, userID uuid.UUID) ([]*entities.BridgeCard, error) {
	return s.repo.GetByUserID(ctx, userID)
}

// GetCard retrieves a specific card
func (s *Service) GetCard(ctx context.Context, userID, cardID uuid.UUID) (*entities.BridgeCard, error) {
	card, err := s.repo.GetByID(ctx, cardID)
	if err != nil {
		return nil, err
	}
	if card == nil || card.UserID != userID {
		return nil, ErrCardNotFound
	}
	return card, nil
}

// FreezeCard freezes a card
func (s *Service) FreezeCard(ctx context.Context, userID, cardID uuid.UUID) (*entities.BridgeCard, error) {
	card, err := s.GetCard(ctx, userID, cardID)
	if err != nil {
		return nil, err
	}

	if card.Status == entities.CardStatusCancelled {
		return nil, ErrCardCancelled
	}
	if card.Status == entities.CardStatusFrozen {
		return card, nil // Already frozen
	}

	// Freeze on Bridge
	_, err = s.bridgeAdapter.Client().FreezeCardAccount(ctx, card.BridgeCustomerID, card.BridgeCardID)
	if err != nil {
		s.logger.Error("Failed to freeze card on Bridge", zap.Error(err))
		return nil, fmt.Errorf("failed to freeze card: %w", err)
	}

	// Update local status
	if err := s.repo.UpdateStatus(ctx, cardID, entities.CardStatusFrozen); err != nil {
		return nil, err
	}

	card.Status = entities.CardStatusFrozen
	s.logger.Info("Card frozen", zap.String("card_id", cardID.String()))
	return card, nil
}

// UnfreezeCard unfreezes a card
func (s *Service) UnfreezeCard(ctx context.Context, userID, cardID uuid.UUID) (*entities.BridgeCard, error) {
	card, err := s.GetCard(ctx, userID, cardID)
	if err != nil {
		return nil, err
	}

	if card.Status == entities.CardStatusCancelled {
		return nil, ErrCardCancelled
	}
	if card.Status == entities.CardStatusActive {
		return card, nil // Already active
	}

	// Unfreeze on Bridge
	_, err = s.bridgeAdapter.Client().UnfreezeCardAccount(ctx, card.BridgeCustomerID, card.BridgeCardID)
	if err != nil {
		s.logger.Error("Failed to unfreeze card on Bridge", zap.Error(err))
		return nil, fmt.Errorf("failed to unfreeze card: %w", err)
	}

	// Update local status
	if err := s.repo.UpdateStatus(ctx, cardID, entities.CardStatusActive); err != nil {
		return nil, err
	}

	card.Status = entities.CardStatusActive
	s.logger.Info("Card unfrozen", zap.String("card_id", cardID.String()))
	return card, nil
}

// GetCardTransactions retrieves transactions for a card
func (s *Service) GetCardTransactions(ctx context.Context, userID, cardID uuid.UUID, limit, offset int) ([]*entities.BridgeCardTransaction, error) {
	// Verify card ownership
	card, err := s.GetCard(ctx, userID, cardID)
	if err != nil {
		return nil, err
	}

	return s.repo.GetTransactionsByCardID(ctx, card.ID, limit, offset)
}

// GetUserTransactions retrieves all card transactions for a user
func (s *Service) GetUserTransactions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.BridgeCardTransaction, error) {
	return s.repo.GetTransactionsByUserID(ctx, userID, limit, offset)
}

// ProcessCardAuthorization handles real-time card authorization
func (s *Service) ProcessCardAuthorization(ctx context.Context, bridgeCardID string, amount decimal.Decimal, merchantName, merchantCategory string) (bool, string, error) {
	s.logger.Info("Processing card authorization",
		zap.String("bridge_card_id", bridgeCardID),
		zap.String("amount", amount.String()))

	// Get card
	card, err := s.repo.GetByBridgeCardID(ctx, bridgeCardID)
	if err != nil || card == nil {
		return false, "card_not_found", ErrCardNotFound
	}

	// Check card status
	if card.Status == entities.CardStatusFrozen {
		return false, "card_frozen", ErrCardFrozen
	}
	if card.Status == entities.CardStatusCancelled {
		return false, "card_cancelled", ErrCardCancelled
	}

	// Check spend balance
	balance, err := s.balanceProvider.GetSpendBalance(ctx, card.UserID)
	if err != nil {
		s.logger.Error("Failed to get spend balance", zap.Error(err))
		return false, "balance_check_failed", err
	}

	if balance.LessThan(amount) {
		return false, "insufficient_funds", ErrInsufficientFunds
	}

	s.logger.Info("Card authorization approved",
		zap.String("card_id", card.ID.String()),
		zap.String("amount", amount.String()))

	return true, "", nil
}

// RecordTransaction records a card transaction from webhook
func (s *Service) RecordTransaction(ctx context.Context, bridgeCardID, bridgeTransID, txType string, amount decimal.Decimal, merchantName, merchantCategory, status string, declineReason *string) error {
	card, err := s.repo.GetByBridgeCardID(ctx, bridgeCardID)
	if err != nil || card == nil {
		return ErrCardNotFound
	}

	// Check for duplicate
	existing, _ := s.repo.GetTransactionByBridgeID(ctx, bridgeTransID)
	if existing != nil {
		// Update status if changed
		if existing.Status != status {
			// If transitioning to completed, deduct from spend balance
			if status == "completed" && existing.Status != "completed" {
				if err := s.settleTransaction(ctx, card.UserID, amount, bridgeTransID, merchantName); err != nil {
					s.logger.Error("Failed to settle card transaction",
						zap.String("transaction_id", bridgeTransID),
						zap.Error(err))
					return err
				}
			}
			return s.repo.UpdateTransactionStatus(ctx, existing.ID, status, declineReason)
		}
		return nil
	}

	tx := &entities.BridgeCardTransaction{
		CardID:           card.ID,
		UserID:           card.UserID,
		BridgeTransID:    bridgeTransID,
		Type:             txType,
		Amount:           amount,
		Currency:         card.Currency,
		MerchantName:     nilIfEmpty(merchantName),
		MerchantCategory: nilIfEmpty(merchantCategory),
		Status:           status,
		DeclineReason:    declineReason,
	}

	if err := s.repo.CreateTransaction(ctx, tx); err != nil {
		return err
	}

	// If transaction is already completed (captured), deduct from spend balance
	if status == "completed" {
		if err := s.settleTransaction(ctx, card.UserID, amount, bridgeTransID, merchantName); err != nil {
			s.logger.Error("Failed to settle card transaction",
				zap.String("transaction_id", bridgeTransID),
				zap.Error(err))
			return err
		}
	}

	return nil
}

// settleTransaction deducts from spend balance and creates ledger entry
func (s *Service) settleTransaction(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, transactionID, merchantName string) error {
	s.logger.Info("Settling card transaction",
		zap.String("user_id", userID.String()),
		zap.String("amount", amount.String()),
		zap.String("transaction_id", transactionID))

	// Deduct from spend balance via balance provider
	if s.balanceProvider != nil {
		if err := s.balanceProvider.DeductSpendBalance(ctx, userID, amount, transactionID); err != nil {
			return fmt.Errorf("failed to deduct spend balance: %w", err)
		}
	}

	// Create ledger entry if ledger service is available
	if s.ledgerService != nil {
		if err := s.createCardTransactionLedgerEntry(ctx, userID, amount, transactionID, merchantName); err != nil {
			s.logger.Error("Failed to create ledger entry for card transaction",
				zap.String("transaction_id", transactionID),
				zap.Error(err))
			// Don't fail the transaction if ledger entry fails - balance already deducted
		}
	}

	return nil
}

// createCardTransactionLedgerEntry creates a ledger entry for a card transaction
func (s *Service) createCardTransactionLedgerEntry(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, transactionID, merchantName string) error {
	spendAccount, err := s.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("failed to get spend account: %w", err)
	}

	desc := fmt.Sprintf("Card transaction: %s", merchantName)
	if merchantName == "" {
		desc = fmt.Sprintf("Card transaction: %s", transactionID)
	}
	idempotencyKey := fmt.Sprintf("card-tx:%s", transactionID)

	txReq := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeCardPayment,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   spendAccount.ID,
				EntryType:   entities.EntryTypeCredit, // Decrease spend balance
				Amount:      amount,
				Currency:    "USD",
				Description: &desc,
			},
		},
	}

	_, err = s.ledgerService.CreateTransaction(ctx, txReq)
	return err
}

// ProcessAuthorization implements BridgeCardProcessor interface for webhook handling
func (s *Service) ProcessAuthorization(ctx context.Context, cardID string, amount decimal.Decimal, merchantName, merchantCategory string) error {
	approved, declineReason, err := s.ProcessCardAuthorization(ctx, cardID, amount, merchantName, merchantCategory)
	if err != nil {
		return err
	}
	if !approved {
		s.logger.Warn("Card authorization declined",
			zap.String("card_id", cardID),
			zap.String("reason", declineReason))
	}
	return nil
}

// RecordDeclinedTransaction implements BridgeCardProcessor interface for webhook handling
func (s *Service) RecordDeclinedTransaction(ctx context.Context, cardID, transactionID, declineReason string) error {
	reason := declineReason
	return s.RecordTransaction(ctx, cardID, transactionID, "authorization", decimal.Zero, "", "", "declined", &reason)
}

// SyncCardStatus syncs card status from Bridge
func (s *Service) SyncCardStatus(ctx context.Context, bridgeCardID string) error {
	card, err := s.repo.GetByBridgeCardID(ctx, bridgeCardID)
	if err != nil || card == nil {
		return ErrCardNotFound
	}

	bridgeCard, err := s.bridgeAdapter.Client().GetCardAccount(ctx, card.BridgeCustomerID, bridgeCardID)
	if err != nil {
		return fmt.Errorf("failed to get card from Bridge: %w", err)
	}

	newStatus := mapBridgeCardStatus(bridgeCard.Status)
	if card.Status != newStatus {
		return s.repo.UpdateStatus(ctx, card.ID, newStatus)
	}

	return nil
}

// Helper functions

func mapBridgeCardStatus(status bridge.CardAccountStatus) entities.CardStatus {
	switch status {
	case bridge.CardAccountStatusActive:
		return entities.CardStatusActive
	case bridge.CardAccountStatusFrozen:
		return entities.CardStatusFrozen
	case bridge.CardAccountStatusCancelled:
		return entities.CardStatusCancelled
	default:
		return entities.CardStatusPending
	}
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
