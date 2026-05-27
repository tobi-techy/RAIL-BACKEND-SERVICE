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
		SELECT id, user_id, wallet_id, from_address, to_address, token_id,
		       amount, transaction_hash, block_number, chain_id, gas_used,
		       gas_price, status, type, description, created_at, confirmed_at
		FROM transactions
		WHERE user_id = $1
		ORDER BY created_at DESC
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
