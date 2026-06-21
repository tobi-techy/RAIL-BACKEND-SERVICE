package allocation

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/autoinvest"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/rail-service/rail_service/pkg/analytics"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/rail-service/rail_service/pkg/metrics"
	"github.com/shopspring/decimal"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("allocation-service")

// AllocationRepository defines the interface for allocation persistence
type AllocationRepository interface {
	// Mode operations
	GetMode(ctx context.Context, userID uuid.UUID) (*entities.SmartAllocationMode, error)
	CreateMode(ctx context.Context, mode *entities.SmartAllocationMode) error
	UpdateMode(ctx context.Context, mode *entities.SmartAllocationMode) error

	// Event operations
	CreateEvent(ctx context.Context, event *entities.AllocationEvent) error
	GetEventsByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.AllocationEvent, error)
	GetEventsByDateRange(ctx context.Context, userID uuid.UUID, startDate, endDate time.Time) ([]*entities.AllocationEvent, error)

	// Aggregate operations
	CountDeclinesInDateRange(ctx context.Context, userID uuid.UUID, startDate, endDate time.Time) (int, error)
}

// AutoInvestService defines the interface for auto-investment operations
type AutoInvestService interface {
	TriggerAutoInvestment(ctx context.Context, req autoinvest.TriggerRequest) error
}

// YieldSnapshotter records a stash balance snapshot for yield TWB calculation.
type YieldSnapshotter interface {
	RecordSnapshot(ctx context.Context, userID uuid.UUID, balance decimal.Decimal) error
}

// StashLockRecorder records a new stash lock cycle when stash funds are deposited.
type StashLockRecorder interface {
	RecordDeposit(ctx context.Context, userID, depositID uuid.UUID, amount decimal.Decimal) error
}

// YieldRouter routes allocated stash principal into the configured yield provider.
type YieldRouter interface {
	EnsureDepositYieldRoute(ctx context.Context, userID, depositID uuid.UUID, amount decimal.Decimal, metadata map[string]any) error
	RouteDepositYield(ctx context.Context, userID, depositID uuid.UUID, amount decimal.Decimal, metadata map[string]any) error
}

type spendingTotalReader interface {
	GetTotalSpendingAdded(ctx context.Context, userID uuid.UUID, startDate, endDate time.Time) (decimal.Decimal, error)
}

// UmbraShielder shields allocated funds into Umbra encrypted balances.
type UmbraShielder interface {
	ShieldFunds(ctx context.Context, userID uuid.UUID, mint string, amount string) error
}

// DepositAutomationEvaluator triggers deposit-based automations after allocation.
type DepositAutomationEvaluator interface {
	EvaluateDepositReceived(ctx context.Context, userID uuid.UUID, depositAmount decimal.Decimal)
}

// AllocationNotificationService defines notification operations for allocation failures.
type AllocationNotificationService interface {
	SendGenericNotification(ctx context.Context, userID uuid.UUID, title, message string) error
	NotifyAllocationComplete(ctx context.Context, userID uuid.UUID, spendAmount, investAmount string) error
}

// Service handles smart allocation mode operations
type Service struct {
	allocationRepo      AllocationRepository
	ledgerService       *ledger.Service
	autoInvestService   AutoInvestService
	yieldSnapshotter    YieldSnapshotter
	stashLockRecorder   StashLockRecorder
	yieldRouter         YieldRouter
	notificationService AllocationNotificationService
	umbraShielder       UmbraShielder
	depositAutomation   DepositAutomationEvaluator
	logger              *logger.Logger
	wg                  sync.WaitGroup // tracks in-flight async goroutines for graceful shutdown
	shuttingDown        atomic.Bool    // prevents new async work after Shutdown begins
}

// NewService creates a new allocation service
func NewService(
	allocationRepo AllocationRepository,
	ledgerService *ledger.Service,
	logger *logger.Logger,
) *Service {
	return &Service{
		allocationRepo: allocationRepo,
		ledgerService:  ledgerService,
		logger:         logger,
	}
}

// Shutdown waits for all in-flight async goroutines to complete.
// Call this during graceful shutdown to avoid killing yield routing, auto-invest,
// or notification goroutines mid-operation.
func (s *Service) Shutdown(timeout time.Duration) {
	s.shuttingDown.Store(true)
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		s.logger.Info("All allocation goroutines completed")
	case <-time.After(timeout):
		s.logger.Warn("Allocation service shutdown timed out, some goroutines may still be running",
			"timeout", timeout)
	}
}

// SetAutoInvestService sets the auto-invest service (to avoid circular dependency)
func (s *Service) SetAutoInvestService(autoInvestService AutoInvestService) {
	s.autoInvestService = autoInvestService
}

// tryStartAsync returns false if shutdown is in progress, preventing new async work.
func (s *Service) tryStartAsync() bool {
	if s.shuttingDown.Load() {
		return false
	}
	s.wg.Add(1)
	return true
}

// SetYieldSnapshotter sets the yield snapshot recorder.
func (s *Service) SetYieldSnapshotter(ys YieldSnapshotter) {
	s.yieldSnapshotter = ys
}

