package transaction

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/database"
	"github.com/rail-service/rail_service/pkg/errors"
	"github.com/rail-service/rail_service/pkg/metrics"
	"go.uber.org/zap"
)

// Transaction represents a financial transaction
// Uses entities.TransactionType and entities.TransactionStatus for consistency
type Transaction struct {
	ID            uuid.UUID                   `json:"id"`
	UserID        uuid.UUID                   `json:"user_id"`
	Type          entities.TransactionType    `json:"type"`
	Status        entities.TransactionStatus  `json:"status"`
	Amount        decimal.Decimal             `json:"amount"`
	Currency      string                      `json:"currency"`
	FromAccount   *string                     `json:"from_account,omitempty"`
	ToAccount     *string                     `json:"to_account,omitempty"`
	Reference     *string                     `json:"reference,omitempty"`
	Metadata      map[string]interface{}      `json:"metadata,omitempty"`
	IdempotencyKey string                     `json:"idempotency_key"`
	CreatedAt     time.Time                   `json:"created_at"`
	UpdatedAt     time.Time                   `json:"updated_at"`
	ProcessedAt   *time.Time                  `json:"processed_at,omitempty"`
}

// AllocationService interface for spending enforcement
type AllocationService interface {
	CanSpend(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (bool, error)
	GetMode(ctx context.Context, userID uuid.UUID) (*entities.SmartAllocationMode, error)
	LogDeclinedSpending(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reason string) error
}

// AllocationNotificationManager interface for sending allocation notifications
type AllocationNotificationManager interface {
	NotifyTransactionDeclined(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, transactionType string) error
}

// Service handles transaction processing with idempotency and integrity
type Service struct {
	db                 *sql.DB
	allocationService  AllocationService
	allocationNotifier AllocationNotificationManager
	logger             *zap.Logger
	processed          map[string]*Transaction // In-memory cache for idempotency
	mu                 sync.RWMutex
}

// NewService creates a new transaction service
func NewService(db *sql.DB, allocationService AllocationService, allocationNotifier AllocationNotificationManager, logger *zap.Logger) *Service {
	return &Service{
		db:                 db,
		allocationService:  allocationService,
		allocationNotifier: allocationNotifier,
		logger:             logger,
		processed:          make(map[string]*Transaction),
	}
}

// ProcessTransaction processes a transaction with idempotency guarantees
func (s *Service) ProcessTransaction(ctx context.Context, tx *Transaction) (*Transaction, error) {
	// Check idempotency
	if existing := s.getProcessedTransaction(tx.IdempotencyKey); existing != nil {
		s.logger.Info("Transaction already processed", 
			zap.String("idempotency_key", tx.IdempotencyKey),
			zap.String("transaction_id", existing.ID.String()),
		)
		return existing, nil
	}

	// Validate transaction
	if err := s.validateTransaction(tx); err != nil {
		return nil, err
	}

	// Set defaults
	if tx.ID == uuid.Nil {
		tx.ID = uuid.New()
	}
	tx.Status = entities.TransactionStatusPending
	tx.CreatedAt = time.Now().UTC()
	tx.UpdatedAt = tx.CreatedAt

	// Process within database transaction
	err := database.WithTransaction(ctx, s.db, func(dbTx *sql.Tx) error {
		// Insert transaction record
		if err := s.insertTransaction(ctx, dbTx, tx); err != nil {
			return fmt.Errorf("failed to insert transaction: %w", err)
		}

		// Process based on transaction type
		switch tx.Type {
		case entities.TransactionTypeDeposit:
			return s.processDeposit(ctx, dbTx, tx)
		case entities.TransactionTypeWithdrawal:
			return s.processWithdrawal(ctx, dbTx, tx)
		case entities.TransactionTypeInvestment:
			return s.processInvestment(ctx, dbTx, tx)
		case entities.TransactionTypeInternalTransfer:
			return s.processTransfer(ctx, dbTx, tx)
		default:
			return &errors.AppError{
				Type:       errors.ErrorTypeValidation,
				Code:       errors.CodeInvalidValue,
				Message:    "unsupported transaction type",
				StatusCode: 400,
			}
		}
	})

	if err != nil {
		tx.Status = entities.TransactionStatusFailed
		s.updateTransactionStatus(ctx, tx.ID, entities.TransactionStatusFailed)
		
		metrics.RecordTransaction(string(tx.Type), "failed", tx.Currency, 0)
		return nil, err
	}

	// Mark as processed
	now := time.Now().UTC()
	tx.Status = entities.TransactionStatusCompleted
	tx.ProcessedAt = &now
	tx.UpdatedAt = now

	s.updateTransactionStatus(ctx, tx.ID, entities.TransactionStatusCompleted)
	s.setProcessedTransaction(tx.IdempotencyKey, tx)

	// Record metrics
	amount, _ := tx.Amount.Float64()
	metrics.RecordTransaction(string(tx.Type), "success", tx.Currency, amount)

	s.logger.Info("Transaction processed successfully",
		zap.String("transaction_id", tx.ID.String()),
		zap.String("type", string(tx.Type)),
		zap.String("amount", tx.Amount.String()),
		zap.String("currency", tx.Currency),
	)

	return tx, nil
}

// validateTransaction validates transaction data
func (s *Service) validateTransaction(tx *Transaction) error {
	if tx.UserID == uuid.Nil {
		return &errors.AppError{
			Type:       errors.ErrorTypeValidation,
			Code:       errors.CodeMissingField,
			Message:    "user_id is required",
			StatusCode: 400,
		}
	}

	if tx.Amount.IsZero() || tx.Amount.IsNegative() {
		return &errors.AppError{
			Type:       errors.ErrorTypeValidation,
			Code:       errors.CodeInvalidAmount,
			Message:    "amount must be positive",
			StatusCode: 400,
		}
	}

	if tx.Currency == "" {
		return &errors.AppError{
			Type:       errors.ErrorTypeValidation,
			Code:       errors.CodeMissingField,
			Message:    "currency is required",
			StatusCode: 400,
		}
	}

	if tx.IdempotencyKey == "" {
		return &errors.AppError{
			Type:       errors.ErrorTypeValidation,
			Code:       errors.CodeMissingField,
			Message:    "idempotency_key is required",
			StatusCode: 400,
		}
	}

	return nil
}

// insertTransaction inserts transaction into database
func (s *Service) insertTransaction(ctx context.Context, tx *sql.Tx, transaction *Transaction) error {
	query := `
		INSERT INTO transactions (
			id, user_id, type, status, amount, currency, from_account, to_account,
			reference, idempotency_key, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`

	_, err := tx.ExecContext(ctx, query,
		transaction.ID, transaction.UserID, transaction.Type, transaction.Status,
		transaction.Amount, transaction.Currency, transaction.FromAccount, transaction.ToAccount,
		transaction.Reference, transaction.IdempotencyKey, transaction.CreatedAt, transaction.UpdatedAt,
	)

	return err
}

// updateTransactionStatus updates transaction status
func (s *Service) updateTransactionStatus(ctx context.Context, id uuid.UUID, status entities.TransactionStatus) error {
	query := `UPDATE transactions SET status = $1, updated_at = $2 WHERE id = $3`
	_, err := s.db.ExecContext(ctx, query, status, time.Now().UTC(), id)
	return err
}

// processDeposit handles deposit transactions
func (s *Service) processDeposit(ctx context.Context, tx *sql.Tx, transaction *Transaction) error {
	// Update user balance
	query := `
		INSERT INTO balances (user_id, currency, amount, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, currency)
		DO UPDATE SET 
			amount = balances.amount + EXCLUDED.amount,
			updated_at = EXCLUDED.updated_at`

	_, err := tx.ExecContext(ctx, query,
		transaction.UserID, transaction.Currency, transaction.Amount, time.Now().UTC(),
	)

	return err
}

// processWithdrawal handles withdrawal transactions
func (s *Service) processWithdrawal(ctx context.Context, tx *sql.Tx, transaction *Transaction) error {
	// Check 70/30 allocation mode spending limit
	if s.allocationService != nil {
		canSpend, err := s.allocationService.CanSpend(ctx, transaction.UserID, transaction.Amount)
		if err != nil {
			s.logger.Error("Failed to check spending limit", 
				zap.String("user_id", transaction.UserID.String()),
				zap.Error(err))
			return fmt.Errorf("failed to check spending limit: %w", err)
		}

		if !canSpend {
			s.logger.Warn("Withdrawal declined - spending limit reached",
				zap.String("user_id", transaction.UserID.String()),
				zap.String("amount", transaction.Amount.String()))
			
			// Log declined spending event
			_ = s.allocationService.LogDeclinedSpending(ctx, transaction.UserID, transaction.Amount, "withdrawal")
			
			// Send notification to user
			if s.allocationNotifier != nil {
				_ = s.allocationNotifier.NotifyTransactionDeclined(ctx, transaction.UserID, transaction.Amount, "withdrawal")
			}
			
			return entities.ErrSpendingLimitReached
		}
	}

	// Check sufficient balance
	var currentBalance decimal.Decimal
	query := `SELECT COALESCE(amount, 0) FROM balances WHERE user_id = $1 AND currency = $2`
	err := tx.QueryRowContext(ctx, query, transaction.UserID, transaction.Currency).Scan(&currentBalance)
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	if currentBalance.LessThan(transaction.Amount) {
		return errors.ErrInsufficientFunds
	}

	// Update balance
	query = `
		UPDATE balances 
		SET amount = amount - $1, updated_at = $2 
		WHERE user_id = $3 AND currency = $4`

	_, err = tx.ExecContext(ctx, query,
		transaction.Amount, time.Now().UTC(), transaction.UserID, transaction.Currency,
	)

	return err
}

// processInvestment handles investment transactions
func (s *Service) processInvestment(ctx context.Context, tx *sql.Tx, transaction *Transaction) error {
	// Check 70/30 allocation mode spending limit (same as withdrawal)
	if s.allocationService != nil {
		canSpend, err := s.allocationService.CanSpend(ctx, transaction.UserID, transaction.Amount)
		if err != nil {
			s.logger.Error("Failed to check spending limit", 
				zap.String("user_id", transaction.UserID.String()),
				zap.Error(err))
			return fmt.Errorf("failed to check spending limit: %w", err)
		}

		if !canSpend {
			s.logger.Warn("Investment declined - spending limit reached",
				zap.String("user_id", transaction.UserID.String()),
				zap.String("amount", transaction.Amount.String()))
			
			// Log declined spending event
			_ = s.allocationService.LogDeclinedSpending(ctx, transaction.UserID, transaction.Amount, "investment")
			
			// Send notification to user
			if s.allocationNotifier != nil {
				_ = s.allocationNotifier.NotifyTransactionDeclined(ctx, transaction.UserID, transaction.Amount, "investment")
			}
			
			return entities.ErrSpendingLimitReached
		}
	}

	// Similar to withdrawal - deduct from cash balance
	return s.processWithdrawal(ctx, tx, transaction)
}

// processTransfer handles transfer transactions
func (s *Service) processTransfer(ctx context.Context, tx *sql.Tx, transaction *Transaction) error {
	// This would involve more complex logic for transfers between accounts
	// For now, implement as a simple withdrawal
	return s.processWithdrawal(ctx, tx, transaction)
}

// getProcessedTransaction retrieves processed transaction by idempotency key
func (s *Service) getProcessedTransaction(key string) *Transaction {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.processed[key]
}

// setProcessedTransaction stores processed transaction
func (s *Service) setProcessedTransaction(key string, tx *Transaction) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.processed[key] = tx
}

