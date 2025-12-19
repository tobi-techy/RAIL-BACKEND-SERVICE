package autoinvest

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/rail-service/rail_service/pkg/logger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("autoinvest-service")

// Default investment configuration
var (
	// DefaultBasketID is the Balanced Growth basket - diversified ETF portfolio
	DefaultBasketID = uuid.MustParse("123e4567-e89b-12d3-a456-426614174002")
	// MinInvestmentThreshold is the minimum stash balance to trigger auto-investment
	MinInvestmentThreshold = decimal.NewFromFloat(10.00)
)

// InvestingService interface for placing orders
type InvestingService interface {
	CreateOrder(ctx context.Context, userID uuid.UUID, req *entities.OrderCreateRequest) (*entities.Order, error)
	GetBasket(ctx context.Context, basketID uuid.UUID) (*entities.Basket, error)
}

// AllocationRepository interface for checking allocation mode
type AllocationRepository interface {
	GetMode(ctx context.Context, userID uuid.UUID) (*entities.SmartAllocationMode, error)
}

// AutoInvestRepository interface for tracking auto-investments
type AutoInvestRepository interface {
	GetUserSettings(ctx context.Context, userID uuid.UUID) (*entities.AutoInvestSettings, error)
	SaveUserSettings(ctx context.Context, settings *entities.AutoInvestSettings) error
	CreateEvent(ctx context.Context, event *entities.AutoInvestEvent) error
	GetPendingUsers(ctx context.Context, threshold decimal.Decimal) ([]uuid.UUID, error)
}

// Service handles automatic investment operations
type Service struct {
	ledgerService    *ledger.Service
	investingService InvestingService
	allocationRepo   AllocationRepository
	autoInvestRepo   AutoInvestRepository
	logger           *logger.Logger
}

// NewService creates a new auto-invest service
func NewService(
	ledgerService *ledger.Service,
	investingService InvestingService,
	allocationRepo AllocationRepository,
	autoInvestRepo AutoInvestRepository,
	logger *logger.Logger,
) *Service {
	return &Service{
		ledgerService:    ledgerService,
		investingService: investingService,
		allocationRepo:   allocationRepo,
		autoInvestRepo:   autoInvestRepo,
		logger:           logger,
	}
}

// SetInvestingService sets the investing service (to avoid circular dependency)
func (s *Service) SetInvestingService(investingService InvestingService) {
	s.investingService = investingService
}

// TriggerAutoInvestment checks stash balance and triggers investment if threshold met
func (s *Service) TriggerAutoInvestment(ctx context.Context, userID uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "autoinvest.TriggerAutoInvestment",
		trace.WithAttributes(attribute.String("user_id", userID.String())))
	defer span.End()

	// Check if user has allocation mode active
	mode, err := s.allocationRepo.GetMode(ctx, userID)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to get allocation mode: %w", err)
	}

	if mode == nil || !mode.Active {
		s.logger.Debug("Allocation mode not active, skipping auto-invest", "user_id", userID)
		return nil
	}

	// Get user's auto-invest settings (or use defaults)
	settings, err := s.getOrCreateSettings(ctx, userID)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to get auto-invest settings: %w", err)
	}

	if !settings.Enabled {
		s.logger.Debug("Auto-invest disabled for user", "user_id", userID)
		return nil
	}

	// Get stash balance
	stashBalance, err := s.ledgerService.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to get stash balance: %w", err)
	}

	span.SetAttributes(attribute.String("stash_balance", stashBalance.String()))

	// Check if threshold met
	if stashBalance.LessThan(settings.Threshold) {
		s.logger.Debug("Stash balance below threshold",
			"user_id", userID,
			"balance", stashBalance,
			"threshold", settings.Threshold)
		return nil
	}

	// Execute auto-investment
	return s.executeAutoInvestment(ctx, userID, stashBalance, settings)
}