// SetYieldRouter sets the provider that routes stash principal into yield after deposit allocation.
func (s *Service) SetYieldRouter(router YieldRouter) {
	s.yieldRouter = router
}

// SetStashLockRecorder sets the stash lock recorder.
func (s *Service) SetStashLockRecorder(r StashLockRecorder) {
	s.stashLockRecorder = r
}

// SetNotificationService sets the notification service for user alerts on auto-invest failure.
func (s *Service) SetNotificationService(ns AllocationNotificationService) {
	s.notificationService = ns
}

// SetUmbraShielder sets the Umbra privacy shielder for post-allocation shielding.
func (s *Service) SetUmbraShielder(u UmbraShielder) {
	s.umbraShielder = u
}

// SetDepositAutomationEvaluator sets the evaluator for deposit-triggered automations.
func (s *Service) SetDepositAutomationEvaluator(e DepositAutomationEvaluator) {
	s.depositAutomation = e
}

// notifyAutoInvestFailure sends a user-facing notification when auto-invest fails silently in a goroutine.
func (s *Service) notifyAutoInvestFailure(userID uuid.UUID, reason string) {
	if s.notificationService == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	title := "Auto-Investment Update"
	msg := "Your automatic investment could not be completed. Your funds remain safe in your stash. Please try again or contact support."
	if err := s.notificationService.SendGenericNotification(ctx, userID, title, msg); err != nil {
		s.logger.Error("Failed to send auto-invest failure notification",
			"user_id", userID, "error", err)
	}
}

// ============================================================================
// Mode Management
// ============================================================================

// GetMode retrieves the allocation mode for a user
func (s *Service) GetMode(ctx context.Context, userID uuid.UUID) (*entities.SmartAllocationMode, error) {
	ctx, span := tracer.Start(ctx, "allocation.GetMode",
		trace.WithAttributes(attribute.String("user_id", userID.String())))
	defer span.End()

	mode, err := s.allocationRepo.GetMode(ctx, userID)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to get allocation mode: %w", err)
	}

	return mode, nil
}

// EnableMode enables the smart allocation mode for a user
func (s *Service) EnableMode(ctx context.Context, userID uuid.UUID, ratios entities.AllocationRatios) error {
	ctx, span := tracer.Start(ctx, "allocation.EnableMode",
		trace.WithAttributes(
			attribute.String("user_id", userID.String()),
			attribute.String("spending_ratio", ratios.SpendingRatio.String()),
			attribute.String("stash_ratio", ratios.StashRatio.String()),
		))
	defer span.End()

	// Validate ratios
	if err := ratios.Validate(); err != nil {
		return fmt.Errorf("invalid ratios: %w", err)
	}

	// SECURITY: Enforce minimum stash ratio at service level — defense in depth.
	minStash := decimal.NewFromFloat(0.10)
	if ratios.StashRatio.LessThan(minStash) {
		return fmt.Errorf("stash ratio must be at least 10%%")
	}

	s.logger.Info("Enabling smart allocation mode",
		"user_id", userID,
		"spending_ratio", ratios.SpendingRatio,
		"stash_ratio", ratios.StashRatio)

	// Check if mode already exists
	existingMode, err := s.allocationRepo.GetMode(ctx, userID)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to check existing mode: %w", err)
	}

	if existingMode != nil {
		// Mode already exists - update it
		existingMode.Active = true
		existingMode.RatioSpending = ratios.SpendingRatio
		existingMode.RatioStash = ratios.StashRatio
		now := time.Now()
		existingMode.ResumedAt = &now
		existingMode.PausedAt = nil

		if err := s.allocationRepo.UpdateMode(ctx, existingMode); err != nil {
			span.RecordError(err)
			return fmt.Errorf("failed to update mode: %w", err)
		}

		s.logger.Info("Updated existing allocation mode", "user_id", userID)
		return nil
	}

	// Create new mode
	now := time.Now()
	mode := &entities.SmartAllocationMode{
		UserID:        userID,
		Active:        true,
		RatioSpending: ratios.SpendingRatio,
		RatioStash:    ratios.StashRatio,
		ResumedAt:     &now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := s.allocationRepo.CreateMode(ctx, mode); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to create mode: %w", err)
	}

	// Create spending_balance and stash_balance ledger accounts
	if err := s.initializeAllocationAccounts(ctx, userID); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to initialize allocation accounts: %w", err)
	}

	s.logger.Info("Successfully enabled smart allocation mode", "user_id", userID)
	return nil
}

// DisableMode disables the smart allocation mode for a user (sets active=false)
func (s *Service) DisableMode(ctx context.Context, userID uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "allocation.DisableMode",
		trace.WithAttributes(attribute.String("user_id", userID.String())))
	defer span.End()

	s.logger.Info("Disabling smart allocation mode", "user_id", userID)

	mode, err := s.allocationRepo.GetMode(ctx, userID)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to get allocation mode: %w", err)
	}

	if mode == nil {
		return nil // Nothing to disable
	}

	mode.Active = false
	now := time.Now()
	mode.PausedAt = &now
	mode.UpdatedAt = now

	if err := s.allocationRepo.UpdateMode(ctx, mode); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to disable mode: %w", err)
	}

	s.logger.Info("Successfully disabled smart allocation mode", "user_id", userID)
	return nil
}

