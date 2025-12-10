package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// BasketRepository handles basket database operations
type BasketRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewBasketRepository creates a new basket repository instance
func NewBasketRepository(db *sql.DB, logger *zap.Logger) *BasketRepository {
	return &BasketRepository{
		db:     db,
		logger: logger,
	}
}

// GetAll retrieves all available baskets
func (r *BasketRepository) GetAll(ctx context.Context) ([]*entities.Basket, error) {
	query := `
		SELECT id, name, description, risk_level, composition_json, created_at, updated_at
		FROM baskets
		ORDER BY name ASC
	`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		r.logger.Error("Failed to query baskets", zap.Error(err))
		return nil, fmt.Errorf("failed to query baskets: %w", err)
	}
	defer rows.Close()

	var baskets []*entities.Basket
	for rows.Next() {
		basket := &entities.Basket{}
		var compositionJSON []byte
		if err := rows.Scan(
			&basket.ID,
			&basket.Name,
			&basket.Description,
			&basket.RiskLevel,
			&compositionJSON,
			&basket.CreatedAt,
			&basket.UpdatedAt,
		); err != nil {
			r.logger.Error("Failed to scan basket row", zap.Error(err))
			return nil, fmt.Errorf("failed to scan basket: %w", err)
		}
		
		// Unmarshal composition JSON
		if err := json.Unmarshal(compositionJSON, &basket.Composition); err != nil {
			r.logger.Error("Failed to unmarshal basket composition", zap.Error(err))
			return nil, fmt.Errorf("failed to unmarshal basket composition: %w", err)
		}
		
		baskets = append(baskets, basket)
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating basket rows", zap.Error(err))
		return nil, fmt.Errorf("error iterating baskets: %w", err)
	}

	r.logger.Debug("Retrieved baskets", zap.Int("count", len(baskets)))
	return baskets, nil
}

// GetByID retrieves a specific basket by ID
func (r *BasketRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.Basket, error) {
	query := `
		SELECT id, name, description, risk_level, composition_json, created_at, updated_at
		FROM baskets
		WHERE id = $1
	`

	basket := &entities.Basket{}
	var compositionJSON []byte
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&basket.ID,
		&basket.Name,
		&basket.Description,
		&basket.RiskLevel,
		&compositionJSON,
		&basket.CreatedAt,
		&basket.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		r.logger.Debug("Basket not found", zap.String("basket_id", id.String()))
		return nil, nil
	}

	if err != nil {
		r.logger.Error("Failed to get basket", zap.Error(err), zap.String("basket_id", id.String()))
		return nil, fmt.Errorf("failed to get basket: %w", err)
	}

	// Unmarshal composition JSON
	if err := json.Unmarshal(compositionJSON, &basket.Composition); err != nil {
		r.logger.Error("Failed to unmarshal basket composition", zap.Error(err))
		return nil, fmt.Errorf("failed to unmarshal basket composition: %w", err)
	}

	r.logger.Debug("Retrieved basket", zap.String("basket_id", id.String()))
	return basket, nil
}