// executeAutoInvestment performs the actual investment
func (s *Service) executeAutoInvestment(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, settings *entities.AutoInvestSettings) error {
	ctx, span := tracer.Start(ctx, "autoinvest.executeAutoInvestment",
		trace.WithAttributes(
			attribute.String("user_id", userID.String()),
			attribute.String("amount", amount.String()),
			attribute.String("basket_id", settings.BasketID.String()),
		))
	defer span.End()

	s.logger.Info("Executing auto-investment",
		"user_id", userID,
		"amount", amount,
		"basket_id", settings.BasketID)

	// Transfer from stash to buying power for investment
	if err := s.transferStashToBuyingPower(ctx, userID, amount); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to transfer stash to buying power: %w", err)
	}

	// Create investment order
	order, err := s.investingService.CreateOrder(ctx, userID, &entities.OrderCreateRequest{
		BasketID: settings.BasketID,
		Side:     entities.OrderSideBuy,
		Amount:   amount.String(),
	})
	if err != nil {
		// Rollback: transfer back to stash
		s.logger.Error("Failed to create order, rolling back", "error", err, "user_id", userID)
		_ = s.transferBuyingPowerToStash(ctx, userID, amount)
		span.RecordError(err)
		return fmt.Errorf("failed to create investment order: %w", err)
	}

	// Record auto-invest event
	event := &entities.AutoInvestEvent{
		ID:        uuid.New(),
		UserID:    userID,
		BasketID:  settings.BasketID,
		Amount:    amount,
		OrderID:   order.ID,
		Status:    entities.AutoInvestStatusCompleted,
		CreatedAt: time.Now(),
	}

	if err := s.autoInvestRepo.CreateEvent(ctx, event); err != nil {
		s.logger.Error("Failed to record auto-invest event", "error", err, "user_id", userID)
		// Don't fail - investment was successful
	}

	s.logger.Info("Auto-investment completed",
		"user_id", userID,
		"order_id", order.ID,
		"amount", amount,
		"basket_id", settings.BasketID)

	return nil
}

// transferStashToBuyingPower moves funds from stash to buying power for investment
func (s *Service) transferStashToBuyingPower(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	stashAccount, err := s.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return fmt.Errorf("failed to get stash account: %w", err)
	}

	buyingPowerAccount, err := s.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeFiatExposure)
	if err != nil {
		return fmt.Errorf("failed to get buying power account: %w", err)
	}

	desc := fmt.Sprintf("Auto-invest transfer: %s USDC from stash to buying power", amount.String())
	idempotencyKey := fmt.Sprintf("autoinvest-transfer-%s-%d", userID.String(), time.Now().UnixNano())

	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Metadata: map[string]any{
			"type":   "auto_invest_transfer",
			"amount": amount.String(),
		},
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   stashAccount.ID,
				EntryType:   entities.EntryTypeCredit, // Decrease stash
				Amount:      amount,
				Currency:    "USDC",
				Description: &desc,
			},
			{
				AccountID:   buyingPowerAccount.ID,
				EntryType:   entities.EntryTypeDebit, // Increase buying power
				Amount:      amount,
				Currency:    "USDC",
				Description: &desc,
			},
		},
	}

	_, err = s.ledgerService.CreateTransaction(ctx, req)
	return err
}

// transferBuyingPowerToStash reverses a failed auto-invest transfer
func (s *Service) transferBuyingPowerToStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	stashAccount, err := s.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return err
	}

	buyingPowerAccount, err := s.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeFiatExposure)
	if err != nil {
		return err
	}

	desc := fmt.Sprintf("Auto-invest rollback: %s USDC from buying power to stash", amount.String())
	idempotencyKey := fmt.Sprintf("autoinvest-rollback-%s-%d", userID.String(), time.Now().UnixNano())

	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   buyingPowerAccount.ID,
				EntryType:   entities.EntryTypeCredit, // Decrease buying power
				Amount:      amount,
				Currency:    "USDC",
				Description: &desc,
			},
			{
				AccountID:   stashAccount.ID,
				EntryType:   entities.EntryTypeDebit, // Increase stash
				Amount:      amount,
				Currency:    "USDC",
				Description: &desc,
			},
		},
	}

	_, err = s.ledgerService.CreateTransaction(ctx, req)
	return err
}

// getOrCreateSettings gets user settings or creates defaults
func (s *Service) getOrCreateSettings(ctx context.Context, userID uuid.UUID) (*entities.AutoInvestSettings, error) {
	settings, err := s.autoInvestRepo.GetUserSettings(ctx, userID)
	if err != nil {
		return nil, err
	}

	if settings != nil {
		return settings, nil
	}

	// Create default settings - auto-invest enabled by default per PRD
	settings = &entities.AutoInvestSettings{
		UserID:    userID,
		Enabled:   true,
		BasketID:  DefaultBasketID,
		Threshold: MinInvestmentThreshold,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.autoInvestRepo.SaveUserSettings(ctx, settings); err != nil {
		return nil, err
	}

	return settings, nil
}

// ProcessPendingAutoInvestments processes all users with stash above threshold
func (s *Service) ProcessPendingAutoInvestments(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "autoinvest.ProcessPendingAutoInvestments")
	defer span.End()

	userIDs, err := s.autoInvestRepo.GetPendingUsers(ctx, MinInvestmentThreshold)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to get pending users: %w", err)
	}

	s.logger.Info("Processing pending auto-investments", "user_count", len(userIDs))

	var lastErr error
	for _, userID := range userIDs {
		if err := s.TriggerAutoInvestment(ctx, userID); err != nil {
			s.logger.Error("Failed to process auto-investment",
				"user_id", userID,
				"error", err)
			lastErr = err
			// Continue processing other users
		}
	}

	return lastErr
}