// ============================================================================
// Fund Processing
// ============================================================================

// ProcessIncomingFunds processes incoming funds and splits them based on active mode
func (s *Service) ProcessIncomingFunds(ctx context.Context, req *entities.IncomingFundsRequest) error {
	ctx, span := tracer.Start(ctx, "allocation.ProcessIncomingFunds",
		trace.WithAttributes(
			attribute.String("user_id", req.UserID.String()),
			attribute.String("amount", req.Amount.String()),
			attribute.String("event_type", string(req.EventType)),
		))
	defer span.End()

	// Validate request
	if err := req.Validate(); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}

	// Check if user has allocation mode active
	mode, err := s.allocationRepo.GetMode(ctx, req.UserID)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to get allocation mode: %w", err)
	}

	// Ensure deposits are always split instantly.
	if mode == nil || !mode.Active {
		isDepositEvent := req.EventType == entities.AllocationEventTypeDeposit ||
			req.EventType == entities.AllocationEventTypeFiatDeposit ||
			req.EventType == entities.AllocationEventTypeCryptoDeposit
		if !isDepositEvent {
			s.logger.Debug("Allocation mode not active, skipping non-deposit split",
				"user_id", req.UserID,
				"event_type", req.EventType,
				"mode_exists", mode != nil)
			return nil
		}

		now := time.Now()
		if mode == nil {
			// First deposit for this user: auto-enable default 70/30 mode.
			ratios := entities.DefaultAllocationRatios()
			mode = &entities.SmartAllocationMode{
				UserID:        req.UserID,
				Active:        true,
				RatioSpending: ratios.SpendingRatio,
				RatioStash:    ratios.StashRatio,
				ResumedAt:     &now,
				CreatedAt:     now,
				UpdatedAt:     now,
			}
			if err := s.allocationRepo.CreateMode(ctx, mode); err != nil {
				span.RecordError(err)
				return fmt.Errorf("failed to auto-create allocation mode: %w", err)
			}
			if err := s.initializeAllocationAccounts(ctx, req.UserID); err != nil {
				span.RecordError(err)
				return fmt.Errorf("failed to initialize allocation accounts: %w", err)
			}
			s.logger.Info("Auto-enabled allocation mode for deposit",
				"user_id", req.UserID,
				"spending_ratio", mode.RatioSpending,
				"stash_ratio", mode.RatioStash)
		} else {
			// Mode exists but inactive: auto-resume for deposit processing.
			mode.Active = true
			mode.ResumedAt = &now
			mode.PausedAt = nil
			mode.UpdatedAt = now
			if err := s.allocationRepo.UpdateMode(ctx, mode); err != nil {
				span.RecordError(err)
				return fmt.Errorf("failed to auto-resume allocation mode: %w", err)
			}
			if err := s.initializeAllocationAccounts(ctx, req.UserID); err != nil {
				span.RecordError(err)
				return fmt.Errorf("failed to ensure allocation accounts: %w", err)
			}
			s.logger.Info("Auto-resumed allocation mode for deposit",
				"user_id", req.UserID,
				"spending_ratio", mode.RatioSpending,
				"stash_ratio", mode.RatioStash)
		}
	}

	s.logger.Info("Processing incoming funds with allocation split",
		"user_id", req.UserID,
		"amount", req.Amount,
		"spending_ratio", mode.RatioSpending,
		"stash_ratio", mode.RatioStash)

	// Calculate split amounts.
	// Compute stash as remainder to avoid precision drift and ensure exact total.
	spendingAmount := req.Amount.Mul(mode.RatioSpending)
	stashAmount := req.Amount.Sub(spendingAmount)

	// Get allocation accounts
	spendingAccount, err := s.ledgerService.GetOrCreateUserAccount(ctx, req.UserID, entities.AccountTypeSpendingBalance)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to get spending account: %w", err)
	}

	stashAccount, err := s.ledgerService.GetOrCreateUserAccount(ctx, req.UserID, entities.AccountTypeStashBalance)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to get stash account: %w", err)
	}

	// Get user's USDC balance account (source of funds for allocation)
	usdcAccount, err := s.ledgerService.GetOrCreateUserAccount(ctx, req.UserID, entities.AccountTypeUSDCBalance)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to get USDC account: %w", err)
	}

	// Create idempotency and metadata for allocation transfers
	desc := fmt.Sprintf("Allocation split: %s USDC (70/30 mode)", req.Amount.String())
	metadata := map[string]any{
		"event_type":      req.EventType,
		"spending_amount": spendingAmount.String(),
		"stash_amount":    stashAmount.String(),
		"spending_ratio":  mode.RatioSpending.String(),
		"stash_ratio":     mode.RatioStash.String(),
	}
	if req.SourceTxID != nil {
		metadata["source_tx_id"] = *req.SourceTxID
	}
	if req.DepositID != nil {
		metadata["deposit_id"] = req.DepositID.String()
	}

	idempotencySeed := fmt.Sprintf("allocation:%s:%s:%s", req.UserID.String(), string(req.EventType), req.Amount.String())
	if req.DepositID != nil {
		idempotencySeed = "allocation:deposit:" + req.DepositID.String()
	} else if req.SourceTxID != nil {
		idempotencySeed = "allocation:source_tx:" + *req.SourceTxID
	}
	allocationBaseKey := fmt.Sprintf("allocation-%s", uuid.NewSHA1(uuid.NameSpaceOID, []byte(idempotencySeed)).String())

	// Create a single atomic ledger transaction for both spending and stash allocations.
	// This eliminates the need for compensating transactions if one leg fails.
	// Skip if the deposit was already atomically split at the ledger level (atomic_split flag).
	alreadySplit := metadataHasValue(req.Metadata, "atomic_split")
	if !alreadySplit {
		if err := s.createAtomicAllocationTransfer(
			ctx,
			req,
			spendingAccount.ID,
			stashAccount.ID,
			usdcAccount.ID,
			spendingAmount,
			stashAmount,
			allocationBaseKey,
			desc,
			metadata,
		); err != nil {
			span.RecordError(err)
			if metrics.Business != nil {
				metrics.Business.AllocationExecutedTotal.WithLabelValues("failed").Inc()
			}
			return fmt.Errorf("failed to create allocation transfer: %w", err)
		}
	} else {
		s.logger.Info("Skipping ledger transfer — deposit already atomically split",
			"user_id", req.UserID,
			"amount", req.Amount)
	}

	// Record yield snapshot after stash balance changes.
	// Captures total savings position (stash + goal sub-accounts) so goal funds earn yield.
	if s.yieldSnapshotter != nil {
		newStashBalance, err := s.ledgerService.GetAccountBalance(ctx, req.UserID, entities.AccountTypeStashBalance)
		if err != nil {
			s.logger.Error("Failed to get stash balance for yield snapshot", "user_id", req.UserID, "error", err)
		} else {
			// Include goal balances in the snapshot for yield TWB calculation
			goalTotal, goalErr := s.ledgerService.GetTotalGoalAllocated(ctx, req.UserID)
			if goalErr != nil {
				s.logger.Error("Failed to get goal balances for yield snapshot", "user_id", req.UserID, "error", goalErr)
			} else {
				newStashBalance = newStashBalance.Add(goalTotal)
			}
			if err := s.yieldSnapshotter.RecordSnapshot(ctx, req.UserID, newStashBalance); err != nil {
				s.logger.Error("Failed to record yield snapshot", "user_id", req.UserID, "error", err)
			}
		}
	}

	// Record stash lock cycle for the deposited amount.
	if s.stashLockRecorder != nil && req.DepositID != nil {
		if err := s.stashLockRecorder.RecordDeposit(ctx, req.UserID, *req.DepositID, stashAmount); err != nil {
			s.logger.Error("Failed to record stash lock cycle (non-fatal, ledger transfers already committed)", "user_id", req.UserID, "deposit_id", *req.DepositID, "error", err)
		}
	}

	routeYield := s.yieldRouter != nil && req.DepositID != nil && stashAmount.GreaterThan(decimal.Zero)
	var routeDepositID uuid.UUID
	routeAmount := stashAmount
	routeMetadata := copyMetadata(req.Metadata)
	if routeYield {
		routeDepositID = *req.DepositID
		if err := s.yieldRouter.EnsureDepositYieldRoute(ctx, req.UserID, routeDepositID, routeAmount, routeMetadata); err != nil {
			// Non-fatal: ledger split is already committed. The async RouteDepositYield
			// goroutine and its retry loop will handle routing. Don't block notification
			// and event recording.
			s.logger.Error("Failed to create durable yield route after allocation ledger transfer (non-fatal, will retry async)",
				"user_id", req.UserID,
				"deposit_id", routeDepositID,
				"amount", routeAmount,
				"error", err)
		}
	}

	// Create allocation event for audit trail
	eventID := uuid.New()
	if req.DepositID != nil {
		eventID = uuid.NewSHA1(uuid.NameSpaceOID, []byte("allocation-event:deposit:"+req.DepositID.String()))
	} else if req.SourceTxID != nil {
		eventID = uuid.NewSHA1(uuid.NameSpaceOID, []byte("allocation-event:source_tx:"+*req.SourceTxID))
	}
	event := &entities.AllocationEvent{
		ID:             eventID,
		UserID:         req.UserID,
		TotalAmount:    req.Amount,
		StashAmount:    stashAmount,
		SpendingAmount: spendingAmount,
		EventType:      req.EventType,
		SourceTxID:     req.SourceTxID,
		Metadata:       req.Metadata,
		CreatedAt:      time.Now(),
	}

	if err := s.allocationRepo.CreateEvent(ctx, event); err != nil {
		// Duplicate events can happen on webhook replay when the ledger transaction was already created.
		// Treat duplicate-key conflicts as benign idempotent outcomes.
		if strings.Contains(strings.ToLower(err.Error()), "duplicate key") {
			s.logger.Debug("Allocation event already exists; skipping duplicate",
				"user_id", req.UserID,
				"event_id", event.ID.String())
		} else {
			// Log error but don't fail - ledger entry is already created
			s.logger.Error("Failed to create allocation event", "error", err, "user_id", req.UserID)
		}
	}

	s.logger.Info("Successfully processed incoming funds with allocation",
		"user_id", req.UserID,
		"total", req.Amount,
		"spending", spendingAmount,
		"stash", stashAmount)

	if metrics.Business != nil {
		metrics.Business.AllocationExecutedTotal.WithLabelValues("success").Inc()
		metrics.Business.AllocationSpendAmount.Observe(spendingAmount.InexactFloat64())
		metrics.Business.AllocationStashAmount.Observe(stashAmount.InexactFloat64())
	}

	// Track net inflow and AUM for Mixpanel
	analytics.TrackEvent(ctx, req.UserID.String(), analytics.EventNetInflowRecorded, map[string]any{
		"amount":         req.Amount.InexactFloat64(),
		"spend_amount":   spendingAmount.InexactFloat64(),
		"stash_amount":   stashAmount.InexactFloat64(),
		"direction":      "inflow",
		"event_type":     string(req.EventType),
	})
	analytics.G().Increment(ctx, req.UserID.String(), map[string]int{
		analytics.PropTotalDeposits: 1,
	})
	analytics.G().Identify(ctx, req.UserID.String(), map[string]any{
		analytics.PropLastDepositAt: time.Now().UTC().Format(time.RFC3339),
	})

	// Route the stash principal into the yield provider as soon as a Circle-backed
	// deposit has been split. This is async because cross-chain settlement and
	// Blend deposit execution can outlive the deposit webhook request.
	if routeYield {
		userID := req.UserID
		depositID := routeDepositID
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					s.logger.Error("Panic in yield routing goroutine",
						"user_id", userID,
						"deposit_id", depositID,
						"panic", r,
						"stack", string(debug.Stack()))
				}
			}()
			bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			if err := s.yieldRouter.RouteDepositYield(bgCtx, userID, depositID, routeAmount, routeMetadata); err != nil {
				s.logger.Error("Failed to route deposit stash into yield provider; retry worker will continue",
					"user_id", userID,
					"deposit_id", depositID,
					"amount", routeAmount,
					"error", err)
			}
		}()
	}

	// Shield allocated funds through Umbra privacy layer (async, non-blocking)
	if s.umbraShielder != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					s.logger.Error("Panic in Umbra shielding goroutine",
						"user_id", req.UserID, "panic", r)
				}
			}()
			bgCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			usdcMint := "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
			totalMicroUnits := req.Amount.Shift(6).IntPart() // USDC has 6 decimals
			if err := s.umbraShielder.ShieldFunds(bgCtx, req.UserID, usdcMint, fmt.Sprintf("%d", totalMicroUnits)); err != nil {
				s.logger.Error("Failed to shield funds through Umbra (non-fatal, ledger split already committed)",
					"user_id", req.UserID,
					"amount", req.Amount,
					"error", err)
			} else {
				s.logger.Info("Successfully shielded funds through Umbra",
					"user_id", req.UserID,
					"amount", req.Amount)
			}
		}()
	}

	// Notify user that the split completed
	if s.notificationService != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.notificationService.NotifyAllocationComplete(bgCtx, req.UserID,
				spendingAmount.StringFixed(2), stashAmount.StringFixed(2),
			)
		}()
	}

	// Trigger auto-investment asynchronously if service is configured
	// Use detached context to avoid cancellation when parent returns
	if s.autoInvestService != nil && stashAccount != nil {
		// Generate correlation ID from deposit for idempotency
		correlationID := event.ID.String()
		if req.DepositID != nil {
			correlationID = req.DepositID.String()
		}

		userID := req.UserID
		stashID := stashAccount.ID

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			// Panic recovery to prevent goroutine crashes from affecting the system
			defer func() {
				if r := recover(); r != nil {
					s.logger.Error("Panic in auto-invest goroutine",
						"user_id", userID,
						"panic", r,
						"stack", string(debug.Stack()))
					s.notifyAutoInvestFailure(userID, fmt.Sprintf("internal error: %v", r))
				}
			}()

			// Use detached context with timeout instead of request context
			bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if err := s.autoInvestService.TriggerAutoInvestment(bgCtx, autoinvest.TriggerRequest{
				UserID:        userID,
				StashID:       stashID,
				CorrelationID: correlationID,
			}); err != nil {
				s.logger.Error("Failed to trigger auto-investment",
					"user_id", userID,
					"error", err)
				// Fix #7: Notify user on failure
				s.notifyAutoInvestFailure(userID, err.Error())
			}
		}()
	}

	// Trigger deposit-based automations (e.g., "on every deposit, move $10 to goal")
	if s.depositAutomation != nil {
		depositUserID := req.UserID
		depositAmount := req.Amount
		if s.tryStartAsync() {
			go func() {
				defer s.wg.Done()
				defer func() {
					if r := recover(); r != nil {
						s.logger.Error("Panic in deposit automation goroutine",
							"user_id", depositUserID,
							"panic", r,
							"stack", string(debug.Stack()))
					}
				}()
				s.depositAutomation.EvaluateDepositReceived(context.Background(), depositUserID, depositAmount)
			}()
		}
	}

	return nil
}

