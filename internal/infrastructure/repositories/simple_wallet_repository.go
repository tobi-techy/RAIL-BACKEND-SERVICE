package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
)

// SimpleWalletRepository implements funding.WalletRepository interface using the wallets table
type SimpleWalletRepository struct {
	db     *sql.DB
	logger *logger.Logger
}

// NewSimpleWalletRepository creates a new simple wallet repository for funding service
func NewSimpleWalletRepository(db *sql.DB, logger *logger.Logger) *SimpleWalletRepository {
	return &SimpleWalletRepository{
		db:     db,
		logger: logger,
	}
}

// GetByUserAndChain retrieves a wallet by user ID and chain from wallets table
func (r *SimpleWalletRepository) GetByUserAndChain(ctx context.Context, userID uuid.UUID, chain entities.Chain) (*entities.Wallet, error) {
	query := `
		SELECT id, user_id, chain, address, provider_ref, status, created_at, updated_at
		FROM wallets 
		WHERE user_id = $1 AND chain = $2`

	wallet := &entities.Wallet{}

	err := r.db.QueryRowContext(ctx, query, userID, string(chain)).Scan(
		&wallet.ID,
		&wallet.UserID,
		&wallet.Chain,
		&wallet.Address,
		&wallet.ProviderRef,
		&wallet.Status,
		&wallet.CreatedAt,
		&wallet.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("wallet not found")
		}
		r.logger.Error("Failed to get wallet by user and chain", "error", err, "user_id", userID.String(), "chain", string(chain))
		return nil, fmt.Errorf("failed to get wallet: %w", err)
	}

	return wallet, nil
}

// GetByAddress retrieves a wallet by address from wallets table
func (r *SimpleWalletRepository) GetByAddress(ctx context.Context, address string) (*entities.Wallet, error) {
	query := `
		SELECT id, user_id, chain, address, provider_ref, status, created_at, updated_at
		FROM wallets 
		WHERE address = $1`

	wallet := &entities.Wallet{}

	err := r.db.QueryRowContext(ctx, query, address).Scan(
		&wallet.ID,
		&wallet.UserID,
		&wallet.Chain,
		&wallet.Address,
		&wallet.ProviderRef,
		&wallet.Status,
		&wallet.CreatedAt,
		&wallet.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("wallet not found")
		}
		r.logger.Error("Failed to get wallet by address", "error", err, "address", address)
		return nil, fmt.Errorf("failed to get wallet: %w", err)
	}

	return wallet, nil
}

// Create creates a new wallet in the wallets table
func (r *SimpleWalletRepository) Create(ctx context.Context, wallet *entities.Wallet) error {
	query := `
		INSERT INTO wallets (
			id, user_id, chain, address, provider_ref, status, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		)`

	_, err := r.db.ExecContext(ctx, query,
		wallet.ID,
		wallet.UserID,
		string(wallet.Chain),
		wallet.Address,
		wallet.ProviderRef,
		wallet.Status,
		wallet.CreatedAt,
		wallet.UpdatedAt,
	)

	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return fmt.Errorf("wallet already exists: %w", err)
		}
		r.logger.Error("Failed to create wallet", "error", err, "user_id", wallet.UserID.String())
		return fmt.Errorf("failed to create wallet: %w", err)
	}

	r.logger.Debug("Wallet created successfully", "wallet_id", wallet.ID.String())
	return nil
}

// GetByID retrieves a wallet by ID from wallets table
func (r *SimpleWalletRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.Wallet, error) {
	query := `
		SELECT id, user_id, chain, address, provider_ref, status, created_at, updated_at
		FROM wallets 
		WHERE id = $1`

	wallet := &entities.Wallet{}

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&wallet.ID,
		&wallet.UserID,
		&wallet.Chain,
		&wallet.Address,
		&wallet.ProviderRef,
		&wallet.Status,
		&wallet.CreatedAt,
		&wallet.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("wallet not found")
		}
		r.logger.Error("Failed to get wallet by ID", "error", err, "wallet_id", id.String())
		return nil, fmt.Errorf("failed to get wallet: %w", err)
	}

	return wallet, nil
}

// GetByUserID retrieves all wallets for a user from wallets table
func (r *SimpleWalletRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.Wallet, error) {
	query := `
		SELECT id, user_id, chain, address, provider_ref, status, created_at, updated_at
		FROM wallets 
		WHERE user_id = $1
		ORDER BY created_at ASC`

	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		r.logger.Error("Failed to get wallets by user ID", "error", err, "user_id", userID.String())
		return nil, fmt.Errorf("failed to get wallets: %w", err)
	}
	defer rows.Close()

	var wallets []*entities.Wallet
	for rows.Next() {
		wallet := &entities.Wallet{}
		err := rows.Scan(
			&wallet.ID,
			&wallet.UserID,
			&wallet.Chain,
			&wallet.Address,
			&wallet.ProviderRef,
			&wallet.Status,
			&wallet.CreatedAt,
			&wallet.UpdatedAt,
		)
		if err != nil {
			r.logger.Error("Failed to scan wallet", "error", err)
			return nil, fmt.Errorf("failed to scan wallet: %w", err)
		}
		wallets = append(wallets, wallet)
	}

	return wallets, nil
}

// UpdateStatus updates a wallet's status
func (r *SimpleWalletRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	query := `
		UPDATE wallets 
		SET status = $1, updated_at = $2 
		WHERE id = $3`

	result, err := r.db.ExecContext(ctx, query, status, time.Now(), id)
	if err != nil {
		r.logger.Error("Failed to update wallet status", "error", err, "wallet_id", id.String())
		return fmt.Errorf("failed to update wallet status: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("wallet not found")
	}

	r.logger.Debug("Wallet status updated", "wallet_id", id.String(), "status", status)
	return nil
}
