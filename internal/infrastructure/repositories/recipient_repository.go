package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/services/recipient"
)

// RecipientRepository implements recipient.Repository
type RecipientRepository struct {
	db *sqlx.DB
}

// NewRecipientRepository creates a new recipient repository
func NewRecipientRepository(db *sqlx.DB) *RecipientRepository {
	return &RecipientRepository{db: db}
}

// Create stores a new recipient
func (r *RecipientRepository) Create(ctx context.Context, rec *recipient.Recipient) error {
	query := `
		INSERT INTO recipients (id, user_id, provider_id, name, schema, address, is_default, is_verified, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

	_, err := r.db.ExecContext(ctx, query,
		rec.ID, rec.UserID, rec.ProviderID, rec.Name, rec.Schema, rec.Address,
		rec.IsDefault, rec.IsVerified, rec.CreatedAt, rec.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to create recipient: %w", err)
	}
	return nil
}

// GetByID retrieves a recipient by ID
func (r *RecipientRepository) GetByID(ctx context.Context, id uuid.UUID) (*recipient.Recipient, error) {
	var rec recipient.Recipient
	query := `SELECT id, user_id, provider_id, name, schema, address, is_default, is_verified, created_at, updated_at
		FROM recipients WHERE id = $1`

	if err := r.db.GetContext(ctx, &rec, query, id); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get recipient: %w", err)
	}
	return &rec, nil
}

// GetByUserID retrieves all recipients for a user
func (r *RecipientRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*recipient.Recipient, error) {
	var recipients []*recipient.Recipient
	query := `SELECT id, user_id, provider_id, name, schema, address, is_default, is_verified, created_at, updated_at
		FROM recipients WHERE user_id = $1 ORDER BY is_default DESC, created_at DESC`

	if err := r.db.SelectContext(ctx, &recipients, query, userID); err != nil {
		return nil, fmt.Errorf("failed to get recipients: %w", err)
	}
	return recipients, nil
}

// GetByProviderID retrieves a recipient by provider ID
func (r *RecipientRepository) GetByProviderID(ctx context.Context, providerID string) (*recipient.Recipient, error) {
	var rec recipient.Recipient
	query := `SELECT id, user_id, provider_id, name, schema, address, is_default, is_verified, created_at, updated_at
		FROM recipients WHERE provider_id = $1`

	if err := r.db.GetContext(ctx, &rec, query, providerID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get recipient: %w", err)
	}
	return &rec, nil
}

// GetDefault retrieves the default recipient for a user
func (r *RecipientRepository) GetDefault(ctx context.Context, userID uuid.UUID) (*recipient.Recipient, error) {
	var rec recipient.Recipient
	query := `SELECT id, user_id, provider_id, name, schema, address, is_default, is_verified, created_at, updated_at
		FROM recipients WHERE user_id = $1 AND is_default = true LIMIT 1`

	if err := r.db.GetContext(ctx, &rec, query, userID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get default recipient: %w", err)
	}
	return &rec, nil
}

// SetDefault sets a recipient as the default for a user
func (r *RecipientRepository) SetDefault(ctx context.Context, userID, recipientID uuid.UUID) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Clear existing default
	_, err = tx.ExecContext(ctx, `UPDATE recipients SET is_default = false, updated_at = $1 WHERE user_id = $2`, time.Now(), userID)
	if err != nil {
		return fmt.Errorf("failed to clear default: %w", err)
	}

	// Set new default
	_, err = tx.ExecContext(ctx, `UPDATE recipients SET is_default = true, updated_at = $1 WHERE id = $2 AND user_id = $3`, time.Now(), recipientID, userID)
	if err != nil {
		return fmt.Errorf("failed to set default: %w", err)
	}

	return tx.Commit()
}

// Delete removes a recipient
func (r *RecipientRepository) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM recipients WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete recipient: %w", err)
	}
	return nil
}