// ============================================================================
// Spending Enforcement
// ============================================================================

// CanSpend checks if a user can spend the requested amount based on their spending balance
func (s *Service) CanSpend(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (bool, error) {
	ctx, span := tracer.Start(ctx, "allocation.CanSpend",
		trace.WithAttributes(
			attribute.String("user_id", userID.String()),
			attribute.String("amount", amount.String()),
		))
	defer span.End()

	// Check if user has allocation mode active
	mode, err := s.allocationRepo.GetMode(ctx, userID)
	if err != nil {
		span.RecordError(err)
		return false, fmt.Errorf("failed to get allocation mode: %w", err)
	}

	// If mode is not active, allow spending (legacy flow)
	if mode == nil || !mode.Active {
		span.SetAttributes(attribute.Bool("mode_active", false))
		return true, nil
	}

	// Get spending balance
	spendingBalance, err := s.ledgerService.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		span.RecordError(err)
		return false, fmt.Errorf("failed to get spending balance: %w", err)
	}

	canSpend := spendingBalance.GreaterThanOrEqual(amount)

	span.SetAttributes(
		attribute.Bool("mode_active", true),
		attribute.String("spending_balance", spendingBalance.String()),
		attribute.Bool("can_spend", canSpend),
	)

	if !canSpend {
		s.logger.Warn("Spending declined - insufficient spending balance",
			"user_id", userID,
			"requested", amount,
			"available", spendingBalance)
	}

	return canSpend, nil
}

