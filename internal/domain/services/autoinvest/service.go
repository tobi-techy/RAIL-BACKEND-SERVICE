package autoinvest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("autoinvest-service")

// Config holds configuration for auto-investment
type Config struct {
	// MinThreshold is the minimum stash balance to trigger auto-investment
	MinThreshold decimal.Decimal
	// DefaultBasketID is the default basket for auto-investment
	DefaultBasketID *uuid.UUID
}

// LedgerService defines ledger operations for balance queries and transfers
type LedgerService interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
	CreateTransaction(ctx context.Context, req *entities.CreateTransactionRequest) (*entities.LedgerTransaction, error)
	GetOrCreateUserAccount(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (*entities.LedgerAccount, error)
}

// OrderPlacer defines order placement operations
type OrderPlacer interface {
	PlaceMarketOrder(ctx context.Context, userID uuid.UUID, symbol string, amount decimal.Decimal) (*entities.AlpacaOrderResponse, error)
}

// Service handles automatic investment from stash balance
type Service struct {
	ledgerService LedgerService
	orderPlacer   OrderPlacer
	config        Config
	logger        *logger.Logger
}

// NewService creates a new auto-invest service
func NewService(
	ledgerService LedgerService,
	orderPlacer OrderPlacer,
	config Config,
	logger *logger.Logger,
) *Service {
	return &Service{
		ledgerService: ledgerService,
		orderPlacer:   orderPlacer,
		config:        config,
		logger:        logger,
	}
}

// TriggerRequest contains parameters for triggering auto-investment.
// This type is aliased in the allocation package as AutoInvestTriggerRequest.
type TriggerRequest struct {
	UserID        uuid.UUID
	StashID       uuid.UUID
	CorrelationID string // Stable identifier for idempotency (e.g., deposit ID or event ID)
}

// TriggerAutoInvestment checks stash balance and triggers investment if threshold is met.
// The req parameter must have a non-empty CorrelationID for idempotency.
func (s *Service) TriggerAutoInvestment(ctx context.Context, req TriggerRequest) error {
	ctx, span := tracer.Start(ctx, "autoinvest.TriggerAutoInvestment",
		trace.WithAttributes(
			attribute.String("user_id", req.UserID.String()),
			attribute.String("stash_id", req.StashID.String()),
			attribute.String("correlation_id", req.CorrelationID),
		))
	defer span.End()

	// Validate correlation ID for idempotency
	if req.CorrelationID == "" {
		return fmt.Errorf("correlation_id is required for idempotency")
	}

	// Get stash balance
	stashBalance, err := s.ledgerService.GetAccountBalance(ctx, req.UserID, entities.AccountTypeStashBalance)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to get stash balance: %w", err)
	}

	// Check if balance meets threshold
	if stashBalance.LessThan(s.config.MinThreshold) {
		s.logger.Debug("Stash balance below threshold, skipping auto-invest",
			"user_id", req.UserID,
			"balance", stashBalance,
			"threshold", s.config.MinThreshold)
		return nil
	}

	s.logger.Info("Triggering auto-investment",
		"user_id", req.UserID,
		"stash_id", req.StashID,
		"balance", stashBalance)

	// Execute the investment with full stash balance
	if err := s.executeAutoInvestment(ctx, req.UserID, req.StashID, stashBalance, req.CorrelationID); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to execute auto-investment: %w", err)
	}

	return nil
}

