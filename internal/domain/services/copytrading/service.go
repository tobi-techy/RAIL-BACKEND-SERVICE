package copytrading

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// Repository defines the interface for copy trading data access
type Repository interface {
	// Conductor operations
	GetActiveConductors(ctx context.Context, limit, offset int, sortBy string) ([]*entities.Conductor, int, error)
	GetConductorByID(ctx context.Context, id uuid.UUID) (*entities.Conductor, error)
	GetConductorByUserID(ctx context.Context, userID uuid.UUID) (*entities.Conductor, error)
	CreateConductor(ctx context.Context, conductor *entities.Conductor) error
	UpdateConductorAUM(ctx context.Context, conductorID uuid.UUID, aum decimal.Decimal) error
	IncrementFollowersCount(ctx context.Context, conductorID uuid.UUID, delta int) error

	// Conductor application operations
	CreateApplication(ctx context.Context, app *entities.ConductorApplication) error
	GetApplicationByID(ctx context.Context, id uuid.UUID) (*entities.ConductorApplication, error)
	GetApplicationByUserID(ctx context.Context, userID uuid.UUID) (*entities.ConductorApplication, error)
	GetPendingApplications(ctx context.Context, limit, offset int) ([]*entities.ConductorApplication, int, error)
	UpdateApplicationStatus(ctx context.Context, id uuid.UUID, status entities.ConductorApplicationStatus, reviewerID *uuid.UUID, reason string) error

	// Track operations
	CreateTrack(ctx context.Context, track *entities.Track) error
	GetTrackByID(ctx context.Context, id uuid.UUID) (*entities.Track, error)
	GetTracksByConductorID(ctx context.Context, conductorID uuid.UUID) ([]*entities.Track, error)
	GetActiveTracks(ctx context.Context, limit, offset int) ([]*entities.Track, int, error)
	UpdateTrack(ctx context.Context, track *entities.Track) error
	DeleteTrack(ctx context.Context, id uuid.UUID) error

	// Track allocation operations
	CreateTrackAllocations(ctx context.Context, allocations []entities.TrackAllocation) error
	GetTrackAllocations(ctx context.Context, trackID uuid.UUID) ([]entities.TrackAllocation, error)
	DeleteTrackAllocations(ctx context.Context, trackID uuid.UUID) error

	// Draft operations
	CreateDraft(ctx context.Context, draft *entities.Draft) error
	GetDraftByID(ctx context.Context, id uuid.UUID) (*entities.Draft, error)
	GetDraftsByDrafterID(ctx context.Context, drafterID uuid.UUID) ([]*entities.Draft, error)
	GetActiveDraftsByConductorID(ctx context.Context, conductorID uuid.UUID) ([]*entities.Draft, error)
	GetExistingDraft(ctx context.Context, drafterID, conductorID uuid.UUID) (*entities.Draft, error)
	UpdateDraftStatus(ctx context.Context, draftID uuid.UUID, status entities.DraftStatus) error
	UpdateDraftCapital(ctx context.Context, draftID uuid.UUID, newCapital decimal.Decimal) error
	UpdateDraftAUM(ctx context.Context, draftID uuid.UUID, currentAUM, profitLoss decimal.Decimal) error

	// Signal operations
	CreateSignal(ctx context.Context, signal *entities.Signal) error
	GetSignalByID(ctx context.Context, id uuid.UUID) (*entities.Signal, error)
	GetPendingSignals(ctx context.Context, limit int) ([]*entities.Signal, error)
	UpdateSignalStatus(ctx context.Context, signalID uuid.UUID, status entities.SignalStatus, processedCount, failedCount int) error
	GetSignalsByConductor(ctx context.Context, conductorID uuid.UUID, limit int) ([]*entities.Signal, error)

	// Execution log operations
	CreateExecutionLog(ctx context.Context, log *entities.SignalExecutionLog) error
	GetExecutionLogByIdempotencyKey(ctx context.Context, key string) (*entities.SignalExecutionLog, error)
	GetExecutionLogsByDraft(ctx context.Context, draftID uuid.UUID, limit int) ([]*entities.SignalExecutionLog, error)
}

// UserProvider checks if a user exists
type UserProvider interface {
	UserExists(ctx context.Context, userID uuid.UUID) (bool, error)
}

// BalanceProvider provides user balance information
type BalanceProvider interface {
	GetAvailableBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
	DeductBalance(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, description string) error
	AddBalance(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, description string) error
}

// TradingAdapter executes trades on the brokerage
type TradingAdapter interface {
	PlaceOrder(ctx context.Context, userID uuid.UUID, symbol string, side string, quantity decimal.Decimal) (orderID string, executedPrice decimal.Decimal, err error)
	GetCurrentPrice(ctx context.Context, symbol string) (decimal.Decimal, error)
}

// Service handles copy trading business logic
type Service struct {
	repo            Repository
	userProvider    UserProvider
	balanceProvider BalanceProvider
	tradingAdapter  TradingAdapter
	logger          *zap.Logger
}

