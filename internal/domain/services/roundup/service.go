package roundup

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// Repository interface for round-up persistence
type Repository interface {
	GetSettings(ctx context.Context, userID uuid.UUID) (*entities.RoundupSettings, error)
	UpsertSettings(ctx context.Context, settings *entities.RoundupSettings) error
	CreateTransaction(ctx context.Context, tx *entities.RoundupTransaction) error
	UpdateTransactionStatus(ctx context.Context, id uuid.UUID, status entities.RoundupStatus, orderID *uuid.UUID) error
	GetPendingTransactions(ctx context.Context, userID uuid.UUID) ([]*entities.RoundupTransaction, error)
	GetTransactions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.RoundupTransaction, error)
	CountTransactions(ctx context.Context, userID uuid.UUID) (int, error)
	GetAccumulator(ctx context.Context, userID uuid.UUID) (*entities.RoundupAccumulator, error)
	UpsertAccumulator(ctx context.Context, acc *entities.RoundupAccumulator) error
	GetUsersReadyForAutoInvest(ctx context.Context) ([]uuid.UUID, error)
}

// AllocationService interface for processing round-up funds
type AllocationService interface {
	ProcessIncomingFunds(ctx context.Context, req *entities.IncomingFundsRequest) error
}

// OrderPlacer interface for placing investment orders
type OrderPlacer interface {
	PlaceMarketOrder(ctx context.Context, userID uuid.UUID, symbol string, notional decimal.Decimal) (*entities.InvestmentOrder, error)
}

// ContributionRecorder interface for recording contributions
type ContributionRecorder interface {
	RecordContribution(ctx context.Context, userID uuid.UUID, contributionType entities.ContributionType, amount decimal.Decimal, source string) error
}

// Service handles round-up operations
type Service struct {
	repo                 Repository
	allocationService    AllocationService
	orderPlacer          OrderPlacer
	contributionRecorder ContributionRecorder
	logger               *zap.Logger
}

// NewService creates a new round-up service
func NewService(
	repo Repository,
	allocationService AllocationService,
	orderPlacer OrderPlacer,
	contributionRecorder ContributionRecorder,
	logger *zap.Logger,
) *Service {
	return &Service{
		repo:                 repo,
		allocationService:    allocationService,
		orderPlacer:          orderPlacer,
		contributionRecorder: contributionRecorder,
		logger:               logger,
	}
}

// GetSettings retrieves round-up settings for a user
func (s *Service) GetSettings(ctx context.Context, userID uuid.UUID) (*entities.RoundupSettings, error) {
	settings, err := s.repo.GetSettings(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get settings: %w", err)
	}
	if settings == nil {
		return entities.DefaultRoundupSettings(userID), nil
	}
	return settings, nil
}

