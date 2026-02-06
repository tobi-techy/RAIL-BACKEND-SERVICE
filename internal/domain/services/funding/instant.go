package funding

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// InstantFundingStatus represents the status of instant funding
type InstantFundingStatus string

const (
	InstantFundingStatusActive   InstantFundingStatus = "active"
	InstantFundingStatusSettled  InstantFundingStatus = "settled"
	InstantFundingStatusRepaid   InstantFundingStatus = "repaid"
)

// InstantFundingLimits defines limits based on account age
type InstantFundingLimits struct {
	NewAccount         decimal.Decimal // <7 days: $1,000
	VerifiedAccount    decimal.Decimal // 7-30 days: $10,000
	EstablishedAccount decimal.Decimal // >30 days: $50,000
}

var DefaultInstantFundingLimits = InstantFundingLimits{
	NewAccount:         decimal.NewFromInt(1000),
	VerifiedAccount:    decimal.NewFromInt(10000),
	EstablishedAccount: decimal.NewFromInt(50000),
}

// InstantFundingRequest is the simplified user-facing request
type InstantFundingRequest struct {
	Amount decimal.Decimal `json:"amount" binding:"required"`
}

// InstantFundingResponse is the simplified user-facing response
type InstantFundingResponse struct {
	Status             string          `json:"status"`
	InstantBuyingPower decimal.Decimal `json:"instant_buying_power"`
	Note               string          `json:"note"`
}

// InstantFundingStatusResponse shows current instant funding state
type InstantFundingStatusResponse struct {
	HasActiveInstantFunding bool            `json:"has_active_instant_funding"`
	TotalInstantBuyingPower decimal.Decimal `json:"total_instant_buying_power"`
	AvailableLimit          decimal.Decimal `json:"available_limit"`
	MaxLimit                decimal.Decimal `json:"max_limit"`
	Status                  string          `json:"status"` // none, active, settled
}

// InstantFunding represents an instant funding record (internal)
type InstantFunding struct {
	ID              uuid.UUID            `db:"id"`
	UserID          uuid.UUID            `db:"user_id"`
	AlpacaAccountID string               `db:"alpaca_account_id"`
	Amount          decimal.Decimal      `db:"amount"`
	JournalID       string               `db:"journal_id"`
	Status          InstantFundingStatus `db:"status"`
	CreatedAt       time.Time            `db:"created_at"`
	SettledAt       *time.Time           `db:"settled_at"`
}

// InstantFundingRepository interface for persistence
type InstantFundingRepository interface {
	Create(ctx context.Context, funding *InstantFunding) error
	GetActiveByUserID(ctx context.Context, userID uuid.UUID) ([]*InstantFunding, error)
	GetTotalActiveAmount(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status InstantFundingStatus) error
	MarkSettled(ctx context.Context, id uuid.UUID) error
	CountTodayRequests(ctx context.Context, userID uuid.UUID) (int, error)
}

// UserAccountRepository interface for account age lookup
type UserAccountRepository interface {
	GetCreatedAt(ctx context.Context, userID uuid.UUID) (time.Time, error)
}

// InstantFundingService handles simplified instant funding
type InstantFundingService struct {
	alpacaService      InstantFundingAlpacaAdapter
	virtualAccountRepo VirtualAccountRepository
	instantFundingRepo InstantFundingRepository
	userAccountRepo    UserAccountRepository
	logger             *zap.Logger
	firmAccountNumber  string
	limits             InstantFundingLimits
	maxRequestsPerDay  int
}

// InstantFundingAlpacaAdapter interface for Alpaca operations (instant funding only)
type InstantFundingAlpacaAdapter interface {
	CreateJournal(ctx context.Context, req *entities.AlpacaJournalRequest) (*entities.AlpacaJournalResponse, error)
}

func NewInstantFundingService(
	alpacaService InstantFundingAlpacaAdapter,
	virtualAccountRepo VirtualAccountRepository,
	instantFundingRepo InstantFundingRepository,
	userAccountRepo UserAccountRepository,
	logger *zap.Logger,
	firmAccountNumber string,
) *InstantFundingService {
	return &InstantFundingService{
		alpacaService:      alpacaService,
		virtualAccountRepo: virtualAccountRepo,
		instantFundingRepo: instantFundingRepo,
		userAccountRepo:    userAccountRepo,
		logger:             logger,
		firmAccountNumber:  firmAccountNumber,
		limits:             DefaultInstantFundingLimits,
		maxRequestsPerDay:  3,
	}
}