// NewService creates a new copy trading service
func NewService(repo Repository, balanceProvider BalanceProvider, tradingAdapter TradingAdapter, logger *zap.Logger) *Service {
	return &Service{
		repo:           repo,
		balanceProvider: balanceProvider,
		tradingAdapter: tradingAdapter,
		logger:         logger,
	}
}

// === Conductor Operations ===

// ListConductors returns a paginated list of active conductors
func (s *Service) ListConductors(ctx context.Context, page, pageSize int, sortBy string) (*entities.ConductorListResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	conductors, total, err := s.repo.GetActiveConductors(ctx, pageSize, offset, sortBy)
	if err != nil {
		return nil, fmt.Errorf("failed to list conductors: %w", err)
	}

	summaries := make([]entities.ConductorSummary, len(conductors))
	for i, c := range conductors {
		summaries[i] = entities.ConductorSummary{
			ID:             c.ID,
			DisplayName:    c.DisplayName,
			AvatarURL:      c.AvatarURL,
			TotalReturn:    c.TotalReturn,
			WinRate:        c.WinRate,
			FollowersCount: c.FollowersCount,
			FeeRate:        c.FeeRate,
			MinDraftAmount: c.MinDraftAmount,
			IsVerified:     c.IsVerified,
			SourceAUM:      c.SourceAUM,
		}
	}

	return &entities.ConductorListResponse{
		Conductors: summaries,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
	}, nil
}

// GetConductor returns detailed conductor information
func (s *Service) GetConductor(ctx context.Context, conductorID uuid.UUID) (*entities.Conductor, error) {
	conductor, err := s.repo.GetConductorByID(ctx, conductorID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conductor: %w", err)
	}
	if conductor == nil {
		return nil, fmt.Errorf("conductor not found")
	}
	return conductor, nil
}

// GetConductorSignals returns recent signals for a conductor
func (s *Service) GetConductorSignals(ctx context.Context, conductorID uuid.UUID, limit int) ([]*entities.Signal, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	return s.repo.GetSignalsByConductor(ctx, conductorID, limit)
}

// === Draft Operations ===

// CreateDraft creates a new copy relationship
func (s *Service) CreateDraft(ctx context.Context, drafterID uuid.UUID, req *entities.CreateDraftRequest) (*entities.Draft, error) {
	// Validate conductor exists and is active
	conductor, err := s.repo.GetConductorByID(ctx, req.ConductorID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conductor: %w", err)
	}
	if conductor == nil {
		return nil, fmt.Errorf("conductor not found")
	}
	if conductor.Status != entities.ConductorStatusActive {
		return nil, fmt.Errorf("conductor is not active")
	}

	// Prevent self-following
	if conductor.UserID == drafterID {
		return nil, fmt.Errorf("cannot copy your own trades")
	}

	// Check minimum draft amount
	if req.AllocatedCapital.LessThan(conductor.MinDraftAmount) {
		return nil, fmt.Errorf("minimum allocation is %s", conductor.MinDraftAmount.String())
	}

	// Check if already following
	existing, err := s.repo.GetExistingDraft(ctx, drafterID, req.ConductorID)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing draft: %w", err)
	}
	if existing != nil {
		return nil, fmt.Errorf("already following this conductor")
	}

	// Check user has sufficient balance
	balance, err := s.balanceProvider.GetAvailableBalance(ctx, drafterID)
	if err != nil {
		return nil, fmt.Errorf("failed to get balance: %w", err)
	}
	if balance.LessThan(req.AllocatedCapital) {
		return nil, fmt.Errorf("insufficient balance: have %s, need %s", balance.String(), req.AllocatedCapital.String())
	}

	// Deduct balance for allocation
	err = s.balanceProvider.DeductBalance(ctx, drafterID, req.AllocatedCapital, fmt.Sprintf("Copy trading allocation to %s", conductor.DisplayName))
	if err != nil {
		return nil, fmt.Errorf("failed to deduct balance: %w", err)
	}

	// Set default copy ratio
	copyRatio := req.CopyRatio
	if copyRatio.IsZero() || copyRatio.GreaterThan(decimal.NewFromInt(1)) {
		copyRatio = decimal.NewFromInt(1)
	}

	now := time.Now().UTC()
	draft := &entities.Draft{
		ID:               uuid.New(),
		DrafterID:        drafterID,
		ConductorID:      req.ConductorID,
		Status:           entities.DraftStatusActive,
		AllocatedCapital: req.AllocatedCapital,
		CurrentAUM:       req.AllocatedCapital,
		StartValue:       req.AllocatedCapital,
		TotalProfitLoss:  decimal.Zero,
		TotalFeesPaid:    decimal.Zero,
		CopyRatio:        copyRatio,
		AutoAdjust:       req.AutoAdjust,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err := s.repo.CreateDraft(ctx, draft); err != nil {
		// Refund on failure
		_ = s.balanceProvider.AddBalance(ctx, drafterID, req.AllocatedCapital, "Refund: draft creation failed")
		return nil, fmt.Errorf("failed to create draft: %w", err)
	}

	// Increment conductor's followers count
	if err := s.repo.IncrementFollowersCount(ctx, req.ConductorID, 1); err != nil {
		s.logger.Warn("Failed to increment followers count", zap.Error(err))
	}

	s.logger.Info("Draft created",
		zap.String("draft_id", draft.ID.String()),
		zap.String("drafter_id", drafterID.String()),
		zap.String("conductor_id", req.ConductorID.String()),
		zap.String("allocated_capital", req.AllocatedCapital.String()))

	return draft, nil
}