// executeAutoInvestment performs the actual investment operation
func (s *Service) executeAutoInvestment(ctx context.Context, userID, stashID uuid.UUID, amount decimal.Decimal, correlationID string) error {
	ctx, span := tracer.Start(ctx, "autoinvest.executeAutoInvestment",
		trace.WithAttributes(
			attribute.String("user_id", userID.String()),
			attribute.String("amount", amount.String()),
		))
	defer span.End()

	// Transfer from stash to fiat exposure (buying power)
	if err := s.transferStashToFiatExposure(ctx, userID, stashID, amount, correlationID); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to transfer to buying power: %w", err)
	}

	// Generate deterministic idempotency key for order
	idempotencyKey := s.generateIdempotencyKey(userID, stashID, amount, correlationID)

	// Place market order (using default symbol for now - could be configurable)
	order, createErr := s.orderPlacer.PlaceMarketOrder(ctx, userID, "SPY", amount)
	if createErr != nil {
		span.RecordError(createErr)
		s.logger.Error("Failed to create order, attempting rollback",
			"user_id", userID,
			"amount", amount,
			"idempotency_key", idempotencyKey,
			"error", createErr)

		// Attempt rollback: transfer fiat exposure back to stash
		rollbackErr := s.transferFiatExposureToStash(ctx, userID, stashID, amount, correlationID)
		if rollbackErr != nil {
			// Log both errors for incident response
			s.logger.Error("Rollback failed after order creation failure",
				"user_id", userID,
				"amount", amount,
				"create_error", createErr,
				"rollback_error", rollbackErr)
			span.RecordError(rollbackErr)

			// Return combined error so callers see both failures
			return fmt.Errorf("order creation failed: %w; rollback also failed: %v", createErr, rollbackErr)
		}

		return fmt.Errorf("order creation failed (rollback succeeded): %w", createErr)
	}

	s.logger.Info("Auto-investment order created",
		"user_id", userID,
		"order_id", order.ID,
		"amount", amount)

	return nil
}

// generateIdempotencyKey creates a deterministic idempotency key from stable inputs.
// The key is a SHA-256 hash of userID, stashID, amount, and correlationID.
// This ensures retries produce the same key for the same operation.
func (s *Service) generateIdempotencyKey(userID, stashID uuid.UUID, amount decimal.Decimal, correlationID string) string {
	// Combine stable inputs into a single string
	input := fmt.Sprintf("autoinvest:%s:%s:%s:%s",
		userID.String(),
		stashID.String(),
		amount.String(),
		correlationID,
	)

	// Hash to create a fixed-length, safe key
	hash := sha256.Sum256([]byte(input))
	return hex.EncodeToString(hash[:])
}

// transferStashToFiatExposure transfers funds from stash to fiat exposure (buying power)
func (s *Service) transferStashToFiatExposure(ctx context.Context, userID, stashID uuid.UUID, amount decimal.Decimal, correlationID string) error {
	stashAccount, err := s.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return fmt.Errorf("failed to get stash account: %w", err)
	}

	fiatAccount, err := s.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeFiatExposure)
	if err != nil {
		return fmt.Errorf("failed to get fiat exposure account: %w", err)
	}

	desc := fmt.Sprintf("Auto-invest transfer from stash %s", stashID)
	idempotencyKey := fmt.Sprintf("autoinvest-transfer:%s:%s", stashID, correlationID)

	txReq := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   stashAccount.ID,
				EntryType:   entities.EntryTypeCredit, // Decrease stash
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

	_, err = s.ledgerService.CreateTransaction(ctx, txReq)
	return err
}

// transferFiatExposureToStash transfers funds back from fiat exposure to stash (rollback)
func (s *Service) transferFiatExposureToStash(ctx context.Context, userID, stashID uuid.UUID, amount decimal.Decimal, correlationID string) error {
	stashAccount, err := s.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return fmt.Errorf("failed to get stash account: %w", err)
	}

	fiatAccount, err := s.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeFiatExposure)
	if err != nil {
		return fmt.Errorf("failed to get fiat exposure account: %w", err)
	}

	desc := fmt.Sprintf("Auto-invest rollback to stash %s", stashID)
	idempotencyKey := fmt.Sprintf("autoinvest-rollback:%s:%s", stashID, correlationID)

	txReq := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   fiatAccount.ID,
				EntryType:   entities.EntryTypeCredit, // Decrease fiat exposure
				Amount:      amount,
				Currency:    "USD",
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

	_, err = s.ledgerService.CreateTransaction(ctx, txReq)
	return err
}
