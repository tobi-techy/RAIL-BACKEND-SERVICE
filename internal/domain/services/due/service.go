package due

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/adapters/due"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"go.uber.org/zap"
)

// Service handles Due account operations
type Service struct {
	dueRepo    DueAccountRepository
	dueAdapter DueAdapter
	logger     *zap.Logger
}

// DueAccountRepository interface for Due account persistence
type DueAccountRepository interface {
	Create(ctx context.Context, account *entities.DueAccount) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.DueAccount, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.DueAccount, error)
	GetByDueID(ctx context.Context, dueID string) (*entities.DueAccount, error)
	Update(ctx context.Context, account *entities.DueAccount) error
	Delete(ctx context.Context, id uuid.UUID) error
	ExistsByUserID(ctx context.Context, userID uuid.UUID) (bool, error)
}

// DueAdapter interface for Due API operations
type DueAdapter interface {
	CreateAccount(ctx context.Context, req due.CreateAccountRequest) (*due.Account, error)
	GetAccount(ctx context.Context, accountID string) (*due.Account, error)
}

// NewService creates a new Due service
func NewService(
	dueRepo DueAccountRepository,
	dueAdapter DueAdapter,
	logger *zap.Logger,
) *Service {
	return &Service{
		dueRepo:    dueRepo,
		dueAdapter: dueAdapter,
		logger:     logger,
	}
}

