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

// CardRepository handles card database operations
type CardRepository struct {
	db *sqlx.DB
}

// NewCardRepository creates a new card repository
func NewCardRepository(db *sqlx.DB) *CardRepository {
	return &CardRepository{db: db}
}

// Create creates a new card
func (r *CardRepository) Create(ctx context.Context, card *entities.BridgeCard) error {
	query := `
		INSERT INTO cards (
			id, user_id, bridge_card_id, bridge_customer_id, type, status,
			last_4, expiry, card_image_url, currency, chain, wallet_address,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
		)`

	if card.ID == uuid.Nil {
		card.ID = uuid.New()
	}
	now := time.Now().UTC()
	card.CreatedAt = now
	card.UpdatedAt = now

	_, err := r.db.ExecContext(ctx, query,
		card.ID, card.UserID, card.BridgeCardID, card.BridgeCustomerID,
		card.Type, card.Status, card.Last4, card.Expiry, card.CardImageURL,
		card.Currency, card.Chain, card.WalletAddress, card.CreatedAt, card.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create card: %w", err)
	}
	return nil
}

// GetByID retrieves a card by ID
func (r *CardRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.BridgeCard, error) {
	var card entities.BridgeCard
	query := `SELECT * FROM cards WHERE id = $1`
	err := r.db.GetContext(ctx, &card, query, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get card: %w", err)
	}
	return &card, nil
}

// GetByBridgeCardID retrieves a card by Bridge card ID
func (r *CardRepository) GetByBridgeCardID(ctx context.Context, bridgeCardID string) (*entities.BridgeCard, error) {
	var card entities.BridgeCard
	query := `SELECT * FROM cards WHERE bridge_card_id = $1`
	err := r.db.GetContext(ctx, &card, query, bridgeCardID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get card by bridge ID: %w", err)
	}
	return &card, nil
}

// GetByUserID retrieves all cards for a user
func (r *CardRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.BridgeCard, error) {
	var cards []*entities.BridgeCard
	query := `SELECT * FROM cards WHERE user_id = $1 ORDER BY created_at DESC`
	err := r.db.SelectContext(ctx, &cards, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get cards by user: %w", err)
	}
	return cards, nil
}

// GetActiveVirtualCard retrieves the user's active virtual card
func (r *CardRepository) GetActiveVirtualCard(ctx context.Context, userID uuid.UUID) (*entities.BridgeCard, error) {
	var card entities.BridgeCard
	query := `SELECT * FROM cards WHERE user_id = $1 AND type = 'virtual' AND status = 'active' LIMIT 1`
	err := r.db.GetContext(ctx, &card, query, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get active virtual card: %w", err)
	}
	return &card, nil
}

// UpdateStatus updates a card's status
func (r *CardRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status entities.CardStatus) error {
	query := `UPDATE cards SET status = $1, updated_at = $2 WHERE id = $3`
	_, err := r.db.ExecContext(ctx, query, status, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("failed to update card status: %w", err)
	}
	return nil
}

// CreateTransaction creates a new card transaction
func (r *CardRepository) CreateTransaction(ctx context.Context, tx *entities.BridgeCardTransaction) error {
	query := `
		INSERT INTO card_transactions (
			id, card_id, user_id, bridge_trans_id, type, amount, currency,
			merchant_name, merchant_category, status, decline_reason,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		)`

	if tx.ID == uuid.Nil {
		tx.ID = uuid.New()
	}
	now := time.Now().UTC()
	tx.CreatedAt = now
	tx.UpdatedAt = now

	_, err := r.db.ExecContext(ctx, query,
		tx.ID, tx.CardID, tx.UserID, tx.BridgeTransID, tx.Type, tx.Amount,
		tx.Currency, tx.MerchantName, tx.MerchantCategory, tx.Status,
		tx.DeclineReason, tx.CreatedAt, tx.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create card transaction: %w", err)
	}
	return nil
}

// GetTransactionByBridgeID retrieves a transaction by Bridge transaction ID
func (r *CardRepository) GetTransactionByBridgeID(ctx context.Context, bridgeTransID string) (*entities.BridgeCardTransaction, error) {
	var tx entities.BridgeCardTransaction
	query := `SELECT * FROM card_transactions WHERE bridge_trans_id = $1`
	err := r.db.GetContext(ctx, &tx, query, bridgeTransID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get transaction: %w", err)
	}
	return &tx, nil
}

// GetTransactionsByCardID retrieves transactions for a card
func (r *CardRepository) GetTransactionsByCardID(ctx context.Context, cardID uuid.UUID, limit, offset int) ([]*entities.BridgeCardTransaction, error) {
	var txs []*entities.BridgeCardTransaction
	query := `SELECT * FROM card_transactions WHERE card_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`
	err := r.db.SelectContext(ctx, &txs, query, cardID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get transactions: %w", err)
	}
	return txs, nil
}

// GetTransactionsByUserID retrieves transactions for a user
func (r *CardRepository) GetTransactionsByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.BridgeCardTransaction, error) {
	var txs []*entities.BridgeCardTransaction
	query := `SELECT * FROM card_transactions WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`
	err := r.db.SelectContext(ctx, &txs, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get user transactions: %w", err)
	}
	return txs, nil
}

// UpdateTransactionStatus updates a transaction's status
func (r *CardRepository) UpdateTransactionStatus(ctx context.Context, id uuid.UUID, status string, declineReason *string) error {
	query := `UPDATE card_transactions SET status = $1, decline_reason = $2, updated_at = $3 WHERE id = $4`
	_, err := r.db.ExecContext(ctx, query, status, declineReason, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("failed to update transaction status: %w", err)
	}
	return nil
}

// CountByUserID counts cards for a user
func (r *CardRepository) CountByUserID(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM cards WHERE user_id = $1`
	err := r.db.GetContext(ctx, &count, query, userID)
	if err != nil {
		return 0, fmt.Errorf("failed to count cards: %w", err)
	}
	return count, nil
}
