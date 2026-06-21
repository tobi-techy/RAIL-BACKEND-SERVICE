package autoinvest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/strategy"
	"github.com/rail-service/rail_service/pkg/analytics"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/rail-service/rail_service/pkg/metrics"
	"github.com/shopspring/decimal"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// ErrMarketClosed is returned by PlaceMarketOrder when the market is not open.
// The autoinvest service treats this as a deferral, not a failure — funds stay
// in fiat_exposure and the kyc_autoinvest worker will retry at next market open.
var ErrMarketClosed = errors.New("market is closed")

var tracer = otel.Tracer("autoinvest-service")

// Config holds configuration for auto-investment
type Config struct {
	// MinThreshold is the minimum stash balance to trigger auto-investment
	MinThreshold decimal.Decimal
	// DefaultBasketID is the default basket for auto-investment
	DefaultBasketID *uuid.UUID
	// FallbackSymbol is the symbol used when strategy engine fails (default: "SPY")
	FallbackSymbol string
	// PositionSyncDelay is the delay before syncing positions after orders (default: 5s)
	PositionSyncDelay time.Duration
}

// LedgerService defines ledger operations for balance queries and transfers
type LedgerService interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
	CreateTransaction(ctx context.Context, req *entities.CreateTransactionRequest) (*entities.LedgerTransaction, error)
	GetOrCreateUserAccount(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (*entities.LedgerAccount, error)
}

// OrderPlacer defines order placement operations
type OrderPlacer interface {
	PlaceMarketOrder(ctx context.Context, userID uuid.UUID, symbol string, amount decimal.Decimal, clientOrderID string) (*entities.AlpacaOrderResponse, error)
}

// FundingBridge journals cash into a user's Alpaca account before orders are placed
type FundingBridge interface {
	JournalToAccount(ctx context.Context, alpacaAccountID string, amount decimal.Decimal, correlationID string) error
}

// AccountLookup resolves a user's Alpaca account ID for journaling
type AccountLookup interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.AlpacaAccount, error)
}

