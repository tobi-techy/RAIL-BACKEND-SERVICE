package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// BankAccountRepository handles bank account persistence
type BankAccountRepository struct {
	db *sqlx.DB
}

// NewBankAccountRepository creates a new bank account repository
func NewBankAccountRepository(db *sqlx.DB) *BankAccountRepository {
	return &BankAccountRepository{db: db}
}

// Create creates a new bank account
func (r *BankAccountRepository) Create(ctx context.Context, bankAccount *entities.BankAccount) error {
	query := `
		INSERT INTO bank_accounts (
			id, user_id, bank_name, account_number_last4, routing_number_last4,
			iban, bic, currency, is_verified, is_primary, bridge_recipient_id,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`

	_, err := r.db.ExecContext(ctx, query,
		bankAccount.ID,
		bankAccount.UserID,
		bankAccount.BankName,
		bankAccount.AccountNumberLast4,
		bankAccount.RoutingNumberLast4,
		bankAccount.IBAN,
		bankAccount.BIC,
		bankAccount.Currency,
		bankAccount.IsVerified,
		bankAccount.IsPrimary,
		bankAccount.BridgeRecipientID,
		bankAccount.CreatedAt,
		bankAccount.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create bank account: %w", err)
	}

	return nil
}

// GetByID retrieves a bank account by ID
func (r *BankAccountRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.BankAccount, error) {
	query := `
		SELECT id, user_id, bank_name, account_number_last4, routing_number_last4,
			iban, bic, currency, is_verified, is_primary, bridge_recipient_id,
			created_at, updated_at
		FROM bank_accounts
		WHERE id = $1
	`

	var bankAccount entities.BankAccount
	err := r.db.GetContext(ctx, &bankAccount, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("bank account not found")
		}
		return nil, fmt.Errorf("failed to get bank account: %w", err)
	}

	return &bankAccount, nil
}

// GetByUserID retrieves all bank accounts for a user
func (r *BankAccountRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.BankAccount, error) {
	query := `
		SELECT id, user_id, bank_name, account_number_last4, routing_number_last4,
			iban, bic, currency, is_verified, is_primary, bridge_recipient_id,
			created_at, updated_at
		FROM bank_accounts
		WHERE user_id = $1
		ORDER BY is_primary DESC, created_at DESC
	`

	var bankAccounts []*entities.BankAccount
	err := r.db.SelectContext(ctx, &bankAccounts, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get bank accounts: %w", err)
	}

	return bankAccounts, nil
}

// GetByBridgeRecipientID retrieves a bank account by Bridge recipient ID
func (r *BankAccountRepository) GetByBridgeRecipientID(ctx context.Context, recipientID string) (*entities.BankAccount, error) {
	query := `
		SELECT id, user_id, bank_name, account_number_last4, routing_number_last4,
			iban, bic, currency, is_verified, is_primary, bridge_recipient_id,
			created_at, updated_at
		FROM bank_accounts
		WHERE bridge_recipient_id = $1
	`

	var bankAccount entities.BankAccount
	err := r.db.GetContext(ctx, &bankAccount, query, recipientID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get bank account by bridge recipient ID: %w", err)
	}

	return &bankAccount, nil
}

// Update updates a bank account
func (r *BankAccountRepository) Update(ctx context.Context, bankAccount *entities.BankAccount) error {
	query := `
		UPDATE bank_accounts
		SET bank_name = $1, is_verified = $2, is_primary = $3, 
			bridge_recipient_id = $4, updated_at = $5
		WHERE id = $6
	`

	_, err := r.db.ExecContext(ctx, query,
		bankAccount.BankName,
		bankAccount.IsVerified,
		bankAccount.IsPrimary,
		bankAccount.BridgeRecipientID,
		time.Now(),
		bankAccount.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update bank account: %w", err)
	}

	return nil
}

// Delete deletes a bank account
func (r *BankAccountRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM bank_accounts WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete bank account: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("bank account not found")
	}

	return nil
}

// SetPrimary sets a bank account as primary and unsets others for the user
func (r *BankAccountRepository) SetPrimary(ctx context.Context, userID, bankAccountID uuid.UUID) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Unset all primary flags for user
	_, err = tx.ExecContext(ctx,
		`UPDATE bank_accounts SET is_primary = false, updated_at = $1 WHERE user_id = $2`,
		time.Now(), userID,
	)
	if err != nil {
		return fmt.Errorf("failed to unset primary flags: %w", err)
	}

	// Set the specified account as primary
	_, err = tx.ExecContext(ctx,
		`UPDATE bank_accounts SET is_primary = true, updated_at = $1 WHERE id = $2 AND user_id = $3`,
		time.Now(), bankAccountID, userID,
	)
	if err != nil {
		return fmt.Errorf("failed to set primary: %w", err)
	}

	return tx.Commit()
}

// MarkVerified marks a bank account as verified
func (r *BankAccountRepository) MarkVerified(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE bank_accounts SET is_verified = true, updated_at = $1 WHERE id = $2`

	_, err := r.db.ExecContext(ctx, query, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to mark bank account verified: %w", err)
	}

	return nil
}

// UpdateBridgeRecipientID updates the Bridge recipient ID for a bank account
func (r *BankAccountRepository) UpdateBridgeRecipientID(ctx context.Context, id uuid.UUID, recipientID string) error {
	query := `UPDATE bank_accounts SET bridge_recipient_id = $1, updated_at = $2 WHERE id = $3`

	_, err := r.db.ExecContext(ctx, query, recipientID, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to update bridge recipient ID: %w", err)
	}

	return nil
}