// GetUserDrafts returns all drafts for a user
func (s *Service) GetUserDrafts(ctx context.Context, userID uuid.UUID) ([]*entities.DraftSummary, error) {
	drafts, err := s.repo.GetDraftsByDrafterID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get drafts: %w", err)
	}

	summaries := make([]*entities.DraftSummary, len(drafts))
	for i, d := range drafts {
		conductor, _ := s.repo.GetConductorByID(ctx, d.ConductorID)
		conductorName := ""
		if conductor != nil {
			conductorName = conductor.DisplayName
		}

		returnPct := decimal.Zero
		if !d.StartValue.IsZero() {
			returnPct = d.TotalProfitLoss.Div(d.StartValue).Mul(decimal.NewFromInt(100))
		}

		summaries[i] = &entities.DraftSummary{
			ID:               d.ID,
			ConductorID:      d.ConductorID,
			ConductorName:    conductorName,
			Status:           d.Status,
			AllocatedCapital: d.AllocatedCapital,
			CurrentAUM:       d.CurrentAUM,
			TotalProfitLoss:  d.TotalProfitLoss,
			ReturnPct:        returnPct,
			CreatedAt:        d.CreatedAt,
		}
	}

	return summaries, nil
}

// GetDraft returns a specific draft with conductor details
func (s *Service) GetDraft(ctx context.Context, userID, draftID uuid.UUID) (*entities.Draft, error) {
	draft, err := s.repo.GetDraftByID(ctx, draftID)
	if err != nil {
		return nil, fmt.Errorf("failed to get draft: %w", err)
	}
	if draft == nil {
		return nil, fmt.Errorf("draft not found")
	}
	if draft.DrafterID != userID {
		return nil, fmt.Errorf("unauthorized")
	}

	// Attach conductor details
	conductor, _ := s.repo.GetConductorByID(ctx, draft.ConductorID)
	draft.Conductor = conductor

	return draft, nil
}

// PauseDraft pauses copying for a draft
func (s *Service) PauseDraft(ctx context.Context, userID, draftID uuid.UUID) error {
	draft, err := s.repo.GetDraftByID(ctx, draftID)
	if err != nil {
		return fmt.Errorf("failed to get draft: %w", err)
	}
	if draft == nil {
		return fmt.Errorf("draft not found")
	}
	if draft.DrafterID != userID {
		return fmt.Errorf("unauthorized")
	}
	if draft.Status != entities.DraftStatusActive {
		return fmt.Errorf("draft is not active")
	}

	if err := s.repo.UpdateDraftStatus(ctx, draftID, entities.DraftStatusPaused); err != nil {
		return fmt.Errorf("failed to pause draft: %w", err)
	}

	s.logger.Info("Draft paused", zap.String("draft_id", draftID.String()))
	return nil
}

// ResumeDraft resumes copying for a paused draft
func (s *Service) ResumeDraft(ctx context.Context, userID, draftID uuid.UUID) error {
	draft, err := s.repo.GetDraftByID(ctx, draftID)
	if err != nil {
		return fmt.Errorf("failed to get draft: %w", err)
	}
	if draft == nil {
		return fmt.Errorf("draft not found")
	}
	if draft.DrafterID != userID {
		return fmt.Errorf("unauthorized")
	}
	if draft.Status != entities.DraftStatusPaused {
		return fmt.Errorf("draft is not paused")
	}

	if err := s.repo.UpdateDraftStatus(ctx, draftID, entities.DraftStatusActive); err != nil {
		return fmt.Errorf("failed to resume draft: %w", err)
	}

	s.logger.Info("Draft resumed", zap.String("draft_id", draftID.String()))
	return nil
}