// StrategyEngine defines strategy selection operations
type StrategyEngine interface {
	GetStrategy(ctx context.Context, userID uuid.UUID) (*strategy.StrategyResult, error)
	GetStrategyForAmount(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (*strategy.StrategyResult, error)
}

// UserRepository defines user eligibility checks for auto-invest.
type UserRepository interface {
	GetUserEntityByID(ctx context.Context, id uuid.UUID) (*entities.User, error)
}

// PositionSyncer syncs Alpaca positions back to the local DB after orders are placed.
type PositionSyncer interface {
	SyncPositions(ctx context.Context, userID uuid.UUID) error
}

// AutoInvestRepository defines persistence for auto-invest events and settings.
type AutoInvestRepository interface {
	GetUserSettings(ctx context.Context, userID uuid.UUID) (*entities.AutoInvestSettings, error)
	HasProcessedCorrelation(ctx context.Context, correlationID string) (bool, error)
	GetEventByCorrelation(ctx context.Context, correlationID string) (*entities.AutoInvestEvent, error)
	CreateEvent(ctx context.Context, event *entities.AutoInvestEvent) error
	UpdateEventStatus(ctx context.Context, userID, eventID uuid.UUID, status entities.AutoInvestStatus, errMsg *string) error
	UpdateEventAmount(ctx context.Context, userID, eventID uuid.UUID, amount decimal.Decimal) error
}

// NotificationService defines user notification operations.
type NotificationService interface {
	SendGenericNotification(ctx context.Context, userID uuid.UUID, title, message string) error
	NotifyInvestmentComplete(ctx context.Context, userID uuid.UUID, amount string) error
}

// Service handles automatic investment from stash balance
type Service struct {
	ledgerService       LedgerService
	orderPlacer         OrderPlacer
	strategyEngine      StrategyEngine
	userRepo            UserRepository
	fundingBridge       FundingBridge
	accountLookup       AccountLookup
	positionSyncer      PositionSyncer
	repo                AutoInvestRepository
	notificationService NotificationService
	config              Config
	logger              *logger.Logger
}

// NewService creates a new auto-invest service
func NewService(
	ledgerService LedgerService,
	orderPlacer OrderPlacer,
	config Config,
	logger *logger.Logger,
) *Service {
	if config.FallbackSymbol == "" {
		config.FallbackSymbol = "SPY"
	}
	if config.PositionSyncDelay <= 0 {
		config.PositionSyncDelay = 5 * time.Second
	}
	return &Service{
		ledgerService: ledgerService,
		orderPlacer:   orderPlacer,
		config:        config,
		logger:        logger,
	}
}

// SetOrderPlacer sets the order placer after initialization.
func (s *Service) SetOrderPlacer(orderPlacer OrderPlacer) {
	s.orderPlacer = orderPlacer
}

// SetStrategyEngine sets the strategy engine after initialization.
func (s *Service) SetStrategyEngine(engine StrategyEngine) {
	s.strategyEngine = engine
}

// SetUserRepository sets the user repository for eligibility checks.
func (s *Service) SetUserRepository(userRepo UserRepository) {
	s.userRepo = userRepo
}

// SetFundingBridge sets the funding bridge for journaling cash into Alpaca before orders.
func (s *Service) SetFundingBridge(fb FundingBridge) {
	s.fundingBridge = fb
}

// SetAccountLookup sets the account lookup for resolving Alpaca account IDs.
func (s *Service) SetAccountLookup(al AccountLookup) {
	s.accountLookup = al
}

// SetPositionSyncer sets the position syncer for post-order position refresh.
func (s *Service) SetPositionSyncer(ps PositionSyncer) {
	s.positionSyncer = ps
}

// SetAutoInvestRepository sets the repository for event tracking and settings.
func (s *Service) SetAutoInvestRepository(repo AutoInvestRepository) {
	s.repo = repo
}

// SetNotificationService sets the notification service for user alerts.
func (s *Service) SetNotificationService(ns NotificationService) {
	s.notificationService = ns
}

// TriggerRequest contains parameters for triggering auto-investment.
type TriggerRequest struct {
	UserID        uuid.UUID
	StashID       uuid.UUID
	CorrelationID string // Stable identifier for idempotency (e.g., deposit ID or event ID)
}

// TriggerAutoInvestment checks stash balance and triggers investment if threshold is met.
func (s *Service) TriggerAutoInvestment(ctx context.Context, req TriggerRequest) error {
	ctx, span := tracer.Start(ctx, "autoinvest.TriggerAutoInvestment",
		trace.WithAttributes(
			attribute.String("user_id", req.UserID.String()),
			attribute.String("stash_id", req.StashID.String()),
			attribute.String("correlation_id", req.CorrelationID),
		))
	defer span.End()

	if req.CorrelationID == "" {
		return fmt.Errorf("correlation_id is required for idempotency")
	}

	// Fix #3: Check if this correlation ID was already processed
	if s.repo != nil {
		processed, err := s.repo.HasProcessedCorrelation(ctx, req.CorrelationID)
		if err != nil {
			span.RecordError(err)
			return fmt.Errorf("failed to check correlation ID: %w", err)
		}
		if processed {
			s.logger.Info("Skipping already-processed auto-invest",
				"user_id", req.UserID,
				"correlation_id", req.CorrelationID)
			return nil
		}
	}

	// Check user's auto-invest settings (enabled + per-user threshold)
	threshold := s.config.MinThreshold
	if s.repo != nil {
		settings, err := s.repo.GetUserSettings(ctx, req.UserID)
		if err != nil {
			span.RecordError(err)
			return fmt.Errorf("failed to get auto-invest settings: %w", err)
		}
		if settings != nil {
			if !settings.Enabled {
				s.logger.Info("Skipping auto-invest for user with disabled settings",
					"user_id", req.UserID)
				return nil
			}
			if settings.Threshold.IsPositive() {
				threshold = settings.Threshold
			}
		}
	}
	if threshold.IsNegative() {
		threshold = decimal.Zero
	}

	eligible, reason, err := s.isUserEligibleForAutoInvest(ctx, req.UserID)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to validate auto-invest eligibility: %w", err)
	}
	if !eligible {
		s.logger.Info("Skipping auto-invest for ineligible user",
			"user_id", req.UserID,
			"reason", reason)
		return nil
	}

	stashBalance, err := s.ledgerService.GetAccountBalance(ctx, req.UserID, entities.AccountTypeStashBalance)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to get stash balance: %w", err)
	}

	// Also pick up any funds already in fiat_exposure from a previous market-closed deferral.
	fiatExposureBalance, err := s.ledgerService.GetAccountBalance(ctx, req.UserID, entities.AccountTypeFiatExposure)
	if err != nil {
		s.logger.Warn("Failed to read fiat_exposure balance, proceeding with stash only", "user_id", req.UserID, "error", err)
		fiatExposureBalance = decimal.Zero
	}

	var investableAmount decimal.Decimal
	if stashBalance.GreaterThan(threshold) {
		investableAmount = stashBalance.Sub(threshold)
	} else if fiatExposureBalance.GreaterThanOrEqual(decimal.NewFromFloat(1.0)) {
		// Stash is below threshold but there are deferred funds in fiat_exposure — invest those.
		investableAmount = fiatExposureBalance
	} else {
		s.logger.Debug("Skipping auto-invest, no investable balance",
			"user_id", req.UserID,
			"stash_balance", stashBalance)
		return nil
	}

	// M1: Skip if investable amount is below minimum order size ($1.00)
	minInvestable := decimal.NewFromFloat(1.0)
	if investableAmount.LessThan(minInvestable) {
		s.logger.Debug("Skipping auto-invest, investable amount below minimum",
			"user_id", req.UserID,
			"investable_amount", investableAmount)
		return nil
	}

	s.logger.Info("Triggering auto-investment",
		"user_id", req.UserID,
		"stash_id", req.StashID,
		"stash_balance", stashBalance,
		"investable_amount", investableAmount,
		"retained_stash", threshold)

	if err := s.executeAutoInvestment(ctx, req.UserID, req.StashID, investableAmount, req.CorrelationID); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to execute auto-investment: %w", err)
	}

	analytics.TrackEvent(ctx, req.UserID.String(), analytics.EventAutoInvestEnabled, map[string]any{
		"amount":    investableAmount.InexactFloat64(),
		"threshold": threshold.InexactFloat64(),
	})

	return nil
}

