package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// WalletListFilters represents filters for wallet listing
type WalletListFilters struct {
	UserID      *uuid.UUID                  `json:"user_id,omitempty"`
	WalletSetID *uuid.UUID                  `json:"wallet_set_id,omitempty"`
	Chain       *entities.WalletChain       `json:"chain,omitempty"`
	AccountType *entities.WalletAccountType `json:"account_type,omitempty"`
	Status      *entities.WalletStatus      `json:"status,omitempty"`
	Limit       int                         `json:"limit"`
	Offset      int                         `json:"offset"`
}

// WalletRepository implements the wallet repository interface using PostgreSQL
type WalletRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewWalletRepository creates a new wallet repository
func NewWalletRepository(db *sql.DB, logger *zap.Logger) *WalletRepository {
	return &WalletRepository{
		db:     db,
		logger: logger,
	}
}

// Create creates a new managed wallet
func (r *WalletRepository) Create(ctx context.Context, wallet *entities.ManagedWallet) error {
	query := `
		INSERT INTO managed_wallets (
			id, user_id, wallet_set_id, circle_wallet_id, chain, 
			address, status, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9
		)`

	_, err := r.db.ExecContext(ctx, query,
		wallet.ID,
		wallet.UserID,
		wallet.WalletSetID,
		wallet.CircleWalletID,
		string(wallet.Chain),
		wallet.Address,
		string(wallet.Status),
		wallet.CreatedAt,
		wallet.UpdatedAt,
	)

	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return fmt.Errorf("wallet already exists: %w", err)
		}
		r.logger.Error("Failed to create wallet", zap.Error(err), zap.String("user_id", wallet.UserID.String()))
		return fmt.Errorf("failed to create wallet: %w", err)
	}

	r.logger.Debug("Wallet created successfully", zap.String("wallet_id", wallet.ID.String()))
	return nil
}

// GetByID retrieves a wallet by ID
func (r *WalletRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.ManagedWallet, error) {
	query := `
		SELECT id, user_id, wallet_set_id, circle_wallet_id, chain, 
		       address, status, created_at, updated_at
		FROM managed_wallets 
		WHERE id = $1`

	wallet := &entities.ManagedWallet{}

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&wallet.ID,
		&wallet.UserID,
		&wallet.WalletSetID,
		&wallet.CircleWalletID,
		&wallet.Chain,
		&wallet.Address,
		&wallet.Status,
		&wallet.CreatedAt,
		&wallet.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("wallet not found")
		}
		r.logger.Error("Failed to get wallet by ID", zap.Error(err), zap.String("wallet_id", id.String()))
		return nil, fmt.Errorf("failed to get wallet: %w", err)
	}

	return wallet, nil
}

// GetByUserID retrieves all wallets for a user
func (r *WalletRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.ManagedWallet, error) {
	query := `
		SELECT id, user_id, wallet_set_id, circle_wallet_id, chain, 
		       address, status, created_at, updated_at
		FROM managed_wallets 
		WHERE user_id = $1
		ORDER BY created_at ASC`

	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		r.logger.Error("Failed to get wallets by user ID", zap.Error(err), zap.String("user_id", userID.String()))
		return nil, fmt.Errorf("failed to get wallets: %w", err)
	}
	defer rows.Close()

	var wallets []*entities.ManagedWallet
	for rows.Next() {
		wallet := &entities.ManagedWallet{}
		err := rows.Scan(
			&wallet.ID,
			&wallet.UserID,
			&wallet.WalletSetID,
			&wallet.CircleWalletID,
			&wallet.Chain,
			&wallet.Address,
			&wallet.Status,
			&wallet.CreatedAt,
			&wallet.UpdatedAt,
		)
		if err != nil {
			r.logger.Error("Failed to scan wallet", zap.Error(err))
			return nil, fmt.Errorf("failed to scan wallet: %w", err)
		}
		wallets = append(wallets, wallet)
	}

	return wallets, nil
}

// GetByUserAndChain retrieves a wallet by user ID and chain
func (r *WalletRepository) GetByUserAndChain(ctx context.Context, userID uuid.UUID, chain entities.WalletChain) (*entities.ManagedWallet, error) {
	query := `
		SELECT id, user_id, wallet_set_id, circle_wallet_id, chain, 
		       address, status, created_at, updated_at
		FROM managed_wallets 
		WHERE user_id = $1 AND chain = $2`

	wallet := &entities.ManagedWallet{}

	err := r.db.QueryRowContext(ctx, query, userID, string(chain)).Scan(
		&wallet.ID,
		&wallet.UserID,
		&wallet.WalletSetID,
		&wallet.CircleWalletID,
		&wallet.Chain,
		&wallet.Address,
		&wallet.Status,
		&wallet.CreatedAt,
		&wallet.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("wallet not found")
		}
		r.logger.Error("Failed to get wallet by user and chain", zap.Error(err),
			zap.String("user_id", userID.String()), zap.String("chain", string(chain)))
		return nil, fmt.Errorf("failed to get wallet: %w", err)
	}

	return wallet, nil
}