// UnlinkDraft stops copying and liquidates positions
func (s *Service) UnlinkDraft(ctx context.Context, userID, draftID uuid.UUID) error {
	draft, err := s.repo.GetDraftByID(ctx, draftID)
	if err != nil {
		return fmt.Errorf("failed to get draft: %w", err)
	}
	if draft == nil {
		return fmt.Errorf("draft not found")
	}
	if draft.DrafterID != userID {
		return fmt.Errorf("unauthorized")
	}
	if draft.Status == entities.DraftStatusUnlinked {
		return fmt.Errorf("draft already unlinked")
	}

	// Mark as unlinking
	if err := s.repo.UpdateDraftStatus(ctx, draftID, entities.DraftStatusUnlinking); err != nil {
		return fmt.Errorf("failed to update draft status: %w", err)
	}

	// Return current AUM to user's balance
	if draft.CurrentAUM.GreaterThan(decimal.Zero) {
		err = s.balanceProvider.AddBalance(ctx, userID, draft.CurrentAUM, "Copy trading unlink - funds returned")
		if err != nil {
			s.logger.Error("Failed to return funds on unlink", zap.Error(err))
		}
	}

	// Mark as unlinked
	if err := s.repo.UpdateDraftStatus(ctx, draftID, entities.DraftStatusUnlinked); err != nil {
		return fmt.Errorf("failed to finalize unlink: %w", err)
	}

	// Decrement conductor's followers count
	if err := s.repo.IncrementFollowersCount(ctx, draft.ConductorID, -1); err != nil {
		s.logger.Warn("Failed to decrement followers count", zap.Error(err))
	}

	s.logger.Info("Draft unlinked",
		zap.String("draft_id", draftID.String()),
		zap.String("returned_amount", draft.CurrentAUM.String()))

	return nil
}

// ResizeDraft adjusts the allocated capital
func (s *Service) ResizeDraft(ctx context.Context, userID, draftID uuid.UUID, newCapital decimal.Decimal) error {
	draft, err := s.repo.GetDraftByID(ctx, draftID)
	if err != nil {
		return fmt.Errorf("failed to get draft: %w", err)
	}
	if draft == nil {
		return fmt.Errorf("draft not found")
	}
	if draft.DrafterID != userID {
		return fmt.Errorf("unauthorized")
	}
	if draft.Status != entities.DraftStatusActive && draft.Status != entities.DraftStatusPaused {
		return fmt.Errorf("cannot resize draft in current status")
	}

	conductor, err := s.repo.GetConductorByID(ctx, draft.ConductorID)
	if err != nil || conductor == nil {
		return fmt.Errorf("conductor not found")
	}

	if newCapital.LessThan(conductor.MinDraftAmount) {
		return fmt.Errorf("minimum allocation is %s", conductor.MinDraftAmount.String())
	}

	diff := newCapital.Sub(draft.AllocatedCapital)

	if diff.GreaterThan(decimal.Zero) {
		// Adding more capital - check balance
		balance, err := s.balanceProvider.GetAvailableBalance(ctx, userID)
		if err != nil {
			return fmt.Errorf("failed to get balance: %w", err)
		}
		if balance.LessThan(diff) {
			return fmt.Errorf("insufficient balance")
		}
		if err := s.balanceProvider.DeductBalance(ctx, userID, diff, "Copy trading allocation increase"); err != nil {
			return fmt.Errorf("failed to deduct balance: %w", err)
		}
	} else if diff.LessThan(decimal.Zero) {
		// Reducing capital - return funds
		if err := s.balanceProvider.AddBalance(ctx, userID, diff.Abs(), "Copy trading allocation decrease"); err != nil {
			return fmt.Errorf("failed to add balance: %w", err)
		}
	}

	if err := s.repo.UpdateDraftCapital(ctx, draftID, newCapital); err != nil {
		return fmt.Errorf("failed to update capital: %w", err)
	}

	s.logger.Info("Draft resized",
		zap.String("draft_id", draftID.String()),
		zap.String("old_capital", draft.AllocatedCapital.String()),
		zap.String("new_capital", newCapital.String()))

	return nil
}

// GetDraftExecutionHistory returns execution logs for a draft
func (s *Service) GetDraftExecutionHistory(ctx context.Context, userID, draftID uuid.UUID, limit int) ([]*entities.SignalExecutionLog, error) {
	draft, err := s.repo.GetDraftByID(ctx, draftID)
	if err != nil {
		return nil, fmt.Errorf("failed to get draft: %w", err)
	}
	if draft == nil {
		return nil, fmt.Errorf("draft not found")
	}
	if draft.DrafterID != userID {
		return nil, fmt.Errorf("unauthorized")
	}

	if limit < 1 || limit > 100 {
		limit = 50
	}

	return s.repo.GetExecutionLogsByDraft(ctx, draftID, limit)
}

// === Signal Processing ===

// ProcessSignal processes a conductor's trade signal for all active drafters
func (s *Service) ProcessSignal(ctx context.Context, signal *entities.Signal) error {
	// Update signal status to processing
	if err := s.repo.UpdateSignalStatus(ctx, signal.ID, entities.SignalStatusProcessing, 0, 0); err != nil {
		return fmt.Errorf("failed to update signal status: %w", err)
	}

	// Get all active drafts for this conductor
	drafts, err := s.repo.GetActiveDraftsByConductorID(ctx, signal.ConductorID)
	if err != nil {
		return fmt.Errorf("failed to get active drafts: %w", err)
	}

	processedCount := 0
	failedCount := 0

	for _, draft := range drafts {
		err := s.executeCopyTrade(ctx, draft, signal)
		if err != nil {
			s.logger.Error("Failed to execute copy trade",
				zap.String("draft_id", draft.ID.String()),
				zap.String("signal_id", signal.ID.String()),
				zap.Error(err))
			failedCount++
		} else {
			processedCount++
		}
	}

	// Update signal status
	status := entities.SignalStatusCompleted
	if failedCount > 0 && processedCount == 0 {
		status = entities.SignalStatusFailed
	}

	if err := s.repo.UpdateSignalStatus(ctx, signal.ID, status, processedCount, failedCount); err != nil {
		s.logger.Error("Failed to update signal status", zap.Error(err))
	}

	s.logger.Info("Signal processed",
		zap.String("signal_id", signal.ID.String()),
		zap.Int("processed", processedCount),
		zap.Int("failed", failedCount))

	return nil
}