// executeAutoInvestment performs the actual investment operation with compensating rollback.
func (s *Service) executeAutoInvestment(ctx context.Context, userID, stashID uuid.UUID, amount decimal.Decimal, correlationID string) error {
	ctx, span := tracer.Start(ctx, "autoinvest.executeAutoInvestment",
		trace.WithAttributes(
			attribute.String("user_id", userID.String()),
			attribute.String("amount", amount.String()),
		))
	defer span.End()

	// Fix #11: Record pending event before starting
	var eventID uuid.UUID
	if s.repo != nil {
		eventID = uuid.New()
		event := &entities.AutoInvestEvent{
			ID:            eventID,
			UserID:        userID,
			Amount:        amount,
			CorrelationID: correlationID,
			Status:        entities.AutoInvestStatusPending,
			CreatedAt:     time.Now(),
		}
		if err := s.repo.CreateEvent(ctx, event); err != nil {
			// Unique constraint on correlation_id — event already exists.
			var pqErr *pq.Error
			if errors.As(err, &pqErr) && pqErr.Code == "23505" {
				// Fetch the existing event to determine whether to retry or skip.
				existing, fetchErr := s.repo.GetEventByCorrelation(ctx, correlationID)
				if fetchErr != nil || existing == nil {
					return fmt.Errorf("duplicate auto-invest event and failed to fetch existing: %w", err)
				}
				if existing.Status == entities.AutoInvestStatusCompleted {
					s.logger.Info("Duplicate auto-invest event already completed, skipping",
						"user_id", userID, "correlation_id", correlationID)
					return nil
				}
				// Pending = previously deferred (e.g. market was closed). Reuse the event ID and continue.
				s.logger.Info("Resuming deferred auto-invest event",
					"user_id", userID, "correlation_id", correlationID, "event_id", existing.ID)
				eventID = existing.ID
			} else {
				span.RecordError(err)
				return fmt.Errorf("failed to record auto-invest event: %w", err)
			}
		}
	}

	// Balance pre-check removed as authoritative guard: CreateTransaction uses SELECT FOR
	// UPDATE internally, which atomically checks and prevents overdraft. A blocking check
	// here was a TOCTOU race. The check below is advisory-only for amount adjustment.
	currentStash, err := s.ledgerService.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		s.logger.Warn("Advisory stash balance check failed, proceeding with original amount",
			"user_id", userID, "error", err)
		currentStash = amount // proceed with original amount; ledger tx is the authoritative guard
	}
	currentFiatExposure, err := s.ledgerService.GetAccountBalance(ctx, userID, entities.AccountTypeFiatExposure)
	if err != nil {
		currentFiatExposure = decimal.Zero
	}

	alreadyInFiatExposure := currentFiatExposure.GreaterThanOrEqual(amount)

	if !alreadyInFiatExposure {
		// Normal path: funds are still in stash — adjust amount if balance changed.
		currentBalance := currentStash
		if currentBalance.LessThan(amount) {
			s.logger.Warn("Advisory: balance may have changed, adjusting amount",
				"user_id", userID,
				"expected", amount,
				"actual", currentBalance)
			threshold := s.config.MinThreshold
			if threshold.IsNegative() {
				threshold = decimal.Zero
			}
			if currentBalance.LessThanOrEqual(threshold) {
				// Advisory warning only — proceed anyway; the atomic ledger debit
				// (SELECT FOR UPDATE) in CreateTransaction will reject if truly insufficient.
				s.logger.Warn("Advisory: balance at or below threshold, atomic ledger debit will be authoritative",
					"user_id", userID, "balance", currentBalance, "threshold", threshold)
			}
			adjusted := currentBalance.Sub(threshold)
			if adjusted.IsPositive() {
				amount = adjusted
			}
			// If adjusted is non-positive, keep original amount — the ledger tx will reject atomically.
			if s.repo != nil && eventID != uuid.Nil {
				_ = s.repo.UpdateEventAmount(ctx, userID, eventID, amount)
			}
		}
	} else {
		// Retry path: funds already moved to fiat_exposure in a prior deferred attempt.
		// Use the fiat_exposure balance as the authoritative amount.
		amount = currentFiatExposure
		s.logger.Info("Resuming deferred investment from fiat_exposure",
			"user_id", userID, "amount", amount)
	}

	// Step 1: Transfer from stash to fiat exposure (buying power).
	// ATOMICITY: transferStashToFiatExposure calls ledgerService.CreateTransaction which
	// uses SELECT FOR UPDATE inside a DB transaction, making the balance check and debit
	// atomic. The re-check above narrows the TOCTOU window but the ledger transaction is
	// the authoritative guard against overdrafts.
	// Skip if funds are already in fiat_exposure (retry after market-closed deferral).
	if !alreadyInFiatExposure {
		if err := s.transferStashToFiatExposure(ctx, userID, stashID, amount, correlationID); err != nil {
			span.RecordError(err)
			s.markEventFailed(ctx, userID, eventID, "ledger transfer failed")
			return fmt.Errorf("failed to transfer to buying power: %w", err)
		}
	}

	// Step 2: Journal cash into the user's Alpaca account
	if s.fundingBridge != nil && s.accountLookup != nil {
		account, err := s.accountLookup.GetByUserID(ctx, userID)
		if err != nil {
			span.RecordError(err)
			// Fix #6: Compensate — reverse the ledger transfer
			s.compensateLedgerTransfer(ctx, userID, stashID, amount, correlationID)
			s.markEventFailed(ctx, userID, eventID, "alpaca account lookup failed")
			return fmt.Errorf("failed to resolve Alpaca account for journal: %w", err)
		}
		if account == nil {
			s.compensateLedgerTransfer(ctx, userID, stashID, amount, correlationID)
			s.markEventFailed(ctx, userID, eventID, "no alpaca account")
			return fmt.Errorf("user has no Alpaca account")
		}
		if err := s.fundingBridge.JournalToAccount(ctx, account.AlpacaAccountID, amount, correlationID); err != nil {
			span.RecordError(err)
			// Fix #6: Compensate — reverse the ledger transfer
			s.compensateLedgerTransfer(ctx, userID, stashID, amount, correlationID)
			s.markEventFailed(ctx, userID, eventID, "journal to alpaca failed")
			return fmt.Errorf("failed to journal funds to Alpaca: %w", err)
		}
		s.logger.Info("Journaled funds to Alpaca account",
			"user_id", userID,
			"alpaca_account_id", account.AlpacaAccountID,
			"amount", amount)
	}

	// Step 3: Get strategy allocation (passes amount for small-deposit collapse)
	strategyResult, err := s.getStrategyAllocation(ctx, userID, amount)
	if err != nil {
		s.logger.Warn("Failed to get strategy, using fallback single asset",
			"user_id", userID,
			"error", err)
		orderErr := s.placeSingleOrder(ctx, userID, stashID, s.config.FallbackSymbol, amount, correlationID)
		if errors.Is(orderErr, ErrMarketClosed) {
			// Leave funds in fiat_exposure; worker will retry at next market open.
			s.logger.Info("Fallback order deferred — market closed", "user_id", userID)
			return nil
		}
		s.syncPositionsAsync(userID)
		if orderErr != nil {
			s.markEventFailed(ctx, userID, eventID, "fallback order failed: "+orderErr.Error())
		} else {
			s.markEventCompleted(ctx, userID, eventID)
		}
		return orderErr
	}

	s.logger.Info("Executing strategy-based auto-investment",
		"user_id", userID,
		"strategy", strategyResult.StrategyName,
		"allocations", len(strategyResult.Allocations),
		"total_amount", amount)

	// Step 4: Place orders for each allocation
	orderErr := s.placeStrategyOrders(ctx, userID, stashID, amount, correlationID, strategyResult)
	if errors.Is(orderErr, ErrMarketClosed) {
		// Leave funds in fiat_exposure; worker will retry at next market open.
		s.logger.Info("Strategy orders deferred — market closed, funds held in fiat_exposure", "user_id", userID)
		return nil
	}
	s.syncPositionsAsync(userID)

	if orderErr != nil {
		s.markEventFailed(ctx, userID, eventID, "strategy orders failed: "+orderErr.Error())
		if metrics.Business != nil {
			metrics.Business.AutoInvestExecutedTotal.WithLabelValues("failed").Inc()
		}
	} else {
		s.markEventCompleted(ctx, userID, eventID)
		if metrics.Business != nil {
			metrics.Business.AutoInvestExecutedTotal.WithLabelValues("success").Inc()
			metrics.Business.AutoInvestAmount.Observe(amount.InexactFloat64())
		}
	}
	return orderErr
}

