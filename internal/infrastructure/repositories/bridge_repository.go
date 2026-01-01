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

// BridgeRepository implements the bridge repository interface
type BridgeRepository struct {
	db *sqlx.DB
}

// NewBridgeRepository creates a new bridge repository
func NewBridgeRepository(db *sqlx.DB) *BridgeRepository {
	return &BridgeRepository{db: db}
}

func (r *BridgeRepository) Create(ctx context.Context, bridge *entities.BridgeTransaction) error {
	query := `
		INSERT INTO bridge_transactions (
			id, user_id, source_chain, dest_chain, amount, source_tx_hash,
			message_hash, attestation, dest_tx_hash, dest_address, status,
			error_message, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`

	_, err := r.db.ExecContext(ctx, query,
		bridge.ID, bridge.UserID, bridge.SourceChain, bridge.DestChain,
		bridge.Amount, nullString(bridge.SourceTxHash), nullString(bridge.MessageHash),
		nullString(bridge.Attestation), nullString(bridge.DestTxHash),
		bridge.DestAddress, bridge.Status, nullString(bridge.ErrorMessage),
		bridge.CreatedAt, bridge.UpdatedAt,
	)
	return err
}

func (r *BridgeRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.BridgeTransaction, error) {
	var bridge entities.BridgeTransaction
	query := `SELECT * FROM bridge_transactions WHERE id = $1`
	if err := r.db.GetContext(ctx, &bridge, query, id); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("bridge transaction not found: %s", id)
		}
		return nil, err
	}
	return &bridge, nil
}

func (r *BridgeRepository) GetBySourceTxHash(ctx context.Context, txHash string) (*entities.BridgeTransaction, error) {
	var bridge entities.BridgeTransaction
	query := `SELECT * FROM bridge_transactions WHERE source_tx_hash = $1`
	if err := r.db.GetContext(ctx, &bridge, query, txHash); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &bridge, nil
}

func (r *BridgeRepository) GetPendingBridges(ctx context.Context) ([]*entities.BridgeTransaction, error) {
	var bridges []*entities.BridgeTransaction
	query := `
		SELECT * FROM bridge_transactions 
		WHERE status IN ($1, $2, $3) 
		ORDER BY created_at ASC`
	err := r.db.SelectContext(ctx, &bridges, query,
		entities.BridgeStatusPending,
		entities.BridgeStatusBurning,
		entities.BridgeStatusAttesting,
	)
	return bridges, err
}

func (r *BridgeRepository) GetByStatus(ctx context.Context, status entities.BridgeStatus) ([]*entities.BridgeTransaction, error) {
	var bridges []*entities.BridgeTransaction
	query := `SELECT * FROM bridge_transactions WHERE status = $1 ORDER BY created_at ASC`
	err := r.db.SelectContext(ctx, &bridges, query, status)
	return bridges, err
}

func (r *BridgeRepository) Update(ctx context.Context, bridge *entities.BridgeTransaction) error {
	query := `
		UPDATE bridge_transactions SET
			source_tx_hash = $2, message_hash = $3, attestation = $4,
			dest_tx_hash = $5, status = $6, error_message = $7, updated_at = $8
		WHERE id = $1`

	bridge.UpdatedAt = time.Now()
	_, err := r.db.ExecContext(ctx, query,
		bridge.ID, nullString(bridge.SourceTxHash), nullString(bridge.MessageHash),
		nullString(bridge.Attestation), nullString(bridge.DestTxHash),
		bridge.Status, nullString(bridge.ErrorMessage), bridge.UpdatedAt,
	)
	return err
}

func (r *BridgeRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status entities.BridgeStatus, errorMsg string) error {
	query := `UPDATE bridge_transactions SET status = $2, error_message = $3, updated_at = $4 WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, id, status, nullString(errorMsg), time.Now())
	return err
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