// GetTransaction retrieves a transaction by ID
func (s *Service) GetTransaction(ctx context.Context, id uuid.UUID) (*Transaction, error) {
	query := `
		SELECT id, user_id, type, status, amount, currency, from_account, to_account,
			   reference, idempotency_key, created_at, updated_at, processed_at
		FROM transactions 
		WHERE id = $1`

	var tx Transaction
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&tx.ID, &tx.UserID, &tx.Type, &tx.Status, &tx.Amount, &tx.Currency,
		&tx.FromAccount, &tx.ToAccount, &tx.Reference, &tx.IdempotencyKey,
		&tx.CreatedAt, &tx.UpdatedAt, &tx.ProcessedAt,
	)

	if err == sql.ErrNoRows {
		return nil, errors.ErrNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get transaction: %w", err)
	}

	return &tx, nil
}

// GetUserTransactions retrieves transactions for a user
func (s *Service) GetUserTransactions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Transaction, error) {
	query := `
		SELECT id, user_id, type, status, amount, currency, from_account, to_account,
			   reference, idempotency_key, created_at, updated_at, processed_at
		FROM transactions 
		WHERE user_id = $1 
		ORDER BY created_at DESC 
		LIMIT $2 OFFSET $3`

	rows, err := s.db.QueryContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var transactions []*Transaction
	for rows.Next() {
		var tx Transaction
		err := rows.Scan(
			&tx.ID, &tx.UserID, &tx.Type, &tx.Status, &tx.Amount, &tx.Currency,
			&tx.FromAccount, &tx.ToAccount, &tx.Reference, &tx.IdempotencyKey,
			&tx.CreatedAt, &tx.UpdatedAt, &tx.ProcessedAt,
		)
		if err != nil {
			return nil, err
		}
		transactions = append(transactions, &tx)
	}

	return transactions, nil
}