// compensateLedgerTransfer reverses a stash-to-fiat-exposure transfer on downstream failure.
func (s *Service) compensateLedgerTransfer(ctx context.Context, userID, stashID uuid.UUID, amount decimal.Decimal, correlationID string) {
	if err := s.transferFiatExposureToStash(ctx, userID, stashID, amount, correlationID); err != nil {
		s.logger.Error("CRITICAL: Failed to compensate ledger transfer — funds may be stranded in fiat_exposure",
			"user_id", userID,
			"amount", amount,
			"correlation_id", correlationID,
			"error", err)
	} else {
		s.logger.Info("Compensated ledger transfer back to stash",
			"user_id", userID, "amount", amount)
	}
}

// markEventFailed updates the event status to failed if repo is wired.
func (s *Service) markEventFailed(ctx context.Context, userID, eventID uuid.UUID, reason string) {
	if s.repo == nil || eventID == uuid.Nil {
		return
	}
	_ = s.repo.UpdateEventStatus(ctx, userID, eventID, entities.AutoInvestStatusFailed, &reason)
}

// markEventCompleted updates the event status to completed if repo is wired.
func (s *Service) markEventCompleted(ctx context.Context, userID, eventID uuid.UUID) {
	if s.repo == nil || eventID == uuid.Nil {
		return
	}
	_ = s.repo.UpdateEventStatus(ctx, userID, eventID, entities.AutoInvestStatusCompleted, nil)
}