// GetByCircleWalletID retrieves a wallet by Circle wallet ID
func (r *WalletRepository) GetByCircleWalletID(ctx context.Context, circleWalletID string) (*entities.ManagedWallet, error) {
	query := `
		SELECT id, user_id, wallet_set_id, circle_wallet_id, chain, 
		       address, status, created_at, updated_at
		FROM managed_wallets 
		WHERE circle_wallet_id = $1`

	wallet := &entities.ManagedWallet{}

	err := r.db.QueryRowContext(ctx, query, circleWalletID).Scan(
		&wallet.ID,
		&wallet.UserID,
		&wallet.WalletSetID,
		&wallet.CircleWalletID,
		&wallet.Chain,
		&wallet.Address,
		&wallet.Status,
		&wallet.CreatedAt,
		&wallet.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("wallet not found")
		}
		r.logger.Error("Failed to get wallet by Circle wallet ID", zap.Error(err),
			zap.String("circle_wallet_id", circleWalletID))
		return nil, fmt.Errorf("failed to get wallet: %w", err)
	}

	return wallet, nil
}

// Update updates a managed wallet
func (r *WalletRepository) Update(ctx context.Context, wallet *entities.ManagedWallet) error {
	query := `
		UPDATE managed_wallets SET 
			wallet_set_id = $2, circle_wallet_id = $3, chain = $4, 
			address = $5, status = $6, updated_at = $7
		WHERE id = $1`

	_, err := r.db.ExecContext(ctx, query,
		wallet.ID,
		wallet.WalletSetID,
		wallet.CircleWalletID,
		string(wallet.Chain),
		wallet.Address,
		string(wallet.Status),
		time.Now(),
	)

	if err != nil {
		r.logger.Error("Failed to update wallet", zap.Error(err), zap.String("wallet_id", wallet.ID.String()))
		return fmt.Errorf("failed to update wallet: %w", err)
	}

	r.logger.Debug("Wallet updated successfully", zap.String("wallet_id", wallet.ID.String()))
	return nil
}

// UpdateStatus updates only the status of a wallet
func (r *WalletRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status entities.WalletStatus) error {
	query := `UPDATE managed_wallets SET status = $2, updated_at = $3 WHERE id = $1`

	_, err := r.db.ExecContext(ctx, query, id, string(status), time.Now())
	if err != nil {
		r.logger.Error("Failed to update wallet status", zap.Error(err), zap.String("wallet_id", id.String()))
		return fmt.Errorf("failed to update wallet status: %w", err)
	}

	r.logger.Debug("Wallet status updated", zap.String("wallet_id", id.String()), zap.String("status", string(status)))
	return nil
}

// WalletSetRepository implements the wallet set repository interface using PostgreSQL
type WalletSetRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewWalletSetRepository creates a new wallet set repository
func NewWalletSetRepository(db *sql.DB, logger *zap.Logger) *WalletSetRepository {
	return &WalletSetRepository{
		db:     db,
		logger: logger,
	}
}

// Create creates a new wallet set
func (r *WalletSetRepository) Create(ctx context.Context, walletSet *entities.WalletSet) error {
	query := `
		INSERT INTO wallet_sets (
			id, name, circle_wallet_set_id, entity_secret_ciphertext, status, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7
		)`

	_, err := r.db.ExecContext(ctx, query,
		walletSet.ID,
		walletSet.Name,
		walletSet.CircleWalletSetID,
		walletSet.EntitySecretCiphertext,
		string(walletSet.Status),
		walletSet.CreatedAt,
		walletSet.UpdatedAt,
	)

	if err != nil {
		r.logger.Error("Failed to create wallet set", zap.Error(err), zap.String("id", walletSet.ID.String()))
		return fmt.Errorf("failed to create wallet set: %w", err)
	}

	r.logger.Debug("Wallet set created successfully", zap.String("wallet_set_id", walletSet.ID.String()))
	return nil
}

// GetByID retrieves a wallet set by ID
func (r *WalletSetRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.WalletSet, error) {
	query := `
		SELECT id, name, circle_wallet_set_id, status, created_at, updated_at
		FROM wallet_sets 
		WHERE id = $1`

	walletSet := &entities.WalletSet{}

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&walletSet.ID,
		&walletSet.Name,
		&walletSet.CircleWalletSetID,
		&walletSet.Status,
		&walletSet.CreatedAt,
		&walletSet.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("wallet set not found")
		}
		r.logger.Error("Failed to get wallet set by ID", zap.Error(err), zap.String("wallet_set_id", id.String()))
		return nil, fmt.Errorf("failed to get wallet set: %w", err)
	}

	return walletSet, nil
}