// executeCopyTrade executes a single copy trade for a drafter
func (s *Service) executeCopyTrade(ctx context.Context, draft *entities.Draft, signal *entities.Signal) error {
	// Generate idempotency key
	idempotencyKey := fmt.Sprintf("copy_%s_%s", draft.ID.String(), signal.ID.String())

	// Check if already executed
	existing, err := s.repo.GetExecutionLogByIdempotencyKey(ctx, idempotencyKey)
	if err != nil {
		return fmt.Errorf("failed to check idempotency: %w", err)
	}
	if existing != nil {
		s.logger.Debug("Trade already executed", zap.String("idempotency_key", idempotencyKey))
		return nil
	}

	// Calculate proportional quantity
	// Drafter Quantity = (Drafter's Allocated Capital / Conductor's Source AUM) × Conductor's Base Quantity × Copy Ratio
	if signal.ConductorAUMAtSignal.IsZero() {
		return fmt.Errorf("conductor AUM is zero")
	}

	ratio := draft.AllocatedCapital.Div(signal.ConductorAUMAtSignal)
	drafterQuantity := signal.BaseQuantity.Mul(ratio).Mul(draft.CopyRatio)

	// Get current price
	currentPrice, err := s.tradingAdapter.GetCurrentPrice(ctx, signal.AssetTicker)
	if err != nil {
		return s.logExecution(ctx, draft, signal, idempotencyKey, decimal.Zero, decimal.Zero,
			entities.ExecutionStatusFailed, fmt.Sprintf("failed to get price: %v", err))
	}

	// Calculate trade value
	tradeValue := drafterQuantity.Mul(currentPrice)

	// Check minimum trade value
	if tradeValue.LessThan(entities.MinimumTradeValue) {
		return s.logExecution(ctx, draft, signal, idempotencyKey, drafterQuantity, currentPrice,
			entities.ExecutionStatusSkippedTooSmall, "trade value below minimum")
	}

	// Check available capital in draft
	if signal.Side == "buy" && tradeValue.GreaterThan(draft.CurrentAUM) {
		// Partial execution with available funds
		if draft.CurrentAUM.LessThan(entities.MinimumTradeValue) {
			return s.logExecution(ctx, draft, signal, idempotencyKey, decimal.Zero, currentPrice,
				entities.ExecutionStatusInsufficientFunds, "insufficient funds for minimum trade")
		}
		drafterQuantity = draft.CurrentAUM.Div(currentPrice)
		tradeValue = draft.CurrentAUM
	}

	// Execute the trade
	orderID, executedPrice, err := s.tradingAdapter.PlaceOrder(ctx, draft.DrafterID, signal.AssetTicker, signal.Side, drafterQuantity)
	if err != nil {
		return s.logExecution(ctx, draft, signal, idempotencyKey, drafterQuantity, currentPrice,
			entities.ExecutionStatusFailed, fmt.Sprintf("order failed: %v", err))
	}

	executedValue := drafterQuantity.Mul(executedPrice)

	// Update draft AUM
	newAUM := draft.CurrentAUM
	if signal.Side == "buy" {
		newAUM = newAUM.Sub(executedValue)
	} else {
		newAUM = newAUM.Add(executedValue)
	}
	profitLoss := newAUM.Sub(draft.StartValue)

	if err := s.repo.UpdateDraftAUM(ctx, draft.ID, newAUM, profitLoss); err != nil {
		s.logger.Error("Failed to update draft AUM", zap.Error(err))
	}

	// Log successful execution
	now := time.Now().UTC()
	log := &entities.SignalExecutionLog{
		ID:               uuid.New(),
		DraftID:          draft.ID,
		SignalID:         signal.ID,
		ExecutedQuantity: drafterQuantity,
		ExecutedPrice:    executedPrice,
		ExecutedValue:    executedValue,
		Status:           entities.ExecutionStatusSuccess,
		FeeApplied:       decimal.Zero,
		OrderID:          orderID,
		IdempotencyKey:   idempotencyKey,
		CreatedAt:        now,
		ExecutedAt:       &now,
	}

	return s.repo.CreateExecutionLog(ctx, log)
}