// CreateAccount creates a new Due account for a user
func (s *Service) CreateAccount(ctx context.Context, req *entities.CreateDueAccountRequest) (*entities.DueAccount, error) {
	s.logger.Info("Creating Due account for user",
		zap.String("user_id", req.UserID.String()),
		zap.String("type", string(req.Type)),
		zap.String("email", req.Email))

	// Check if user already has a Due account
	exists, err := s.dueRepo.ExistsByUserID(ctx, req.UserID)
	if err != nil {
		s.logger.Error("Failed to check if user already has Due account",
			zap.String("user_id", req.UserID.String()),
			zap.Error(err))
		return nil, fmt.Errorf("failed to check existing Due account: %w", err)
	}

	if exists {
		s.logger.Warn("User already has a Due account",
			zap.String("user_id", req.UserID.String()))
		return nil, fmt.Errorf("user already has a Due account")
	}

	// Prepare Due API request
	dueReq := due.CreateAccountRequest{
		Type:     string(req.Type),
		Name:     req.Name,
		Email:    req.Email,
		Country:  req.Country,
		Category: req.Category,
	}

	// Call Due API to create account
	dueAccount, err := s.dueAdapter.CreateAccount(ctx, dueReq)
	if err != nil {
		s.logger.Error("Failed to create Due account via API",
			zap.String("user_id", req.UserID.String()),
			zap.String("email", req.Email),
			zap.Error(err))
		return nil, fmt.Errorf("failed to create Due account: %w", err)
	}

	// Map Due API response to our domain entity
	now := time.Now()
	account := &entities.DueAccount{
		ID:          uuid.New(),
		UserID:      req.UserID,
		DueID:       dueAccount.ID,
		Type:        entities.DueAccountType(dueAccount.Type),
		Name:        dueAccount.Name,
		Email:       dueAccount.Email,
		Country:     dueAccount.Country,
		Category:    dueAccount.Category,
		Status:      entities.DueAccountStatus(dueAccount.Status),
		KYCStatus:   dueAccount.KYC.Status,
		TOSAccepted: nil, // Will be set when user accepts TOS
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Save to database
	if err := s.dueRepo.Create(ctx, account); err != nil {
		s.logger.Error("Failed to save Due account to database",
			zap.String("user_id", req.UserID.String()),
			zap.String("due_id", account.DueID),
			zap.Error(err))
		return nil, fmt.Errorf("failed to save Due account: %w", err)
	}

	s.logger.Info("Successfully created Due account",
		zap.String("account_id", account.ID.String()),
		zap.String("user_id", req.UserID.String()),
		zap.String("due_id", account.DueID),
		zap.String("status", string(account.Status)))

	return account, nil
}

// GetAccountByUserID retrieves a Due account by user ID
func (s *Service) GetAccountByUserID(ctx context.Context, userID uuid.UUID) (*entities.DueAccount, error) {
	s.logger.Info("Getting Due account for user",
		zap.String("user_id", userID.String()))

	account, err := s.dueRepo.GetByUserID(ctx, userID)
	if err != nil {
		s.logger.Error("Failed to get Due account by user ID",
			zap.String("user_id", userID.String()),
			zap.Error(err))
		return nil, fmt.Errorf("failed to get Due account: %w", err)
	}

	return account, nil
}

// GetAccountByID retrieves a Due account by its ID
func (s *Service) GetAccountByID(ctx context.Context, accountID uuid.UUID) (*entities.DueAccount, error) {
	s.logger.Info("Getting Due account by ID",
		zap.String("account_id", accountID.String()))

	account, err := s.dueRepo.GetByID(ctx, accountID)
	if err != nil {
		s.logger.Error("Failed to get Due account by ID",
			zap.String("account_id", accountID.String()),
			zap.Error(err))
		return nil, fmt.Errorf("failed to get Due account: %w", err)
	}

	return account, nil
}

// UpdateAccountStatus updates the status of a Due account
func (s *Service) UpdateAccountStatus(ctx context.Context, accountID uuid.UUID, status entities.DueAccountStatus) error {
	s.logger.Info("Updating Due account status",
		zap.String("account_id", accountID.String()),
		zap.String("status", string(status)))

	// Get current account
	account, err := s.dueRepo.GetByID(ctx, accountID)
	if err != nil {
		s.logger.Error("Failed to get Due account for update",
			zap.String("account_id", accountID.String()),
			zap.Error(err))
		return fmt.Errorf("failed to get Due account: %w", err)
	}

	// Update status and timestamp
	account.Status = status
	account.UpdatedAt = time.Now()

	// Save changes
	if err := s.dueRepo.Update(ctx, account); err != nil {
		s.logger.Error("Failed to update Due account status",
			zap.String("account_id", accountID.String()),
			zap.String("status", string(status)),
			zap.Error(err))
		return fmt.Errorf("failed to update Due account: %w", err)
	}

	s.logger.Info("Successfully updated Due account status",
		zap.String("account_id", accountID.String()),
		zap.String("status", string(status)))

	return nil
}

// SyncAccountStatus syncs the account status with Due API
func (s *Service) SyncAccountStatus(ctx context.Context, accountID uuid.UUID) error {
	s.logger.Info("Syncing Due account status with API",
		zap.String("account_id", accountID.String()))

	// Get local account
	account, err := s.dueRepo.GetByID(ctx, accountID)
	if err != nil {
		s.logger.Error("Failed to get Due account for sync",
			zap.String("account_id", accountID.String()),
			zap.Error(err))
		return fmt.Errorf("failed to get Due account: %w", err)
	}

	// Get latest status from Due API
	dueAccount, err := s.dueAdapter.GetAccount(ctx, account.DueID)
	if err != nil {
		s.logger.Error("Failed to get Due account from API",
			zap.String("account_id", accountID.String()),
			zap.String("due_id", account.DueID),
			zap.Error(err))
		return fmt.Errorf("failed to get Due account from API: %w", err)
	}

	// Update local status if changed
	if account.Status != entities.DueAccountStatus(dueAccount.Status) ||
	   account.KYCStatus != dueAccount.KYC.Status {

		account.Status = entities.DueAccountStatus(dueAccount.Status)
		account.KYCStatus = dueAccount.KYC.Status
		account.UpdatedAt = time.Now()

		if err := s.dueRepo.Update(ctx, account); err != nil {
			s.logger.Error("Failed to update Due account after sync",
				zap.String("account_id", accountID.String()),
				zap.Error(err))
			return fmt.Errorf("failed to update Due account: %w", err)
		}

		s.logger.Info("Successfully synced Due account status",
			zap.String("account_id", accountID.String()),
			zap.String("status", string(account.Status)),
			zap.String("kyc_status", account.KYCStatus))
	}

	return nil
}

// AcceptTOS marks the Terms of Service as accepted for a Due account
func (s *Service) AcceptTOS(ctx context.Context, accountID uuid.UUID) error {
	s.logger.Info("Accepting TOS for Due account",
		zap.String("account_id", accountID.String()))

	// Get current account
	account, err := s.dueRepo.GetByID(ctx, accountID)
	if err != nil {
		s.logger.Error("Failed to get Due account for TOS acceptance",
			zap.String("account_id", accountID.String()),
			zap.Error(err))
		return fmt.Errorf("failed to get Due account: %w", err)
	}

	// Set TOS accepted timestamp
	now := time.Now()
	account.TOSAccepted = &now
	account.UpdatedAt = now

	// Save changes
	if err := s.dueRepo.Update(ctx, account); err != nil {
		s.logger.Error("Failed to update Due account TOS acceptance",
			zap.String("account_id", accountID.String()),
			zap.Error(err))
		return fmt.Errorf("failed to update Due account: %w", err)
	}

	s.logger.Info("Successfully accepted TOS for Due account",
		zap.String("account_id", accountID.String()))

	return nil
}