// ============================================================================
// Balance Queries
// ============================================================================

// GetBalances retrieves allocation balances for a user
func (s *Service) GetBalances(ctx context.Context, userID uuid.UUID) (*entities.AllocationBalances, error) {
	ctx, span := tracer.Start(ctx, "allocation.GetBalances",
		trace.WithAttributes(attribute.String("user_id", userID.String())))
	defer span.End()

	// Check if user has allocation mode active
	mode, err := s.allocationRepo.GetMode(ctx, userID)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to get allocation mode: %w", err)
	}

	// Always read USDC/fiat so totals remain accurate when funds move to broker cash.
	usdcBalance, err := s.getOptionalAccountBalance(ctx, userID, entities.AccountTypeUSDCBalance)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to get usdc balance: %w", err)
	}
	fiatExposure, err := s.getOptionalAccountBalance(ctx, userID, entities.AccountTypeFiatExposure)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to get fiat exposure: %w", err)
	}

	// If mode doesn't exist, map legacy balance view:
	// - spending reflects liquid USDC
	// - invest reflects broker cash (fiat exposure)
	if mode == nil {
		balances := &entities.AllocationBalances{
			UserID:            userID,
			SpendingBalance:   usdcBalance,
			StashBalance:      decimal.Zero,
			USDCBalance:       usdcBalance,
			FiatExposure:      fiatExposure,
			SpendingUsed:      decimal.Zero,
			SpendingRemaining: decimal.Zero,
			ModeActive:        false,
			UpdatedAt:         time.Now(),
		}
		balances.CalculateTotals()
		return balances, nil
	}
	if s.ledgerService == nil {
		return nil, fmt.Errorf("ledger service not configured")
	}

	var (
		spendingBalance decimal.Decimal
		stashBalance    decimal.Decimal
		spendingUsed    decimal.Decimal
		spendingErr     error
		stashErr        error
		spendingUsedErr error
	)

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		spendingBalance, spendingErr = s.getOptionalAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	}()

	go func() {
		defer wg.Done()
		stashBalance, stashErr = s.getOptionalAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	}()

	go func() {
		defer wg.Done()
		spendingUsed, spendingUsedErr = s.calculateSpendingUsed(ctx, userID, mode)
	}()

	wg.Wait()

	if spendingErr != nil {
		span.RecordError(spendingErr)
		return nil, fmt.Errorf("failed to get spending balance: %w", spendingErr)
	}
	if stashErr != nil {
		span.RecordError(stashErr)
		return nil, fmt.Errorf("failed to get stash balance: %w", stashErr)
	}
	if spendingUsedErr != nil {
		s.logger.Warn("Failed to calculate spending used, defaulting to zero", "error", spendingUsedErr)
		spendingUsed = decimal.Zero
	}

	balances := &entities.AllocationBalances{
		UserID:          userID,
		SpendingBalance: spendingBalance,
		StashBalance:    stashBalance,
		USDCBalance:     usdcBalance,
		FiatExposure:    fiatExposure,
		SpendingUsed:    spendingUsed,
		ModeActive:      mode.Active,
		UpdatedAt:       time.Now(),
	}

	// Calculate derived values
	balances.CalculateTotals()

	span.SetAttributes(
		attribute.String("spending_balance", spendingBalance.String()),
		attribute.String("stash_balance", stashBalance.String()),
		attribute.String("fiat_exposure", fiatExposure.String()),
		attribute.String("total_balance", balances.TotalBalance.String()),
	)

	return balances, nil
}