// logExecution logs a non-successful execution
func (s *Service) logExecution(ctx context.Context, draft *entities.Draft, signal *entities.Signal,
	idempotencyKey string, quantity, price decimal.Decimal, status entities.ExecutionStatus, errMsg string) error {

	now := time.Now().UTC()
	log := &entities.SignalExecutionLog{
		ID:               uuid.New(),
		DraftID:          draft.ID,
		SignalID:         signal.ID,
		ExecutedQuantity: quantity,
		ExecutedPrice:    price,
		ExecutedValue:    quantity.Mul(price),
		Status:           status,
		FeeApplied:       decimal.Zero,
		ErrorMessage:     errMsg,
		IdempotencyKey:   idempotencyKey,
		CreatedAt:        now,
	}

	if err := s.repo.CreateExecutionLog(ctx, log); err != nil {
		s.logger.Error("Failed to create execution log", zap.Error(err))
	}

	if status == entities.ExecutionStatusFailed {
		return fmt.Errorf("execution failed: %s", errMsg)
	}
	return nil
}

// CreateSignalFromConductorTrade creates a signal when a conductor executes a trade
func (s *Service) CreateSignalFromConductorTrade(ctx context.Context, conductorUserID uuid.UUID, ticker, side string, quantity, price decimal.Decimal, orderID string) (*entities.Signal, error) {
	conductor, err := s.repo.GetConductorByUserID(ctx, conductorUserID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conductor: %w", err)
	}
	if conductor == nil {
		return nil, nil // User is not a conductor
	}
	if conductor.Status != entities.ConductorStatusActive {
		return nil, nil // Conductor not active
	}

	signalType := entities.SignalTypeBuy
	if side == "sell" {
		signalType = entities.SignalTypeSell
	}

	signal := &entities.Signal{
		ID:                   uuid.New(),
		ConductorID:          conductor.ID,
		AssetTicker:          ticker,
		SignalType:           signalType,
		Side:                 side,
		BaseQuantity:         quantity,
		BasePrice:            price,
		BaseValue:            quantity.Mul(price),
		ConductorAUMAtSignal: conductor.SourceAUM,
		OrderID:              orderID,
		Status:               entities.SignalStatusPending,
		CreatedAt:            time.Now().UTC(),
	}

	if err := s.repo.CreateSignal(ctx, signal); err != nil {
		return nil, fmt.Errorf("failed to create signal: %w", err)
	}

	s.logger.Info("Signal created from conductor trade",
		zap.String("signal_id", signal.ID.String()),
		zap.String("conductor_id", conductor.ID.String()),
		zap.String("ticker", ticker),
		zap.String("side", side))

	return signal, nil
}

// SetUserProvider sets the user provider (optional dependency injection)
func (s *Service) SetUserProvider(provider UserProvider) {
	s.userProvider = provider
}

// === Conductor Application Operations ===

// ApplyAsConductor submits an application to become a conductor
func (s *Service) ApplyAsConductor(ctx context.Context, userID uuid.UUID, req *entities.CreateConductorApplicationRequest) (*entities.ConductorApplication, error) {
	// Check if user exists (must have Rail account)
	if s.userProvider != nil {
		exists, err := s.userProvider.UserExists(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("failed to verify user: %w", err)
		}
		if !exists {
			return nil, fmt.Errorf("user must have an existing Rail account")
		}
	}

	// Check if already a conductor
	existing, err := s.repo.GetConductorByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to check conductor status: %w", err)
	}
	if existing != nil {
		return nil, fmt.Errorf("user is already a conductor")
	}

	// Check for pending application
	pendingApp, err := s.repo.GetApplicationByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing application: %w", err)
	}
	if pendingApp != nil && pendingApp.Status == entities.ConductorApplicationStatusPending {
		return nil, fmt.Errorf("application already pending review")
	}

	now := time.Now().UTC()
	app := &entities.ConductorApplication{
		ID:                 uuid.New(),
		UserID:             userID,
		DisplayName:        req.DisplayName,
		Bio:                req.Bio,
		InvestmentStrategy: req.InvestmentStrategy,
		Experience:         req.Experience,
		SocialLinks:        req.SocialLinks,
		Status:             entities.ConductorApplicationStatusPending,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	if err := s.repo.CreateApplication(ctx, app); err != nil {
		return nil, fmt.Errorf("failed to create application: %w", err)
	}

	s.logger.Info("Conductor application submitted",
		zap.String("application_id", app.ID.String()),
		zap.String("user_id", userID.String()))

	return app, nil
}

// GetMyApplication returns the current user's conductor application
func (s *Service) GetMyApplication(ctx context.Context, userID uuid.UUID) (*entities.ConductorApplication, error) {
	app, err := s.repo.GetApplicationByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get application: %w", err)
	}
	return app, nil
}

// ListPendingApplications returns pending conductor applications (admin only)
func (s *Service) ListPendingApplications(ctx context.Context, page, pageSize int) ([]*entities.ConductorApplication, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	return s.repo.GetPendingApplications(ctx, pageSize, offset)
}

