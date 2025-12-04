package repositories

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// VirtualAccountRepository implements the virtual account repository interface
type VirtualAccountRepository struct {
	db *sqlx.DB
}

// NewVirtualAccountRepository creates a new virtual account repository
func NewVirtualAccountRepository(db *sqlx.DB) *VirtualAccountRepository {
	return &VirtualAccountRepository{db: db}
}

// Create creates a new virtual account
func (r *VirtualAccountRepository) Create(ctx context.Context, account *entities.VirtualAccount) error {
	query := `
		INSERT INTO virtual_accounts (
			id, user_id, due_account_id, alpaca_account_id,
			account_number, routing_number, status, currency,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10
		)
	`

	_, err := r.db.ExecContext(ctx, query,
		account.ID,
		account.UserID,
		account.DueAccountID,
		account.AlpacaAccountID,
		account.AccountNumber,
		account.RoutingNumber,
		account.Status,
		account.Currency,
		account.CreatedAt,
		account.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create virtual account: %w", err)
	}

	return nil
}

// GetByID retrieves a virtual account by ID
func (r *VirtualAccountRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.VirtualAccount, error) {
	query := `
		SELECT id, user_id, due_account_id, alpaca_account_id,
			   account_number, routing_number, status, currency,
			   created_at, updated_at
		FROM virtual_accounts
		WHERE id = $1
	`

	var account entities.VirtualAccount
	err := r.db.GetContext(ctx, &account, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("virtual account not found: %w", err)
		}
		return nil, fmt.Errorf("failed to get virtual account: %w", err)
	}

	return &account, nil
}

// GetByDueAccountID retrieves a virtual account by Due account ID
func (r *VirtualAccountRepository) GetByDueAccountID(ctx context.Context, dueAccountID string) (*entities.VirtualAccount, error) {
	query := `
		SELECT id, user_id, due_account_id, alpaca_account_id,
			   account_number, routing_number, status, currency,
			   created_at, updated_at
		FROM virtual_accounts
		WHERE due_account_id = $1
	`

	var account entities.VirtualAccount
	err := r.db.GetContext(ctx, &account, query, dueAccountID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("virtual account not found: %w", err)
		}
		return nil, fmt.Errorf("failed to get virtual account: %w", err)
	}

	return &account, nil
}

// GetByUserID retrieves all virtual accounts for a user
func (r *VirtualAccountRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.VirtualAccount, error) {
	query := `
		SELECT id, user_id, due_account_id, alpaca_account_id,
			   account_number, routing_number, status, currency,
			   created_at, updated_at
		FROM virtual_accounts
		WHERE user_id = $1
		ORDER BY created_at DESC
	`

	var accounts []*entities.VirtualAccount
	err := r.db.SelectContext(ctx, &accounts, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list virtual accounts: %w", err)
	}

	return accounts, nil
}

// Update updates a virtual account
func (r *VirtualAccountRepository) Update(ctx context.Context, account *entities.VirtualAccount) error {
	query := `
		UPDATE virtual_accounts
		SET due_account_id = $2,
			alpaca_account_id = $3,
			account_number = $4,
			routing_number = $5,
			status = $6,
			currency = $7,
			updated_at = $8
		WHERE id = $1
	`

	_, err := r.db.ExecContext(ctx, query,
		account.ID,
		account.DueAccountID,
		account.AlpacaAccountID,
		account.AccountNumber,
		account.RoutingNumber,
		account.Status,
		account.Currency,
		account.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to update virtual account: %w", err)
	}

	return nil
}

// GetByAlpacaAccountID retrieves a virtual account by Alpaca account ID
func (r *VirtualAccountRepository) GetByAlpacaAccountID(ctx context.Context, alpacaAccountID string) (*entities.VirtualAccount, error) {
	query := `
		SELECT id, user_id, due_account_id, alpaca_account_id,
			   account_number, routing_number, status, currency,
			   created_at, updated_at
		FROM virtual_accounts
		WHERE alpaca_account_id = $1
	`

	var account entities.VirtualAccount
	err := r.db.GetContext(ctx, &account, query, alpacaAccountID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("virtual account not found")
		}
		return nil, fmt.Errorf("failed to get virtual account: %w", err)
	}

	return &account, nil
}

// UpdateStatus updates the status of a virtual account
func (r *VirtualAccountRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status entities.VirtualAccountStatus) error {
	query := `
		UPDATE virtual_accounts
		SET status = $2
		WHERE id = $1
	`

	_, err := r.db.ExecContext(ctx, query, id, status)
	if err != nil {
		return fmt.Errorf("failed to update virtual account status: %w", err)
	}

	return nil
}

// ExistsByUserAndAlpacaAccount checks if a virtual account exists for a user and Alpaca account
func (r *VirtualAccountRepository) ExistsByUserAndAlpacaAccount(ctx context.Context, userID uuid.UUID, alpacaAccountID string) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1 FROM virtual_accounts
			WHERE user_id = $1 AND alpaca_account_id = $2
		)
	`

	var exists bool
	err := r.db.GetContext(ctx, &exists, query, userID, alpacaAccountID)
	if err != nil {
		return false, fmt.Errorf("failed to check virtual account existence: %w", err)
	}

	return exists, nil
}
