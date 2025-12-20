package autoinvest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/strategy"
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

// StrategyEngine defines strategy selection operations
type StrategyEngine interface {
	GetStrategy(ctx context.Context, userID uuid.UUID) (*strategy.StrategyResult, error)
}

// Service handles automatic investment from stash balance
type Service struct {
	ledgerService  LedgerService
	orderPlacer    OrderPlacer
	strategyEngine StrategyEngine
	config         Config
	logger         *logger.Logger
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

// SetOrderPlacer sets the order placer after initialization.
// This is used to break circular dependencies in the DI container.
func (s *Service) SetOrderPlacer(orderPlacer OrderPlacer) {
	s.orderPlacer = orderPlacer
}

// SetStrategyEngine sets the strategy engine after initialization.
// This is used to break circular dependencies in the DI container.
func (s *Service) SetStrategyEngine(engine StrategyEngine) {
	s.strategyEngine = engine
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

	// Get strategy allocation
	strategyResult, err := s.getStrategyAllocation(ctx, userID)
	if err != nil {
		s.logger.Warn("Failed to get strategy, using fallback single asset",
			"user_id", userID,
			"error", err)
		// Fallback to single SPY order if strategy engine fails
		return s.placeSingleOrder(ctx, userID, stashID, "SPY", amount, correlationID)
	}

	s.logger.Info("Executing strategy-based auto-investment",
		"user_id", userID,
		"strategy", strategyResult.StrategyName,
		"allocations", len(strategyResult.Allocations),
		"total_amount", amount)

	// Place orders for each allocation
	return s.placeStrategyOrders(ctx, userID, stashID, amount, correlationID, strategyResult)
}

// getStrategyAllocation retrieves the strategy allocation for a user
func (s *Service) getStrategyAllocation(ctx context.Context, userID uuid.UUID) (*strategy.StrategyResult, error) {
	if s.strategyEngine == nil {
		// Return default fallback if no strategy engine configured
		return &strategy.StrategyResult{
			StrategyName: "Default Fallback",
			Allocations: []strategy.Allocation{
				{Symbol: "SPY", Weight: decimal.NewFromInt(100)},
			},
		}, nil
	}

	return s.strategyEngine.GetStrategy(ctx, userID)
}

// placeStrategyOrders places orders for each allocation in the strategy
func (s *Service) placeStrategyOrders(ctx context.Context, userID, stashID uuid.UUID, totalAmount decimal.Decimal, correlationID string, result *strategy.StrategyResult) error {
	hundred := decimal.NewFromInt(100)
	var lastErr error

	for i, alloc := range result.Allocations {
		// Calculate amount for this allocation: totalAmount * (weight / 100)
		allocAmount := totalAmount.Mul(alloc.Weight).Div(hundred)

		// Skip if allocation amount is too small
		if allocAmount.LessThan(decimal.NewFromFloat(1.0)) {
			s.logger.Debug("Skipping allocation due to small amount",
				"symbol", alloc.Symbol,
				"amount", allocAmount)
			continue
		}

		// Generate unique correlation ID for each order
		orderCorrelationID := fmt.Sprintf("%s:%d:%s", correlationID, i, alloc.Symbol)

		if err := s.placeSingleOrder(ctx, userID, stashID, alloc.Symbol, allocAmount, orderCorrelationID); err != nil {
			s.logger.Error("Failed to place order for allocation",
				"user_id", userID,
				"symbol", alloc.Symbol,
				"amount", allocAmount,
				"error", err)
			lastErr = err
			// Continue with other allocations even if one fails
		}
	}

	return lastErr
}

// placeSingleOrder places a single market order
func (s *Service) placeSingleOrder(ctx context.Context, userID, stashID uuid.UUID, symbol string, amount decimal.Decimal, correlationID string) error {
	// Generate deterministic idempotency key for order
	idempotencyKey := s.generateIdempotencyKey(userID, stashID, amount, correlationID)

	order, createErr := s.orderPlacer.PlaceMarketOrder(ctx, userID, symbol, amount)
	if createErr != nil {
		s.logger.Error("Failed to create order",
			"user_id", userID,
			"symbol", symbol,
			"amount", amount,
			"idempotency_key", idempotencyKey,
			"error", createErr)
		return fmt.Errorf("order creation failed for %s: %w", symbol, createErr)
	}

	s.logger.Info("Auto-investment order created",
		"user_id", userID,
		"order_id", order.ID,
		"symbol", symbol,
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