// ReviewApplication approves or rejects a conductor application (admin only)
func (s *Service) ReviewApplication(ctx context.Context, applicationID, reviewerID uuid.UUID, req *entities.ReviewConductorApplicationRequest) error {
	app, err := s.repo.GetApplicationByID(ctx, applicationID)
	if err != nil {
		return fmt.Errorf("failed to get application: %w", err)
	}
	if app == nil {
		return fmt.Errorf("application not found")
	}
	if app.Status != entities.ConductorApplicationStatusPending {
		return fmt.Errorf("application already reviewed")
	}

	if req.Approved {
		// Create conductor profile
		now := time.Now().UTC()
		conductor := &entities.Conductor{
			ID:             uuid.New(),
			UserID:         app.UserID,
			DisplayName:    app.DisplayName,
			Bio:            app.Bio,
			Status:         entities.ConductorStatusActive,
			FeeRate:        decimal.NewFromFloat(0.02), // Default 2% fee
			SourceAUM:      decimal.Zero,
			TotalReturn:    decimal.Zero,
			WinRate:        decimal.Zero,
			MaxDrawdown:    decimal.Zero,
			SharpeRatio:    decimal.Zero,
			TotalTrades:    0,
			FollowersCount: 0,
			MinDraftAmount: decimal.NewFromInt(100), // Default $100 minimum
			IsVerified:     true,
			VerifiedAt:     &now,
			CreatedAt:      now,
			UpdatedAt:      now,
		}

		if err := s.repo.CreateConductor(ctx, conductor); err != nil {
			return fmt.Errorf("failed to create conductor: %w", err)
		}

		if err := s.repo.UpdateApplicationStatus(ctx, applicationID, entities.ConductorApplicationStatusApproved, &reviewerID, ""); err != nil {
			return fmt.Errorf("failed to update application status: %w", err)
		}

		s.logger.Info("Conductor application approved",
			zap.String("application_id", applicationID.String()),
			zap.String("conductor_id", conductor.ID.String()))
	} else {
		if err := s.repo.UpdateApplicationStatus(ctx, applicationID, entities.ConductorApplicationStatusRejected, &reviewerID, req.RejectionReason); err != nil {
			return fmt.Errorf("failed to update application status: %w", err)
		}

		s.logger.Info("Conductor application rejected",
			zap.String("application_id", applicationID.String()),
			zap.String("reason", req.RejectionReason))
	}

	return nil
}

// === Track Operations ===