// GetByCircleWalletSetID retrieves a wallet set by Circle wallet set ID
func (r *WalletSetRepository) GetByCircleWalletSetID(ctx context.Context, circleWalletSetID string) (*entities.WalletSet, error) {
	query := `
		SELECT id, name, circle_wallet_set_id, status, created_at, updated_at
		FROM wallet_sets 
		WHERE circle_wallet_set_id = $1`

	walletSet := &entities.WalletSet{}

	err := r.db.QueryRowContext(ctx, query, circleWalletSetID).Scan(
		&walletSet.ID,
		&walletSet.Name,
		&walletSet.CircleWalletSetID,
		&walletSet.Status,
		&walletSet.CreatedAt,
		&walletSet.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("wallet set not found")
		}
		r.logger.Error("Failed to get wallet set by Circle ID", zap.Error(err),
			zap.String("circle_wallet_set_id", circleWalletSetID))
		return nil, fmt.Errorf("failed to get wallet set: %w", err)
	}

	return walletSet, nil
}

// GetActive retrieves the currently active wallet set
func (r *WalletSetRepository) GetActive(ctx context.Context) (*entities.WalletSet, error) {
	query := `
		SELECT id, name, circle_wallet_set_id, status, created_at, updated_at
		FROM wallet_sets 
		WHERE status = $1
		ORDER BY created_at DESC
		LIMIT 1`

	walletSet := &entities.WalletSet{}

	err := r.db.QueryRowContext(ctx, query, string(entities.WalletSetStatusActive)).Scan(
		&walletSet.ID,
		&walletSet.Name,
		&walletSet.CircleWalletSetID,
		&walletSet.Status,
		&walletSet.CreatedAt,
		&walletSet.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no active wallet set found")
		}
		r.logger.Error("Failed to get active wallet set", zap.Error(err))
		return nil, fmt.Errorf("failed to get active wallet set: %w", err)
	}

	return walletSet, nil
}

// Update updates a wallet set
func (r *WalletSetRepository) Update(ctx context.Context, walletSet *entities.WalletSet) error {
	query := `
		UPDATE wallet_sets SET 
			name = $2, circle_wallet_set_id = $3, status = $4, updated_at = $5
		WHERE id = $1`

	_, err := r.db.ExecContext(ctx, query,
		walletSet.ID,
		walletSet.Name,
		walletSet.CircleWalletSetID,
		string(walletSet.Status),
		time.Now(),
	)

	if err != nil {
		r.logger.Error("Failed to update wallet set", zap.Error(err), zap.String("wallet_set_id", walletSet.ID.String()))
		return fmt.Errorf("failed to update wallet set: %w", err)
	}

	r.logger.Debug("Wallet set updated successfully", zap.String("wallet_set_id", walletSet.ID.String()))
	return nil
}

// GetByUserIDAndAccountType retrieves wallets by user ID and account type
func (r *WalletRepository) GetByUserIDAndAccountType(ctx context.Context, userID uuid.UUID, accountType entities.WalletAccountType) ([]*entities.ManagedWallet, error) {
	query := `
		SELECT id, user_id, wallet_set_id, circle_wallet_id, chain, address, account_type, status, created_at, updated_at
		FROM managed_wallets 
		WHERE user_id = $1 AND account_type = $2
		ORDER BY created_at ASC`

	rows, err := r.db.QueryContext(ctx, query, userID, string(accountType))
	if err != nil {
		r.logger.Error("Failed to get wallets by user and account type", zap.Error(err),
			zap.String("user_id", userID.String()),
			zap.String("account_type", string(accountType)))
		return nil, fmt.Errorf("failed to get wallets: %w", err)
	}
	defer rows.Close()

	var wallets []*entities.ManagedWallet
	for rows.Next() {
		wallet := &entities.ManagedWallet{}
		err := rows.Scan(
			&wallet.ID,
			&wallet.UserID,
			&wallet.WalletSetID,
			&wallet.CircleWalletID,
			&wallet.Chain,
			&wallet.Address,
			&wallet.AccountType,
			&wallet.Status,
			&wallet.CreatedAt,
			&wallet.UpdatedAt,
		)
		if err != nil {
			r.logger.Error("Failed to scan wallet", zap.Error(err))
			return nil, fmt.Errorf("failed to scan wallet: %w", err)
		}
		wallets = append(wallets, wallet)
	}

	return wallets, nil
}