// syncPositionsAsync triggers a position sync in the background after orders are placed.
func (s *Service) syncPositionsAsync(userID uuid.UUID) {
	if s.positionSyncer == nil {
		return
	}
	go func() {
		time.Sleep(s.config.PositionSyncDelay)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.positionSyncer.SyncPositions(ctx, userID); err != nil {
			s.logger.Warn("Post-order position sync failed, retrying once", "user_id", userID, "error", err)
			// Retry once after another delay
			time.Sleep(s.config.PositionSyncDelay)
			retryCtx, retryCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer retryCancel()
			if retryErr := s.positionSyncer.SyncPositions(retryCtx, userID); retryErr != nil {
				s.logger.Error("Post-order position sync retry failed", "user_id", userID, "error", retryErr)
			} else {
				s.logger.Info("Post-order position sync succeeded on retry", "user_id", userID)
			}
		} else {
			s.logger.Info("Post-order position sync complete", "user_id", userID)
		}
	}()
}

func (s *Service) isUserEligibleForAutoInvest(ctx context.Context, userID uuid.UUID) (bool, string, error) {
	// Fail-closed: if user repository is not wired, deny auto-invest
	if s.userRepo == nil {
		return false, "user_repo_not_configured", nil
	}

	user, err := s.userRepo.GetUserEntityByID(ctx, userID)
	if err != nil {
		return false, "", err
	}
	if user == nil {
		return false, "user_not_found", nil
	}
	if !user.IsActive {
		return false, "user_inactive", nil
	}
	if user.BridgeKYCStatus == nil || strings.ToLower(strings.TrimSpace(*user.BridgeKYCStatus)) != "active" {
		return false, "bridge_kyc_not_active", nil
	}
	if user.AlpacaAccountID == nil || strings.TrimSpace(*user.AlpacaAccountID) == "" {
		return false, "missing_alpaca_account", nil
	}

	return true, "", nil
}