// GetBalancesLite retrieves current balances without historical spending calculations.
// This is intended for latency-sensitive endpoints where only live balances are needed.
func (s *Service) GetBalancesLite(ctx context.Context, userID uuid.UUID) (*entities.AllocationBalances, error) {
	ctx, span := tracer.Start(ctx, "allocation.GetBalancesLite",
		trace.WithAttributes(attribute.String("user_id", userID.String())))
	defer span.End()

	mode, err := s.allocationRepo.GetMode(ctx, userID)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to get allocation mode: %w", err)
	}

	usdcBalance, err := s.getOptionalAccountBalance(ctx, userID, entities.AccountTypeUSDCBalance)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to get usdc balance: %w", err)
	}
	fiatExposure, err := s.getOptionalAccountBalance(ctx, userID, entities.AccountTypeFiatExposure)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to get fiat exposure: %w", err)
	}

	if mode == nil {
		balances := &entities.AllocationBalances{
			UserID:            userID,
			SpendingBalance:   usdcBalance,
			StashBalance:      decimal.Zero,
			USDCBalance:       usdcBalance,
			FiatExposure:      fiatExposure,
			SpendingUsed:      decimal.Zero,
			SpendingRemaining: decimal.Zero,
			ModeActive:        false,
			UpdatedAt:         time.Now(),
		}
		balances.CalculateTotals()
		return balances, nil
	}
	if s.ledgerService == nil {
		return nil, fmt.Errorf("ledger service not configured")
	}

	var (
		spendingBalance decimal.Decimal
		stashBalance    decimal.Decimal
		spendingErr     error
		stashErr        error
	)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		spendingBalance, spendingErr = s.getOptionalAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	}()

	go func() {
		defer wg.Done()
		stashBalance, stashErr = s.getOptionalAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	}()

	wg.Wait()

	if spendingErr != nil {
		span.RecordError(spendingErr)
		return nil, fmt.Errorf("failed to get spending balance: %w", spendingErr)
	}
	if stashErr != nil {
		span.RecordError(stashErr)
		return nil, fmt.Errorf("failed to get stash balance: %w", stashErr)
	}

	balances := &entities.AllocationBalances{
		UserID:          userID,
		SpendingBalance: spendingBalance,
		StashBalance:    stashBalance,
		USDCBalance:     usdcBalance,
		FiatExposure:    fiatExposure,
		SpendingUsed:    decimal.Zero,
		ModeActive:      mode.Active,
		UpdatedAt:       time.Now(),
	}
	balances.CalculateTotals()

	return balances, nil
}