// ListWithFilters retrieves wallets with pagination and filters for admin queries
func (r *WalletRepository) ListWithFilters(ctx context.Context, filters WalletListFilters) ([]*entities.ManagedWallet, int64, error) {
	var conditions []string
	var args []interface{}
	argIndex := 1

	// Build WHERE conditions
	if filters.UserID != nil {
		conditions = append(conditions, fmt.Sprintf("user_id = $%d", argIndex))
		args = append(args, *filters.UserID)
		argIndex++
	}

	if filters.WalletSetID != nil {
		conditions = append(conditions, fmt.Sprintf("wallet_set_id = $%d", argIndex))
		args = append(args, *filters.WalletSetID)
		argIndex++
	}

	if filters.Chain != nil {
		conditions = append(conditions, fmt.Sprintf("chain = $%d", argIndex))
		args = append(args, string(*filters.Chain))
		argIndex++
	}

	if filters.AccountType != nil {
		conditions = append(conditions, fmt.Sprintf("account_type = $%d", argIndex))
		args = append(args, string(*filters.AccountType))
		argIndex++
	}

	if filters.Status != nil {
		conditions = append(conditions, fmt.Sprintf("status = $%d", argIndex))
		args = append(args, string(*filters.Status))
		argIndex++
	}

	// Build query
	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Count query
	countQuery := "SELECT COUNT(*) FROM managed_wallets " + whereClause
	var totalCount int64
	err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&totalCount)
	if err != nil {
		r.logger.Error("Failed to count wallets", zap.Error(err))
		return nil, 0, fmt.Errorf("failed to count wallets: %w", err)
	}

	// Data query with pagination
	query := fmt.Sprintf(`
		SELECT id, user_id, wallet_set_id, circle_wallet_id, chain, address, account_type, status, created_at, updated_at
		FROM managed_wallets 
		%s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d`, whereClause, argIndex, argIndex+1)

	// Add pagination args
	args = append(args, filters.Limit, filters.Offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to list wallets with filters", zap.Error(err))
		return nil, 0, fmt.Errorf("failed to list wallets: %w", err)
	}
	defer rows.Close()

	var wallets []*entities.ManagedWallet
	for rows.Next() {
		wallet := &entities.ManagedWallet{}
		err := rows.Scan(
			&wallet.ID,
			&wallet.UserID,
			&wallet.WalletSetID,
			&wallet.CircleWalletID,
			&wallet.Chain,
			&wallet.Address,
			&wallet.AccountType,
			&wallet.Status,
			&wallet.CreatedAt,
			&wallet.UpdatedAt,
		)
		if err != nil {
			r.logger.Error("Failed to scan wallet", zap.Error(err))
			return nil, 0, fmt.Errorf("failed to scan wallet: %w", err)
		}
		wallets = append(wallets, wallet)
	}

	return wallets, totalCount, nil
}

// GetWalletsByWalletSetID retrieves all wallets in a wallet set
func (r *WalletRepository) GetWalletsByWalletSetID(ctx context.Context, walletSetID uuid.UUID) ([]*entities.ManagedWallet, error) {
	query := `
		SELECT id, user_id, wallet_set_id, circle_wallet_id, chain, address, account_type, status, created_at, updated_at
		FROM managed_wallets 
		WHERE wallet_set_id = $1
		ORDER BY created_at ASC`

	rows, err := r.db.QueryContext(ctx, query, walletSetID)
	if err != nil {
		r.logger.Error("Failed to get wallets by wallet set ID", zap.Error(err),
			zap.String("wallet_set_id", walletSetID.String()))
		return nil, fmt.Errorf("failed to get wallets: %w", err)
	}
	defer rows.Close()

	var wallets []*entities.ManagedWallet
	for rows.Next() {
		wallet := &entities.ManagedWallet{}
		err := rows.Scan(
			&wallet.ID,
			&wallet.UserID,
			&wallet.WalletSetID,
			&wallet.CircleWalletID,
			&wallet.Chain,
			&wallet.Address,
			&wallet.AccountType,
			&wallet.Status,
			&wallet.CreatedAt,
			&wallet.UpdatedAt,
		)
		if err != nil {
			r.logger.Error("Failed to scan wallet", zap.Error(err))
			return nil, fmt.Errorf("failed to scan wallet: %w", err)
		}
		wallets = append(wallets, wallet)
	}

	return wallets, nil
}

// CountByStatus counts wallets by status
func (r *WalletRepository) CountByStatus(ctx context.Context, status entities.WalletStatus) (int64, error) {
	query := `SELECT COUNT(*) FROM managed_wallets WHERE status = $1`

	var count int64
	err := r.db.QueryRowContext(ctx, query, string(status)).Scan(&count)
	if err != nil {
		r.logger.Error("Failed to count wallets by status", zap.Error(err),
			zap.String("status", string(status)))
		return 0, fmt.Errorf("failed to count wallets: %w", err)
	}

	return count, nil
}