// getStrategyAllocation retrieves the strategy allocation for a user and deposit amount
func (s *Service) getStrategyAllocation(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (*strategy.StrategyResult, error) {
	if s.strategyEngine == nil {
		return &strategy.StrategyResult{
			StrategyName: "Default Fallback",
			Allocations: []strategy.Allocation{
				{Symbol: s.config.FallbackSymbol, Weight: decimal.NewFromInt(100)},
			},
		}, nil
	}

	return s.strategyEngine.GetStrategyForAmount(ctx, userID, amount)
}

// placeStrategyOrders places orders for each allocation in the strategy.
// Tracks placed vs failed amounts; compensates the ledger for any unplaced portion.
func (s *Service) placeStrategyOrders(ctx context.Context, userID, stashID uuid.UUID, totalAmount decimal.Decimal, correlationID string, result *strategy.StrategyResult) error {
	hundred := decimal.NewFromInt(100)
	var lastErr error
	placedAmount := decimal.Zero

	for i, alloc := range result.Allocations {
		allocAmount := totalAmount.Mul(alloc.Weight).Div(hundred).Truncate(2)

		if allocAmount.LessThan(decimal.NewFromFloat(1.0)) {
			s.logger.Debug("Skipping allocation due to small amount",
				"symbol", alloc.Symbol,
				"amount", allocAmount)
			continue
		}

		orderCorrelationID := fmt.Sprintf("%s:%d:%s", correlationID, i, alloc.Symbol)

		if err := s.placeSingleOrder(ctx, userID, stashID, alloc.Symbol, allocAmount, orderCorrelationID); err != nil {
			// Market closed is a deferral — funds stay in fiat_exposure for retry.
			if errors.Is(err, ErrMarketClosed) {
				s.logger.Info("Strategy orders deferred — market is closed",
					"user_id", userID)
				return ErrMarketClosed
			}
			s.logger.Error("Failed to place order for allocation",
				"user_id", userID,
				"symbol", alloc.Symbol,
				"amount", allocAmount,
				"error", err)
			lastErr = err
		} else {
			placedAmount = placedAmount.Add(allocAmount)
		}
	}

	// Compensate any unplaced amount back to stash to prevent funds stranding in fiat_exposure
	unplaced := totalAmount.Sub(placedAmount)
	if unplaced.GreaterThan(decimal.NewFromFloat(0.01)) {
		s.compensateLedgerTransfer(ctx, userID, stashID, unplaced, correlationID+":partial-refund")
	}

	if placedAmount.IsZero() && lastErr != nil {
		return fmt.Errorf("all strategy orders failed, last error: %w", lastErr)
	}
	if lastErr != nil {
		s.logger.Warn("Partial order failure in strategy execution",
			"user_id", userID,
			"placed_amount", placedAmount,
			"unplaced_amount", unplaced,
			"last_error", lastErr)
	}

	return nil
}

// placeSingleOrder places a single market order
// Fix #2: Validates order response status after placement
func (s *Service) placeSingleOrder(ctx context.Context, userID, stashID uuid.UUID, symbol string, amount decimal.Decimal, correlationID string) error {
	amount = amount.Truncate(2)
	clientOrderID := s.generateIdempotencyKey(userID, stashID, correlationID)

	order, createErr := s.orderPlacer.PlaceMarketOrder(ctx, userID, symbol, amount, clientOrderID)
	if createErr != nil {
		// Market closed is a deferral, not a failure. Funds remain in fiat_exposure
		// and the kyc_autoinvest worker will retry when the market reopens.
		if errors.Is(createErr, ErrMarketClosed) || strings.Contains(createErr.Error(), "market is closed") {
			s.logger.Info("Order deferred — market is closed",
				"user_id", userID,
				"symbol", symbol,
				"amount", amount,
			)
			return ErrMarketClosed
		}
		s.logger.Error("Failed to create order",
			"user_id", userID,
			"symbol", symbol,
			"amount", amount,
			"client_order_id", clientOrderID,
			"error", createErr)
		return fmt.Errorf("order creation failed for %s: %w", symbol, createErr)
	}

	// Fix #2 & #10: Validate order was accepted
	if order != nil && order.Status != "" {
		if order.Status == entities.AlpacaOrderStatusRejected ||
			order.Status == entities.AlpacaOrderStatusCanceled ||
			order.Status == entities.AlpacaOrderStatusExpired {
			s.logger.Error("Order was rejected/canceled by broker",
				"user_id", userID,
				"order_id", order.ID,
				"symbol", symbol,
				"status", order.Status)
			return fmt.Errorf("order %s was %s by broker", symbol, order.Status)
		}
	}

	s.logger.Info("Auto-investment order created",
		"user_id", userID,
		"order_id", order.ID,
		"symbol", symbol,
		"amount", amount,
		"status", order.Status)

	if s.notificationService != nil {
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.notificationService.NotifyInvestmentComplete(bgCtx, userID, amount.StringFixed(2))
		}()
	}

	return nil
}