func (s *Service) getOptionalAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error) {
	if s.ledgerService == nil {
		return decimal.Zero, nil
	}

	balance, err := s.ledgerService.GetAccountBalance(ctx, userID, accountType)
	if err == nil {
		return balance, nil
	}

	if strings.Contains(err.Error(), "account not found") {
		return decimal.Zero, nil
	}

	return decimal.Zero, err
}

// ============================================================================
// Decline Tracking
// ============================================================================

// LogDeclinedSpending logs a declined spending attempt due to allocation limit
func (s *Service) LogDeclinedSpending(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reason string) error {
	ctx, span := tracer.Start(ctx, "allocation.LogDeclinedSpending",
		trace.WithAttributes(
			attribute.String("user_id", userID.String()),
			attribute.String("amount", amount.String()),
			attribute.String("reason", reason),
		))
	defer span.End()

	s.logger.Warn("Logging declined spending",
		"user_id", userID,
		"amount", amount,
		"reason", reason)

	// Log metric for monitoring
	span.SetAttributes(attribute.Bool("spending_declined", true))

	// Note: The actual decline is tracked by the declined_due_to_7030 column
	// in the transactions table, which should be set by the calling service
	// when they create the transaction record.

	// We could also optionally create an event for this
	// but that might be redundant with the transaction record

	return nil
}

// ============================================================================
// Helper Methods
// ============================================================================

// calculateSpendingUsed calculates the total spending used in the current period
func (s *Service) calculateSpendingUsed(ctx context.Context, userID uuid.UUID, mode *entities.SmartAllocationMode) (decimal.Decimal, error) {
	if mode == nil || !mode.Active {
		return decimal.Zero, nil
	}

	// Get start of current period - default to daily reset
	periodStart := s.getPeriodStart("daily")

	if repoWithTotals, ok := s.allocationRepo.(spendingTotalReader); ok {
		total, err := repoWithTotals.GetTotalSpendingAdded(ctx, userID, periodStart, time.Now())
		if err == nil {
			return total, nil
		}
	}

	// Query spending events from allocation events table
	events, err := s.allocationRepo.GetEventsByDateRange(ctx, userID, periodStart, time.Now())
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to get allocation events: %w", err)
	}

	// Sum up spending amounts from events
	total := decimal.Zero
	for _, event := range events {
		// SpendingAmount represents the amount allocated to spending
		total = total.Add(event.SpendingAmount)
	}

	return total, nil
}