// CreateTrack creates a new track for a conductor
func (s *Service) CreateTrack(ctx context.Context, userID uuid.UUID, req *entities.CreateTrackRequest) (*entities.Track, error) {
	// Verify user is a conductor
	conductor, err := s.repo.GetConductorByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conductor: %w", err)
	}
	if conductor == nil {
		return nil, fmt.Errorf("user is not a conductor")
	}
	if conductor.Status != entities.ConductorStatusActive {
		return nil, fmt.Errorf("conductor account is not active")
	}

	// Validate allocations sum to 100%
	totalWeight := decimal.Zero
	for _, alloc := range req.Allocations {
		if alloc.TargetWeight.LessThanOrEqual(decimal.Zero) {
			return nil, fmt.Errorf("allocation weight must be positive")
		}
		totalWeight = totalWeight.Add(alloc.TargetWeight)
	}
	if !totalWeight.Equal(decimal.NewFromInt(100)) {
		return nil, fmt.Errorf("allocations must sum to 100%%, got %s%%", totalWeight.String())
	}

	now := time.Now().UTC()
	track := &entities.Track{
		ID:             uuid.New(),
		ConductorID:    conductor.ID,
		Name:           req.Name,
		Description:    req.Description,
		RiskLevel:      req.RiskLevel,
		IsActive:       true,
		FollowersCount: 0,
		TotalReturn:    decimal.Zero,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := s.repo.CreateTrack(ctx, track); err != nil {
		return nil, fmt.Errorf("failed to create track: %w", err)
	}

	// Create allocations
	allocations := make([]entities.TrackAllocation, len(req.Allocations))
	for i, alloc := range req.Allocations {
		allocations[i] = entities.TrackAllocation{
			ID:           uuid.New(),
			TrackID:      track.ID,
			AssetTicker:  alloc.AssetTicker,
			AssetName:    alloc.AssetName,
			TargetWeight: alloc.TargetWeight,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
	}

	if err := s.repo.CreateTrackAllocations(ctx, allocations); err != nil {
		return nil, fmt.Errorf("failed to create allocations: %w", err)
	}

	track.Allocations = allocations

	s.logger.Info("Track created",
		zap.String("track_id", track.ID.String()),
		zap.String("conductor_id", conductor.ID.String()),
		zap.String("name", track.Name))

	return track, nil
}

// GetTrack returns a track with its allocations
func (s *Service) GetTrack(ctx context.Context, trackID uuid.UUID) (*entities.Track, error) {
	track, err := s.repo.GetTrackByID(ctx, trackID)
	if err != nil {
		return nil, fmt.Errorf("failed to get track: %w", err)
	}
	if track == nil {
		return nil, fmt.Errorf("track not found")
	}

	allocations, err := s.repo.GetTrackAllocations(ctx, trackID)
	if err != nil {
		return nil, fmt.Errorf("failed to get allocations: %w", err)
	}
	track.Allocations = allocations

	conductor, _ := s.repo.GetConductorByID(ctx, track.ConductorID)
	track.Conductor = conductor

	return track, nil
}

// ListTracks returns active tracks with pagination
func (s *Service) ListTracks(ctx context.Context, page, pageSize int) ([]*entities.Track, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	tracks, total, err := s.repo.GetActiveTracks(ctx, pageSize, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list tracks: %w", err)
	}

	// Attach conductor info
	for _, track := range tracks {
		conductor, _ := s.repo.GetConductorByID(ctx, track.ConductorID)
		track.Conductor = conductor
	}

	return tracks, total, nil
}

// GetConductorTracks returns all tracks for a conductor
func (s *Service) GetConductorTracks(ctx context.Context, conductorID uuid.UUID) ([]*entities.Track, error) {
	tracks, err := s.repo.GetTracksByConductorID(ctx, conductorID)
	if err != nil {
		return nil, fmt.Errorf("failed to get tracks: %w", err)
	}

	for _, track := range tracks {
		allocations, _ := s.repo.GetTrackAllocations(ctx, track.ID)
		track.Allocations = allocations
	}

	return tracks, nil
}

// UpdateTrack updates a track's details and allocations
func (s *Service) UpdateTrack(ctx context.Context, userID, trackID uuid.UUID, req *entities.UpdateTrackRequest) (*entities.Track, error) {
	track, err := s.repo.GetTrackByID(ctx, trackID)
	if err != nil {
		return nil, fmt.Errorf("failed to get track: %w", err)
	}
	if track == nil {
		return nil, fmt.Errorf("track not found")
	}

	// Verify ownership
	conductor, err := s.repo.GetConductorByUserID(ctx, userID)
	if err != nil || conductor == nil {
		return nil, fmt.Errorf("user is not a conductor")
	}
	if track.ConductorID != conductor.ID {
		return nil, fmt.Errorf("unauthorized to update this track")
	}

	// Update fields
	if req.Name != nil {
		track.Name = *req.Name
	}
	if req.Description != nil {
		track.Description = *req.Description
	}
	if req.RiskLevel != nil {
		track.RiskLevel = *req.RiskLevel
	}
	track.UpdatedAt = time.Now().UTC()

	if err := s.repo.UpdateTrack(ctx, track); err != nil {
		return nil, fmt.Errorf("failed to update track: %w", err)
	}

	// Update allocations if provided
	if len(req.Allocations) > 0 {
		totalWeight := decimal.Zero
		for _, alloc := range req.Allocations {
			totalWeight = totalWeight.Add(alloc.TargetWeight)
		}
		if !totalWeight.Equal(decimal.NewFromInt(100)) {
			return nil, fmt.Errorf("allocations must sum to 100%%")
		}

		// Delete old allocations and create new ones
		if err := s.repo.DeleteTrackAllocations(ctx, trackID); err != nil {
			return nil, fmt.Errorf("failed to delete old allocations: %w", err)
		}

		allocations := make([]entities.TrackAllocation, len(req.Allocations))
		now := time.Now().UTC()
		for i, alloc := range req.Allocations {
			allocations[i] = entities.TrackAllocation{
				ID:           uuid.New(),
				TrackID:      trackID,
				AssetTicker:  alloc.AssetTicker,
				AssetName:    alloc.AssetName,
				TargetWeight: alloc.TargetWeight,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
		}

		if err := s.repo.CreateTrackAllocations(ctx, allocations); err != nil {
			return nil, fmt.Errorf("failed to create allocations: %w", err)
		}

		track.Allocations = allocations
	}

	s.logger.Info("Track updated",
		zap.String("track_id", trackID.String()))

	return track, nil
}

// DeleteTrack deactivates a track
func (s *Service) DeleteTrack(ctx context.Context, userID, trackID uuid.UUID) error {
	track, err := s.repo.GetTrackByID(ctx, trackID)
	if err != nil {
		return fmt.Errorf("failed to get track: %w", err)
	}
	if track == nil {
		return fmt.Errorf("track not found")
	}

	// Verify ownership
	conductor, err := s.repo.GetConductorByUserID(ctx, userID)
	if err != nil || conductor == nil {
		return fmt.Errorf("user is not a conductor")
	}
	if track.ConductorID != conductor.ID {
		return fmt.Errorf("unauthorized to delete this track")
	}

	track.IsActive = false
	track.UpdatedAt = time.Now().UTC()

	if err := s.repo.UpdateTrack(ctx, track); err != nil {
		return fmt.Errorf("failed to deactivate track: %w", err)
	}

	s.logger.Info("Track deactivated",
		zap.String("track_id", trackID.String()))

	return nil
}
