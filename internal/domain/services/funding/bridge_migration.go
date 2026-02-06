package funding

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/rail-service/rail_service/pkg/logger"
)

// BridgeMigrationService handles migration of virtual accounts from legacy to Bridge
type BridgeMigrationService struct {
	bridgeClient       bridge.BridgeClient
	virtualAccountRepo MigrationVirtualAccountRepository
	userRepo           MigrationUserRepository
	logger             *logger.Logger
}

// MigrationVirtualAccountRepository defines repository operations for migration
type MigrationVirtualAccountRepository interface {
	GetAccountsForMigration(ctx context.Context, limit int) ([]*entities.VirtualAccount, error)
	UpdateBridgeAccountID(ctx context.Context, id uuid.UUID, bridgeAccountID string) error
	Update(ctx context.Context, account *entities.VirtualAccount) error
}

// MigrationUserRepository defines user repository operations for migration
type MigrationUserRepository interface {
	GetByID(ctx context.Context, userID uuid.UUID) (*entities.User, error)
	GetBridgeCustomerID(ctx context.Context, userID uuid.UUID) (string, error)
}

// NewBridgeMigrationService creates a new migration service
func NewBridgeMigrationService(
	bridgeClient bridge.BridgeClient,
	virtualAccountRepo MigrationVirtualAccountRepository,
	userRepo MigrationUserRepository,
	logger *logger.Logger,
) *BridgeMigrationService {
	return &BridgeMigrationService{
		bridgeClient:       bridgeClient,
		virtualAccountRepo: virtualAccountRepo,
		userRepo:           userRepo,
		logger:             logger,
	}
}

// MigrationResult represents the result of a migration batch
type MigrationResult struct {
	TotalProcessed int              `json:"total_processed"`
	Successful     int              `json:"successful"`
	Failed         int              `json:"failed"`
	Errors         []MigrationError `json:"errors,omitempty"`
	Duration       time.Duration    `json:"duration"`
}

// MigrationError represents a single migration failure
type MigrationError struct {
	VirtualAccountID uuid.UUID `json:"virtual_account_id"`
	UserID           uuid.UUID `json:"user_id"`
	Error            string    `json:"error"`
}

// MigrateAccounts migrates legacy virtual accounts to Bridge
// This maintains both providers during transition
func (s *BridgeMigrationService) MigrateAccounts(ctx context.Context, batchSize int) (*MigrationResult, error) {
	startTime := time.Now()
	result := &MigrationResult{}

	s.logger.Info("Starting legacy to Bridge migration", "batch_size", batchSize)

	// Get Due accounts that need migration (no Bridge account ID yet)
	accounts, err := s.virtualAccountRepo.GetAccountsForMigration(ctx, batchSize)
	if err != nil {
		return nil, fmt.Errorf("get accounts for migration: %w", err)
	}

	result.TotalProcessed = len(accounts)
	s.logger.Info("Found accounts for migration", "count", len(accounts))

	for _, account := range accounts {
		if err := s.migrateAccount(ctx, account); err != nil {
			result.Failed++
			result.Errors = append(result.Errors, MigrationError{
				VirtualAccountID: account.ID,
				UserID:           account.UserID,
				Error:            err.Error(),
			})
			s.logger.Error("Failed to migrate account",
				"virtual_account_id", account.ID,
				"user_id", account.UserID,
				"error", err)
			continue
		}
		result.Successful++
	}

	result.Duration = time.Since(startTime)

	s.logger.Info("Migration batch completed",
		"total", result.TotalProcessed,
		"successful", result.Successful,
		"failed", result.Failed,
		"duration", result.Duration)

	return result, nil
}

// migrateAccount migrates a single Due account to Bridge
func (s *BridgeMigrationService) migrateAccount(ctx context.Context, account *entities.VirtualAccount) error {
	// Get user's Bridge customer ID
	bridgeCustomerID, err := s.userRepo.GetBridgeCustomerID(ctx, account.UserID)
	if err != nil {
		return fmt.Errorf("get bridge customer id: %w", err)
	}

	if bridgeCustomerID == "" {
		return fmt.Errorf("user has no Bridge customer ID")
	}

	// Create corresponding Bridge virtual account
	bridgeReq := &bridge.CreateVirtualAccountRequest{
		Source: bridge.VirtualAccountSource{
			Currency: bridge.CurrencyUSD,
		},
		Destination: bridge.VirtualAccountDestination{
			Currency:    bridge.CurrencyUSDC,
			PaymentRail: bridge.PaymentRailSolana, // Solana-only support
		},
	}

	bridgeVA, err := s.bridgeClient.CreateVirtualAccount(ctx, bridgeCustomerID, bridgeReq)
	if err != nil {
		return fmt.Errorf("create bridge virtual account: %w", err)
	}

	// Update account with Bridge ID (keep Due ID for backward compatibility)
	account.BridgeAccountID = &bridgeVA.ID
	account.UpdatedAt = time.Now()

	if err := s.virtualAccountRepo.Update(ctx, account); err != nil {
		return fmt.Errorf("update virtual account: %w", err)
	}

	s.logger.Info("Account migrated successfully",
		"virtual_account_id", account.ID,
		"bridge_customer_id", account.BridgeCustomerID,
		"bridge_account_id", bridgeVA.ID)

	return nil
}

// GetMigrationStatus returns the current migration status
func (s *BridgeMigrationService) GetMigrationStatus(ctx context.Context) (*MigrationStatus, error) {
	accounts, err := s.virtualAccountRepo.GetAccountsForMigration(ctx, 10000)
	if err != nil {
		return nil, fmt.Errorf("get migration status: %w", err)
	}

	return &MigrationStatus{
		PendingMigration: len(accounts),
		LastChecked:      time.Now(),
	}, nil
}

// MigrationStatus represents the current state of migration
type MigrationStatus struct {
	PendingMigration int       `json:"pending_migration"`
	LastChecked      time.Time `json:"last_checked"`
}
