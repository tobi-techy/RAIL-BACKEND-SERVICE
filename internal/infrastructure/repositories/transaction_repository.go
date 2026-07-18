package repositories

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// TransactionRepository reads blockchain transactions.
type TransactionRepository struct {
	db *sqlx.DB
}

func NewTransactionRepository(db *sqlx.DB) *TransactionRepository {
	return &TransactionRepository{db: db}
}

func (r *TransactionRepository) GetUserTransactions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]entities.Transaction, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}

	var transactions []entities.Transaction
	err := r.db.SelectContext(ctx, &transactions, `
		SELECT t.id, t.user_id, t.wallet_id, t.from_address, t.to_address, t.token_id,
		       t.amount, t.transaction_hash, t.block_number, t.chain_id, t.gas_used,
		       t.gas_price, t.status, t.type, t.description,
		       COALESCE(
		           CASE tok.symbol
		               WHEN 'USDC' THEN 'USD'
		               WHEN 'USDT' THEN 'USD'
		               WHEN 'cNGN' THEN 'NGN'
		               WHEN 'cEUR' THEN 'EUR'
		               WHEN 'cGBP' THEN 'GBP'
		               WHEN 'cKES' THEN 'KES'
		               WHEN 'cGHS' THEN 'GHS'
		               WHEN 'cZAR' THEN 'ZAR'
		               ELSE tok.symbol
		           END, 'USD'
		       ) AS currency,
		       t.created_at, t.confirmed_at
		FROM transactions t
		LEFT JOIN tokens tok ON tok.id = t.token_id
		WHERE t.user_id = $1
		ORDER BY t.created_at DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("get user transactions: %w", err)
	}
	return transactions, nil
}

// TransactionProviderAdapter wraps TransactionRepository to implement
// the miriam.TransactionProvider interface.
type TransactionProviderAdapter struct {
	repo *TransactionRepository
}

func NewTransactionProviderAdapter(repo *TransactionRepository) *TransactionProviderAdapter {
	return &TransactionProviderAdapter{repo: repo}
}

func (a *TransactionProviderAdapter) GetUserTransactions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]entities.Transaction, error) {
	return a.repo.GetUserTransactions(ctx, userID, limit, offset)
}