// getPeriodStart returns the start of the current period based on reset frequency
func (s *Service) getPeriodStart(resetPeriod string) time.Time {
	now := time.Now()
	switch resetPeriod {
	case "daily":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	case "weekly":
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7 // Sunday
		}
		return now.AddDate(0, 0, -(weekday - 1)).Truncate(24 * time.Hour)
	case "monthly":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	default:
		// Default to daily
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	}
}

// initializeAllocationAccounts creates spending and stash accounts for a user
func (s *Service) initializeAllocationAccounts(ctx context.Context, userID uuid.UUID) error {
	// Create spending balance account
	_, err := s.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("failed to create spending balance account: %w", err)
	}

	// Create stash balance account
	_, err = s.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return fmt.Errorf("failed to create stash balance account: %w", err)
	}

	return nil
}

func (s *Service) createAtomicAllocationTransfer(
	ctx context.Context,
	req *entities.IncomingFundsRequest,
	spendingAccountID uuid.UUID,
	stashAccountID uuid.UUID,
	sourceAccountID uuid.UUID,
	spendingAmount decimal.Decimal,
	stashAmount decimal.Decimal,
	idempotencyKey string,
	description string,
	baseMetadata map[string]any,
) error {
	metadata := make(map[string]any, len(baseMetadata)+1)
	for k, v := range baseMetadata {
		metadata[k] = v
	}
	metadata["allocation_type"] = "atomic_split"

	var entries []entities.CreateEntryRequest

	if !spendingAmount.IsZero() {
		spendDesc := fmt.Sprintf("Spending allocation: %s", spendingAmount.String())
		entries = append(entries,
			entities.CreateEntryRequest{
				AccountID:   spendingAccountID,
				EntryType:   entities.EntryTypeDebit,
				Amount:      spendingAmount,
				Currency:    "USDC",
				Description: stringPtr(spendDesc),
			},
			entities.CreateEntryRequest{
				AccountID:   sourceAccountID,
				EntryType:   entities.EntryTypeCredit,
				Amount:      spendingAmount,
				Currency:    "USDC",
				Description: &description,
			},
		)
	}

	if !stashAmount.IsZero() {
		stashDesc := fmt.Sprintf("Stash allocation: %s", stashAmount.String())
		entries = append(entries,
			entities.CreateEntryRequest{
				AccountID:   stashAccountID,
				EntryType:   entities.EntryTypeDebit,
				Amount:      stashAmount,
				Currency:    "USDC",
				Description: stringPtr(stashDesc),
			},
			entities.CreateEntryRequest{
				AccountID:   sourceAccountID,
				EntryType:   entities.EntryTypeCredit,
				Amount:      stashAmount,
				Currency:    "USDC",
				Description: &description,
			},
		)
	}

	if len(entries) == 0 {
		return nil
	}

	reqTx := &entities.CreateTransactionRequest{
		UserID:          &req.UserID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceID:     req.DepositID,
		ReferenceType:   stringPtr("allocation_split"),
		IdempotencyKey:  idempotencyKey,
		Description:     &description,
		Metadata:        metadata,
		Entries:         entries,
	}

	_, err := s.ledgerService.CreateTransaction(ctx, reqTx)
	if err != nil {
		return fmt.Errorf("create atomic allocation transfer: %w", err)
	}

	return nil
}

func (s *Service) createAllocationTransfer(
	ctx context.Context,
	req *entities.IncomingFundsRequest,
	targetAccountID uuid.UUID,
	sourceAccountID uuid.UUID,
	amount decimal.Decimal,
	allocationType string,
	idempotencyKey string,
	targetDescription string,
	rootDescription string,
	baseMetadata map[string]any,
) error {
	if amount.IsZero() {
		return nil
	}

	metadata := make(map[string]any, len(baseMetadata)+1)
	for k, v := range baseMetadata {
		metadata[k] = v
	}
	metadata["allocation_type"] = allocationType

	reqTx := &entities.CreateTransactionRequest{
		UserID:          &req.UserID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceID:     req.DepositID,
		ReferenceType:   stringPtr("allocation_split"),
		IdempotencyKey:  idempotencyKey,
		Description:     &rootDescription,
		Metadata:        metadata,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   targetAccountID,
				EntryType:   entities.EntryTypeDebit,
				Amount:      amount,
				Currency:    "USDC",
				Description: stringPtr(targetDescription),
			},
			{
				AccountID:   sourceAccountID,
				EntryType:   entities.EntryTypeCredit,
				Amount:      amount,
				Currency:    "USDC",
				Description: &rootDescription,
			},
		},
	}

	_, err := s.ledgerService.CreateTransaction(ctx, reqTx)
	if err != nil {
		return fmt.Errorf("create allocation transfer (%s): %w", allocationType, err)
	}

	return nil
}

// stringPtr returns a pointer to a string
func stringPtr(s string) *string {
	return &s
}

func metadataHasValue(metadata map[string]any, key string) bool {
	if metadata == nil {
		return false
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return false
	}
	s := strings.TrimSpace(fmt.Sprintf("%v", value))
	if s == "" || s == "false" || s == "0" {
		return false
	}
	return true
}

func copyMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return nil
	}
	out := make(map[string]any, len(metadata))
	for k, v := range metadata {
		out[k] = v
	}
	return out
}