// RequestInstantFunding handles POST /funding/instant
// User sees: "I click fund → I have buying power → I trade"
func (s *InstantFundingService) RequestInstantFunding(ctx context.Context, userID uuid.UUID, req *InstantFundingRequest) (*InstantFundingResponse, error) {
	s.logger.Info("Processing instant funding request",
		zap.String("user_id", userID.String()),
		zap.String("amount", req.Amount.String()))

	// Validate amount
	if req.Amount.LessThanOrEqual(decimal.Zero) {
		return nil, fmt.Errorf("amount must be positive")
	}

	// Rate limit: max 3 requests per day
	todayCount, err := s.instantFundingRepo.CountTodayRequests(ctx, userID)
	if err != nil {
		s.logger.Error("Failed to check daily request count", zap.Error(err))
		return nil, fmt.Errorf("failed to validate request")
	}
	if todayCount >= s.maxRequestsPerDay {
		return nil, fmt.Errorf("daily instant funding limit reached (max %d requests/day)", s.maxRequestsPerDay)
	}

	// Get user's limit based on account age
	maxLimit, err := s.getUserLimit(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to determine funding limit: %w", err)
	}

	// Check current active instant funding
	activeTotal, err := s.instantFundingRepo.GetTotalActiveAmount(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to check active funding: %w", err)
	}

	availableLimit := maxLimit.Sub(activeTotal)
	if req.Amount.GreaterThan(availableLimit) {
		return nil, fmt.Errorf("amount exceeds available limit ($%s available)", availableLimit.StringFixed(2))
	}

	// Get user's Alpaca account
	virtualAccounts, err := s.virtualAccountRepo.GetByUserID(ctx, userID)
	if err != nil || len(virtualAccounts) == 0 {
		return nil, fmt.Errorf("no brokerage account found")
	}
	alpacaAccountID := virtualAccounts[0].AlpacaAccountID
	if alpacaAccountID == "" {
		return nil, fmt.Errorf("brokerage account not ready")
	}

	// Create journal entry: firm → user (instant buying power)
	journal, err := s.alpacaService.CreateJournal(ctx, &entities.AlpacaJournalRequest{
		FromAccount: s.firmAccountNumber,
		ToAccount:   alpacaAccountID,
		EntryType:   "JNLC",
		Amount:      req.Amount,
		Description: fmt.Sprintf("Instant funding for user %s", userID.String()),
	})
	if err != nil {
		s.logger.Error("Failed to create journal entry", zap.Error(err))
		return nil, fmt.Errorf("failed to process instant funding")
	}

	// Record instant funding
	funding := &InstantFunding{
		ID:              uuid.New(),
		UserID:          userID,
		AlpacaAccountID: alpacaAccountID,
		Amount:          req.Amount,
		JournalID:       journal.ID,
		Status:          InstantFundingStatusActive,
		CreatedAt:       time.Now(),
	}
	if err := s.instantFundingRepo.Create(ctx, funding); err != nil {
		s.logger.Error("Failed to record instant funding", zap.Error(err))
		// Journal already created - log for reconciliation but don't fail user
	}

	s.logger.Info("Instant funding approved",
		zap.String("user_id", userID.String()),
		zap.String("amount", req.Amount.String()),
		zap.String("journal_id", journal.ID))

	return &InstantFundingResponse{
		Status:             "approved",
		InstantBuyingPower: req.Amount,
		Note:               "Wire transfer pending - settles in 1-2 business days",
	}, nil
}

// GetInstantFundingStatus handles GET /funding/instant/status
func (s *InstantFundingService) GetInstantFundingStatus(ctx context.Context, userID uuid.UUID) (*InstantFundingStatusResponse, error) {
	maxLimit, err := s.getUserLimit(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to determine funding limit: %w", err)
	}

	activeTotal, err := s.instantFundingRepo.GetTotalActiveAmount(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to check active funding: %w", err)
	}

	status := "none"
	hasActive := activeTotal.GreaterThan(decimal.Zero)
	if hasActive {
		status = "active"
	}

	return &InstantFundingStatusResponse{
		HasActiveInstantFunding: hasActive,
		TotalInstantBuyingPower: activeTotal,
		AvailableLimit:          maxLimit.Sub(activeTotal),
		MaxLimit:                maxLimit,
		Status:                  status,
	}, nil
}

// getUserLimit returns the instant funding limit based on account age
func (s *InstantFundingService) getUserLimit(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	createdAt, err := s.userAccountRepo.GetCreatedAt(ctx, userID)
	if err != nil {
		return decimal.Zero, err
	}

	accountAge := time.Since(createdAt)
	switch {
	case accountAge < 7*24*time.Hour:
		return s.limits.NewAccount, nil // <7 days: $1,000
	case accountAge < 30*24*time.Hour:
		return s.limits.VerifiedAccount, nil // 7-30 days: $10,000
	default:
		return s.limits.EstablishedAccount, nil // >30 days: $50,000
	}
}

// SettleInstantFunding marks instant funding as settled (called by webhook)
func (s *InstantFundingService) SettleInstantFunding(ctx context.Context, fundingID uuid.UUID) error {
	return s.instantFundingRepo.MarkSettled(ctx, fundingID)
}

// FundBrokerageAccount is the legacy method for direct journal funding (used by allocation)
func (s *InstantFundingService) FundBrokerageAccount(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	s.logger.Info("Initiating brokerage funding",
		zap.String("user_id", userID.String()),
		zap.String("amount", amount.String()))

	virtualAccounts, err := s.virtualAccountRepo.GetByUserID(ctx, userID)
	if err != nil || len(virtualAccounts) == 0 || virtualAccounts[0].AlpacaAccountID == "" {
		return fmt.Errorf("user has no Alpaca account")
	}

	journal, err := s.alpacaService.CreateJournal(ctx, &entities.AlpacaJournalRequest{
		FromAccount: s.firmAccountNumber,
		ToAccount:   virtualAccounts[0].AlpacaAccountID,
		EntryType:   "JNLC",
		Amount:      amount,
		Description: fmt.Sprintf("Deposit funding for user %s", userID.String()),
	})
	if err != nil {
		return fmt.Errorf("create journal: %w", err)
	}

	s.logger.Info("Brokerage funding completed",
		zap.String("user_id", userID.String()),
		zap.String("journal_id", journal.ID))

	return nil
}
