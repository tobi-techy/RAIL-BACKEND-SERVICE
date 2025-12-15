package allocation

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

var tracer = otel.Tracer("allocation-service")

// AllocationRepository defines the interface for allocation persistence
type AllocationRepository interface {
	// Mode operations
	GetMode(ctx context.Context, userID uuid.UUID) (*entities.SmartAllocationMode, error)
	CreateMode(ctx context.Context, mode *entities.SmartAllocationMode) error
	UpdateMode(ctx context.Context, mode *entities.SmartAllocationMode) error
	PauseMode(ctx context.Context, userID uuid.UUID) error
	ResumeMode(ctx context.Context, userID uuid.UUID) error

	// Event operations
	CreateEvent(ctx context.Context, event *entities.AllocationEvent) error
	GetEventsByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.AllocationEvent, error)
	GetEventsByDateRange(ctx context.Context, userID uuid.UUID, startDate, endDate time.Time) ([]*entities.AllocationEvent, error)

	// Summary operations
	CreateWeeklySummary(ctx context.Context, summary *entities.WeeklyAllocationSummary) error
	GetLatestWeeklySummary(ctx context.Context, userID uuid.UUID) (*entities.WeeklyAllocationSummary, error)

	// Aggregate operations
	CountDeclinesInDateRange(ctx context.Context, userID uuid.UUID, startDate, endDate time.Time) (int, error)
}

// Service handles smart allocation mode operations
type Service struct {
	allocationRepo AllocationRepository
	ledgerService  *ledger.Service
	logger         *logger.Logger
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

// PauseMode pauses the smart allocation mode for a user
func (s *Service) PauseMode(ctx context.Context, userID uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "allocation.PauseMode",
		trace.WithAttributes(attribute.String("user_id", userID.String())))
	defer span.End()

	s.logger.Info("Pausing smart allocation mode", "user_id", userID)

	if err := s.allocationRepo.PauseMode(ctx, userID); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to pause mode: %w", err)
	}

	s.logger.Info("Successfully paused smart allocation mode", "user_id", userID)
	return nil
}

// ResumeMode resumes the smart allocation mode for a user
func (s *Service) ResumeMode(ctx context.Context, userID uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "allocation.ResumeMode",
		trace.WithAttributes(attribute.String("user_id", userID.String())))
	defer span.End()

	s.logger.Info("Resuming smart allocation mode", "user_id", userID)

	if err := s.allocationRepo.ResumeMode(ctx, userID); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to resume mode: %w", err)
	}

	s.logger.Info("Successfully resumed smart allocation mode", "user_id", userID)
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

	// If mode is not active, skip splitting (legacy flow handles it)
	if mode == nil || !mode.Active {
		s.logger.Debug("Allocation mode not active, skipping split",
			"user_id", req.UserID,
			"mode_exists", mode != nil)
		return nil
	}

	s.logger.Info("Processing incoming funds with allocation split",
		"user_id", req.UserID,
		"amount", req.Amount,
		"spending_ratio", mode.RatioSpending,
		"stash_ratio", mode.RatioStash)

	// Calculate split amounts
	spendingAmount := req.Amount.Mul(mode.RatioSpending)
	stashAmount := req.Amount.Mul(mode.RatioStash)

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

	// Get system buffer account
	systemAccount, err := s.ledgerService.GetSystemAccount(ctx, entities.AccountTypeSystemBufferUSDC)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to get system account: %w", err)
	}

	// Create ledger transaction for allocation split
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

	ledgerReq := &entities.CreateTransactionRequest{
		UserID:          &req.UserID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceID:     req.DepositID,
		ReferenceType:   stringPtr("allocation_split"),
		IdempotencyKey:  fmt.Sprintf("allocation-%s-%d", req.UserID.String(), time.Now().UnixNano()),
		Description:     &desc,
		Metadata:        metadata,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   spendingAccount.ID,
				EntryType:   entities.EntryTypeDebit, // Increase spending balance
				Amount:      spendingAmount,
				Currency:    "USDC",
				Description: stringPtr(fmt.Sprintf("Spending allocation: %s", spendingAmount.String())),
			},
			{
				AccountID:   stashAccount.ID,
				EntryType:   entities.EntryTypeDebit, // Increase stash balance
				Amount:      stashAmount,
				Currency:    "USDC",
				Description: stringPtr(fmt.Sprintf("Stash allocation: %s", stashAmount.String())),
			},
			{
				AccountID:   systemAccount.ID,
				EntryType:   entities.EntryTypeCredit, // Decrease system buffer
				Amount:      req.Amount,
				Currency:    "USDC",
				Description: &desc,
			},
		},
	}

	if _, err := s.ledgerService.CreateTransaction(ctx, ledgerReq); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to create ledger transaction: %w", err)
	}

	// Create allocation event for audit trail
	event := &entities.AllocationEvent{
		ID:             uuid.New(),
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
		// Log error but don't fail - ledger entry is already created
		s.logger.Error("Failed to create allocation event", "error", err, "user_id", req.UserID)
	}

	s.logger.Info("Successfully processed incoming funds with allocation",
		"user_id", req.UserID,
		"total", req.Amount,
		"spending", spendingAmount,
		"stash", stashAmount)

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

	// If mode doesn't exist, return zero balances
	if mode == nil {
		return &entities.AllocationBalances{
			UserID:            userID,
			SpendingBalance:   decimal.Zero,
			StashBalance:      decimal.Zero,
			SpendingUsed:      decimal.Zero,
			SpendingRemaining: decimal.Zero,
			TotalBalance:      decimal.Zero,
			ModeActive:        false,
			UpdatedAt:         time.Now(),
		}, nil
	}

	// Get spending balance
	spendingBalance, err := s.ledgerService.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to get spending balance: %w", err)
	}

	// Get stash balance
	stashBalance, err := s.ledgerService.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to get stash balance: %w", err)
	}

	// Calculate spending used from transactions in current period
	spendingUsed, err := s.calculateSpendingUsed(ctx, userID, mode)
	if err != nil {
		s.logger.Warn("Failed to calculate spending used, defaulting to zero", "error", err)
		spendingUsed = decimal.Zero
	}

	balances := &entities.AllocationBalances{
		UserID:          userID,
		SpendingBalance: spendingBalance,
		StashBalance:    stashBalance,
		SpendingUsed:    spendingUsed,
		ModeActive:      mode.Active,
		UpdatedAt:       time.Now(),
	}

	// Calculate derived values
	balances.CalculateTotals()

	span.SetAttributes(
		attribute.String("spending_balance", spendingBalance.String()),
		attribute.String("stash_balance", stashBalance.String()),
		attribute.String("total_balance", balances.TotalBalance.String()),
	)

	return balances, nil
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
		return now.AddDate(0, 0, -(weekday-1)).Truncate(24 * time.Hour)
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

// stringPtr returns a pointer to a string
func stringPtr(s string) *string {
	return &s
}