// UpdateSettings updates round-up settings
func (s *Service) UpdateSettings(ctx context.Context, userID uuid.UUID, req *UpdateSettingsRequest) (*entities.RoundupSettings, error) {
	settings, err := s.GetSettings(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Apply updates
	if req.Enabled != nil {
		settings.Enabled = *req.Enabled
	}
	if req.Multiplier != nil {
		settings.Multiplier = *req.Multiplier
	}
	if req.Threshold != nil {
		settings.Threshold = *req.Threshold
	}
	if req.AutoInvestEnabled != nil {
		settings.AutoInvestEnabled = *req.AutoInvestEnabled
	}
	if req.AutoInvestBasketID != nil {
		settings.AutoInvestBasketID = req.AutoInvestBasketID
		settings.AutoInvestSymbol = nil
	}
	if req.AutoInvestSymbol != nil {
		settings.AutoInvestSymbol = req.AutoInvestSymbol
		settings.AutoInvestBasketID = nil
	}

	if err := settings.Validate(); err != nil {
		return nil, fmt.Errorf("invalid settings: %w", err)
	}

	if err := s.repo.UpsertSettings(ctx, settings); err != nil {
		return nil, fmt.Errorf("save settings: %w", err)
	}

	s.logger.Info("Round-up settings updated",
		zap.String("user_id", userID.String()),
		zap.Bool("enabled", settings.Enabled),
		zap.String("multiplier", settings.Multiplier.String()))

	return settings, nil
}

// ProcessTransaction processes a card/bank transaction and calculates round-up
func (s *Service) ProcessTransaction(ctx context.Context, req *ProcessTransactionRequest) (*entities.RoundupTransaction, error) {
	settings, err := s.GetSettings(ctx, req.UserID)
	if err != nil {
		return nil, err
	}

	if !settings.Enabled {
		return nil, fmt.Errorf("round-ups not enabled for user")
	}

	// Calculate round-up
	rounded, spareChange, multiplied := entities.CalculateRoundup(req.Amount, settings.Multiplier)

	now := time.Now()
	tx := &entities.RoundupTransaction{
		ID:               uuid.New(),
		UserID:           req.UserID,
		OriginalAmount:   req.Amount,
		RoundedAmount:    rounded,
		SpareChange:      spareChange,
		MultipliedAmount: multiplied,
		SourceType:       req.SourceType,
		SourceRef:        req.SourceRef,
		MerchantName:     req.MerchantName,
		Status:           entities.RoundupStatusPending,
		CreatedAt:        now,
	}

	if err := s.repo.CreateTransaction(ctx, tx); err != nil {
		return nil, fmt.Errorf("create transaction: %w", err)
	}

	// Update accumulator
	acc, err := s.repo.GetAccumulator(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("get accumulator: %w", err)
	}

	acc.PendingAmount = acc.PendingAmount.Add(multiplied)
	if err := s.repo.UpsertAccumulator(ctx, acc); err != nil {
		return nil, fmt.Errorf("update accumulator: %w", err)
	}

	s.logger.Info("Round-up transaction processed",
		zap.String("user_id", req.UserID.String()),
		zap.String("original", req.Amount.String()),
		zap.String("spare_change", spareChange.String()),
		zap.String("multiplied", multiplied.String()),
		zap.String("pending_total", acc.PendingAmount.String()))

	// Check if threshold reached for auto-invest
	if settings.AutoInvestEnabled && acc.PendingAmount.GreaterThanOrEqual(settings.Threshold) {
		go s.triggerAutoInvest(context.Background(), req.UserID)
	}

	return tx, nil
}

// CollectPendingRoundups collects pending round-ups and moves to allocation
func (s *Service) CollectPendingRoundups(ctx context.Context, userID uuid.UUID) error {
	acc, err := s.repo.GetAccumulator(ctx, userID)
	if err != nil {
		return fmt.Errorf("get accumulator: %w", err)
	}

	if acc.PendingAmount.IsZero() {
		return nil
	}

	// Process through allocation service (70/30 split if enabled)
	if s.allocationService != nil {
		err = s.allocationService.ProcessIncomingFunds(ctx, &entities.IncomingFundsRequest{
			UserID:    userID,
			Amount:    acc.PendingAmount,
			EventType: entities.AllocationEventTypeRoundup,
		})
		if err != nil {
			return fmt.Errorf("process allocation: %w", err)
		}
	}

	// Record contribution
	if s.contributionRecorder != nil {
		s.contributionRecorder.RecordContribution(ctx, userID, entities.ContributionTypeRoundup, acc.PendingAmount, "roundup_collection")
	}

	// Update pending transactions to collected
	pendingTxs, err := s.repo.GetPendingTransactions(ctx, userID)
	if err != nil {
		return fmt.Errorf("get pending transactions: %w", err)
	}

	for _, tx := range pendingTxs {
		s.repo.UpdateTransactionStatus(ctx, tx.ID, entities.RoundupStatusCollected, nil)
	}

	// Update accumulator
	now := time.Now()
	acc.TotalCollected = acc.TotalCollected.Add(acc.PendingAmount)
	acc.LastCollectionAt = &now
	acc.PendingAmount = decimal.Zero

	if err := s.repo.UpsertAccumulator(ctx, acc); err != nil {
		return fmt.Errorf("update accumulator: %w", err)
	}

	s.logger.Info("Round-ups collected",
		zap.String("user_id", userID.String()),
		zap.String("amount", acc.TotalCollected.String()))

	return nil
}

// triggerAutoInvest triggers auto-investment when threshold is reached
func (s *Service) triggerAutoInvest(ctx context.Context, userID uuid.UUID) {
	settings, err := s.GetSettings(ctx, userID)
	if err != nil || !settings.AutoInvestEnabled {
		return
	}

	acc, err := s.repo.GetAccumulator(ctx, userID)
	if err != nil || acc.PendingAmount.LessThan(settings.Threshold) {
		return
	}

	investAmount := acc.PendingAmount

	// Place order
	var order *entities.InvestmentOrder
	if settings.AutoInvestSymbol != nil && s.orderPlacer != nil {
		order, err = s.orderPlacer.PlaceMarketOrder(ctx, userID, *settings.AutoInvestSymbol, investAmount)
		if err != nil {
			s.logger.Error("Auto-invest order failed", zap.Error(err), zap.String("user_id", userID.String()))
			return
		}
	}

	// Update transactions to invested
	pendingTxs, _ := s.repo.GetPendingTransactions(ctx, userID)
	var orderID *uuid.UUID
	if order != nil {
		orderID = &order.ID
	}
	for _, tx := range pendingTxs {
		s.repo.UpdateTransactionStatus(ctx, tx.ID, entities.RoundupStatusInvested, orderID)
	}

	// Update accumulator
	now := time.Now()
	acc.TotalInvested = acc.TotalInvested.Add(investAmount)
	acc.LastInvestmentAt = &now
	acc.PendingAmount = decimal.Zero
	s.repo.UpsertAccumulator(ctx, acc)

	// Record contribution
	if s.contributionRecorder != nil {
		s.contributionRecorder.RecordContribution(ctx, userID, entities.ContributionTypeRoundup, investAmount, "auto_invest")
	}

	s.logger.Info("Auto-invest triggered",
		zap.String("user_id", userID.String()),
		zap.String("amount", investAmount.String()),
		zap.String("symbol", *settings.AutoInvestSymbol))
}

// ProcessAutoInvestBatch processes auto-invest for all eligible users
func (s *Service) ProcessAutoInvestBatch(ctx context.Context) error {
	userIDs, err := s.repo.GetUsersReadyForAutoInvest(ctx)
	if err != nil {
		return fmt.Errorf("get eligible users: %w", err)
	}

	s.logger.Info("Processing auto-invest batch", zap.Int("user_count", len(userIDs)))

	for _, userID := range userIDs {
		s.triggerAutoInvest(ctx, userID)
	}

	return nil
}

// GetSummary returns a summary of round-up activity for a user
func (s *Service) GetSummary(ctx context.Context, userID uuid.UUID) (*entities.RoundupSummary, error) {
	settings, err := s.GetSettings(ctx, userID)
	if err != nil {
		return nil, err
	}

	acc, err := s.repo.GetAccumulator(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get accumulator: %w", err)
	}

	count, err := s.repo.CountTransactions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("count transactions: %w", err)
	}

	return &entities.RoundupSummary{
		Settings:         settings,
		PendingAmount:    acc.PendingAmount,
		TotalCollected:   acc.TotalCollected,
		TotalInvested:    acc.TotalInvested,
		TransactionCount: count,
	}, nil
}

// GetTransactions returns paginated transactions
func (s *Service) GetTransactions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.RoundupTransaction, error) {
	if limit <= 0 {
		limit = 20
	}
	return s.repo.GetTransactions(ctx, userID, limit, offset)
}

// UpdateSettingsRequest represents a request to update settings
type UpdateSettingsRequest struct {
	Enabled            *bool            `json:"enabled,omitempty"`
	Multiplier         *decimal.Decimal `json:"multiplier,omitempty"`
	Threshold          *decimal.Decimal `json:"threshold,omitempty"`
	AutoInvestEnabled  *bool            `json:"auto_invest_enabled,omitempty"`
	AutoInvestBasketID *uuid.UUID       `json:"auto_invest_basket_id,omitempty"`
	AutoInvestSymbol   *string          `json:"auto_invest_symbol,omitempty"`
}

// ProcessTransactionRequest represents a request to process a transaction
type ProcessTransactionRequest struct {
	UserID       uuid.UUID
	Amount       decimal.Decimal
	SourceType   entities.RoundupSourceType
	SourceRef    *string
	MerchantName *string
}