// generateIdempotencyKey creates a deterministic idempotency key stable across retries.
// Does NOT include amount — amount can change on balance re-check, which would produce
// a different clientOrderID and cause Alpaca to treat a retry as a new order.
func (s *Service) generateIdempotencyKey(userID, stashID uuid.UUID, correlationID string) string {
	input := fmt.Sprintf("autoinvest:%s:%s:%s", userID.String(), stashID.String(), correlationID)
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
	corrHash := fmt.Sprintf("%x", sha256.Sum256([]byte(correlationID)))[:16]
	idempotencyKey := fmt.Sprintf("autoinvest-transfer:%s:%s", stashID, corrHash)

	txReq := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   stashAccount.ID,
				EntryType:   entities.EntryTypeCredit,
				Amount:      amount,
				Currency:    "USDC",
				Description: &desc,
			},
			{
				AccountID:   fiatAccount.ID,
				EntryType:   entities.EntryTypeDebit,
				Amount:      amount,
				Currency:    "USD", // fiat_exposure = USD buying power in Alpaca, not USDC
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
	corrHash := fmt.Sprintf("%x", sha256.Sum256([]byte(correlationID)))[:16]
	idempotencyKey := fmt.Sprintf("autoinvest-rollback:%s:%s", stashID, corrHash)

	txReq := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   fiatAccount.ID,
				EntryType:   entities.EntryTypeCredit,
				Amount:      amount,
				Currency:    "USD", // fiat_exposure = USD buying power in Alpaca, not USDC
				Description: &desc,
			},
			{
				AccountID:   stashAccount.ID,
				EntryType:   entities.EntryTypeDebit,
				Amount:      amount,
				Currency:    "USDC",
				Description: &desc,
			},
		},
	}

	_, err = s.ledgerService.CreateTransaction(ctx, txReq)
	return err
}
