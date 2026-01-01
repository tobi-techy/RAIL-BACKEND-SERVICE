package grid

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// Repository implements grid.Repository
type Repository struct {
	db *sqlx.DB
}

// NewRepository creates a new Grid repository
func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

// CreateAccount creates a new Grid account
func (r *Repository) CreateAccount(ctx context.Context, account *entities.GridAccount) error {
	query := `
		INSERT INTO grid_accounts (id, user_id, email, address, status, kyc_status, encrypted_session_secret, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	_, err := r.db.ExecContext(ctx, query,
		account.ID,
		account.UserID,
		account.Email,
		account.Address,
		account.Status,
		account.KYCStatus,
		account.EncryptedSessionSecret,
		account.CreatedAt,
		account.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create grid account: %w", err)
	}
	return nil
}

// GetAccountByUserID retrieves a Grid account by user ID
func (r *Repository) GetAccountByUserID(ctx context.Context, userID uuid.UUID) (*entities.GridAccount, error) {
	var account entities.GridAccount
	query := `SELECT id, user_id, email, address, status, kyc_status, encrypted_session_secret, created_at, updated_at 
			  FROM grid_accounts WHERE user_id = $1`

	if err := r.db.GetContext(ctx, &account, query, userID); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("grid account not found for user: %s", userID)
		}
		return nil, fmt.Errorf("failed to get grid account: %w", err)
	}
	return &account, nil
}

// GetAccountByAddress retrieves a Grid account by Solana address
func (r *Repository) GetAccountByAddress(ctx context.Context, address string) (*entities.GridAccount, error) {
	var account entities.GridAccount
	query := `SELECT id, user_id, email, address, status, kyc_status, encrypted_session_secret, created_at, updated_at 
			  FROM grid_accounts WHERE address = $1`

	if err := r.db.GetContext(ctx, &account, query, address); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("grid account not found for address: %s", address)
		}
		return nil, fmt.Errorf("failed to get grid account: %w", err)
	}
	return &account, nil
}

// GetAccountByEmail retrieves a Grid account by email
func (r *Repository) GetAccountByEmail(ctx context.Context, email string) (*entities.GridAccount, error) {
	var account entities.GridAccount
	query := `SELECT id, user_id, email, address, status, kyc_status, encrypted_session_secret, created_at, updated_at 
			  FROM grid_accounts WHERE email = $1`

	if err := r.db.GetContext(ctx, &account, query, email); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("grid account not found for email: %s", email)
		}
		return nil, fmt.Errorf("failed to get grid account: %w", err)
	}
	return &account, nil
}

// UpdateAccount updates a Grid account
func (r *Repository) UpdateAccount(ctx context.Context, account *entities.GridAccount) error {
	query := `
		UPDATE grid_accounts 
		SET email = $1, address = $2, status = $3, kyc_status = $4, encrypted_session_secret = $5, updated_at = $6
		WHERE id = $7`

	result, err := r.db.ExecContext(ctx, query,
		account.Email,
		account.Address,
		account.Status,
		account.KYCStatus,
		account.EncryptedSessionSecret,
		time.Now().UTC(),
		account.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update grid account: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("grid account not found: %s", account.ID)
	}
	return nil
}

// CreateVirtualAccount creates a new Grid virtual account
func (r *Repository) CreateVirtualAccount(ctx context.Context, va *entities.GridVirtualAccount) error {
	query := `
		INSERT INTO grid_virtual_accounts (id, grid_account_id, user_id, external_id, account_number, routing_number, bank_name, currency, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	_, err := r.db.ExecContext(ctx, query,
		va.ID,
		va.GridAccountID,
		va.UserID,
		va.ExternalID,
		va.AccountNumber,
		va.RoutingNumber,
		va.BankName,
		va.Currency,
		va.Status,
		va.CreatedAt,
		va.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create grid virtual account: %w", err)
	}
	return nil
}

// GetVirtualAccountsByUserID retrieves all virtual accounts for a user
func (r *Repository) GetVirtualAccountsByUserID(ctx context.Context, userID uuid.UUID) ([]entities.GridVirtualAccount, error) {
	var accounts []entities.GridVirtualAccount
	query := `SELECT id, grid_account_id, user_id, external_id, account_number, routing_number, bank_name, currency, status, created_at, updated_at 
			  FROM grid_virtual_accounts WHERE user_id = $1 ORDER BY created_at DESC`

	if err := r.db.SelectContext(ctx, &accounts, query, userID); err != nil {
		return nil, fmt.Errorf("failed to get grid virtual accounts: %w", err)
	}
	return accounts, nil
}

// GetVirtualAccountByExternalID retrieves a virtual account by Grid's external ID
func (r *Repository) GetVirtualAccountByExternalID(ctx context.Context, externalID string) (*entities.GridVirtualAccount, error) {
	var va entities.GridVirtualAccount
	query := `SELECT id, grid_account_id, user_id, external_id, account_number, routing_number, bank_name, currency, status, created_at, updated_at 
			  FROM grid_virtual_accounts WHERE external_id = $1`

	if err := r.db.GetContext(ctx, &va, query, externalID); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("grid virtual account not found: %s", externalID)
		}
		return nil, fmt.Errorf("failed to get grid virtual account: %w", err)
	}
	return &va, nil
}

// CreatePaymentIntent creates a new payment intent
func (r *Repository) CreatePaymentIntent(ctx context.Context, pi *entities.GridPaymentIntent) error {
	query := `
		INSERT INTO grid_payment_intents (id, grid_account_id, user_id, external_id, amount, currency, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	_, err := r.db.ExecContext(ctx, query,
		pi.ID,
		pi.GridAccountID,
		pi.UserID,
		pi.ExternalID,
		pi.Amount,
		pi.Currency,
		pi.Status,
		pi.CreatedAt,
		pi.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create grid payment intent: %w", err)
	}
	return nil
}

// GetPaymentIntentByExternalID retrieves a payment intent by Grid's external ID
func (r *Repository) GetPaymentIntentByExternalID(ctx context.Context, externalID string) (*entities.GridPaymentIntent, error) {
	var pi entities.GridPaymentIntent
	query := `SELECT id, grid_account_id, user_id, external_id, amount, currency, status, created_at, updated_at 
			  FROM grid_payment_intents WHERE external_id = $1`

	if err := r.db.GetContext(ctx, &pi, query, externalID); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("grid payment intent not found: %s", externalID)
		}
		return nil, fmt.Errorf("failed to get grid payment intent: %w", err)
	}
	return &pi, nil
}

// UpdatePaymentIntent updates a payment intent
func (r *Repository) UpdatePaymentIntent(ctx context.Context, pi *entities.GridPaymentIntent) error {
	query := `UPDATE grid_payment_intents SET status = $1, updated_at = $2 WHERE id = $3`

	result, err := r.db.ExecContext(ctx, query, pi.Status, time.Now().UTC(), pi.ID)
	if err != nil {
		return fmt.Errorf("failed to update grid payment intent: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("grid payment intent not found: %s", pi.ID)
	}
	return nil
}
