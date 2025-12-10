package repositories

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// RoundupRepository handles round-up data persistence
type RoundupRepository struct {
	db *sqlx.DB
}

// NewRoundupRepository creates a new round-up repository
func NewRoundupRepository(db *sqlx.DB) *RoundupRepository {
	return &RoundupRepository{db: db}
}

// GetSettings retrieves round-up settings for a user
func (r *RoundupRepository) GetSettings(ctx context.Context, userID uuid.UUID) (*entities.RoundupSettings, error) {
	var settings entities.RoundupSettings
	err := r.db.GetContext(ctx, &settings,
		`SELECT user_id, enabled, multiplier, threshold, auto_invest_enabled, 
		        auto_invest_basket_id, auto_invest_symbol, created_at, updated_at
		 FROM roundup_settings WHERE user_id = $1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &settings, nil
}

// UpsertSettings creates or updates round-up settings
func (r *RoundupRepository) UpsertSettings(ctx context.Context, settings *entities.RoundupSettings) error {
	settings.UpdatedAt = time.Now()
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO roundup_settings (user_id, enabled, multiplier, threshold, auto_invest_enabled, 
		                               auto_invest_basket_id, auto_invest_symbol, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (user_id) DO UPDATE SET
		   enabled = EXCLUDED.enabled,
		   multiplier = EXCLUDED.multiplier,
		   threshold = EXCLUDED.threshold,
		   auto_invest_enabled = EXCLUDED.auto_invest_enabled,
		   auto_invest_basket_id = EXCLUDED.auto_invest_basket_id,
		   auto_invest_symbol = EXCLUDED.auto_invest_symbol,
		   updated_at = EXCLUDED.updated_at`,
		settings.UserID, settings.Enabled, settings.Multiplier, settings.Threshold,
		settings.AutoInvestEnabled, settings.AutoInvestBasketID, settings.AutoInvestSymbol,
		settings.CreatedAt, settings.UpdatedAt)
	return err
}

// CreateTransaction creates a new round-up transaction
func (r *RoundupRepository) CreateTransaction(ctx context.Context, tx *entities.RoundupTransaction) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO roundup_transactions (id, user_id, original_amount, rounded_amount, spare_change,
		                                   multiplied_amount, source_type, source_ref, merchant_name, status, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		tx.ID, tx.UserID, tx.OriginalAmount, tx.RoundedAmount, tx.SpareChange,
		tx.MultipliedAmount, tx.SourceType, tx.SourceRef, tx.MerchantName, tx.Status, tx.CreatedAt)
	return err
}

// UpdateTransactionStatus updates a transaction's status
func (r *RoundupRepository) UpdateTransactionStatus(ctx context.Context, id uuid.UUID, status entities.RoundupStatus, orderID *uuid.UUID) error {
	now := time.Now()
	var collectedAt, investedAt *time.Time
	if status == entities.RoundupStatusCollected {
		collectedAt = &now
	} else if status == entities.RoundupStatusInvested {
		investedAt = &now
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE roundup_transactions SET status = $1, collected_at = COALESCE($2, collected_at), 
		 invested_at = COALESCE($3, invested_at), investment_order_id = COALESCE($4, investment_order_id)
		 WHERE id = $5`, status, collectedAt, investedAt, orderID, id)
	return err
}

// GetPendingTransactions retrieves pending transactions for a user
func (r *RoundupRepository) GetPendingTransactions(ctx context.Context, userID uuid.UUID) ([]*entities.RoundupTransaction, error) {
	var txs []*entities.RoundupTransaction
	err := r.db.SelectContext(ctx, &txs,
		`SELECT * FROM roundup_transactions WHERE user_id = $1 AND status = 'pending' ORDER BY created_at`, userID)
	return txs, err
}

// GetTransactions retrieves transactions for a user with pagination
func (r *RoundupRepository) GetTransactions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.RoundupTransaction, error) {
	var txs []*entities.RoundupTransaction
	err := r.db.SelectContext(ctx, &txs,
		`SELECT * FROM roundup_transactions WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		userID, limit, offset)
	return txs, err
}

// CountTransactions counts transactions for a user
func (r *RoundupRepository) CountTransactions(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	err := r.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM roundup_transactions WHERE user_id = $1`, userID)
	return count, err
}

// GetAccumulator retrieves the accumulator for a user
func (r *RoundupRepository) GetAccumulator(ctx context.Context, userID uuid.UUID) (*entities.RoundupAccumulator, error) {
	var acc entities.RoundupAccumulator
	err := r.db.GetContext(ctx, &acc, `SELECT * FROM roundup_accumulators WHERE user_id = $1`, userID)
	if err == sql.ErrNoRows {
		return &entities.RoundupAccumulator{UserID: userID}, nil
	}
	if err != nil {
		return nil, err
	}
	return &acc, nil
}

// UpsertAccumulator creates or updates the accumulator
func (r *RoundupRepository) UpsertAccumulator(ctx context.Context, acc *entities.RoundupAccumulator) error {
	acc.UpdatedAt = time.Now()
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO roundup_accumulators (user_id, pending_amount, total_collected, total_invested, 
		                                   last_collection_at, last_investment_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (user_id) DO UPDATE SET
		   pending_amount = EXCLUDED.pending_amount,
		   total_collected = EXCLUDED.total_collected,
		   total_invested = EXCLUDED.total_invested,
		   last_collection_at = COALESCE(EXCLUDED.last_collection_at, roundup_accumulators.last_collection_at),
		   last_investment_at = COALESCE(EXCLUDED.last_investment_at, roundup_accumulators.last_investment_at),
		   updated_at = EXCLUDED.updated_at`,
		acc.UserID, acc.PendingAmount, acc.TotalCollected, acc.TotalInvested,
		acc.LastCollectionAt, acc.LastInvestmentAt, acc.UpdatedAt)
	return err
}

// GetUsersReadyForAutoInvest finds users whose pending amount exceeds threshold
func (r *RoundupRepository) GetUsersReadyForAutoInvest(ctx context.Context) ([]uuid.UUID, error) {
	var userIDs []uuid.UUID
	err := r.db.SelectContext(ctx, &userIDs,
		`SELECT a.user_id FROM roundup_accumulators a
		 JOIN roundup_settings s ON a.user_id = s.user_id
		 WHERE s.enabled = true AND s.auto_invest_enabled = true 
		   AND a.pending_amount >= s.threshold`)
	return userIDs, err
}
