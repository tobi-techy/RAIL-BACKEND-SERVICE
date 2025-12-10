package repositories

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// PositionRepository handles position database operations
type PositionRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewPositionRepository creates a new position repository instance
func NewPositionRepository(db *sql.DB, logger *zap.Logger) *PositionRepository {
	return &PositionRepository{
		db:     db,
		logger: logger,
	}
}

// GetByUserID retrieves all positions for a specific user
func (r *PositionRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.Position, error) {
	query := `
		SELECT id, user_id, basket_id, quantity, avg_price, market_value, updated_at
		FROM positions
		WHERE user_id = $1
		ORDER BY market_value DESC
	`

	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		r.logger.Error("Failed to query positions", zap.Error(err), zap.String("user_id", userID.String()))
		return nil, fmt.Errorf("failed to query positions: %w", err)
	}
	defer rows.Close()

	var positions []*entities.Position
	for rows.Next() {
		position := &entities.Position{}
		if err := rows.Scan(
			&position.ID,
			&position.UserID,
			&position.BasketID,
			&position.Quantity,
			&position.AvgPrice,
			&position.MarketValue,
			&position.UpdatedAt,
		); err != nil {
			r.logger.Error("Failed to scan position row", zap.Error(err))
			return nil, fmt.Errorf("failed to scan position: %w", err)
		}
		positions = append(positions, position)
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating position rows", zap.Error(err))
		return nil, fmt.Errorf("error iterating positions: %w", err)
	}

	r.logger.Debug("Retrieved positions", zap.String("user_id", userID.String()), zap.Int("count", len(positions)))
	return positions, nil
}

// GetByUserAndBasket retrieves a specific position for a user and basket combination
func (r *PositionRepository) GetByUserAndBasket(ctx context.Context, userID, basketID uuid.UUID) (*entities.Position, error) {
	query := `
		SELECT id, user_id, basket_id, quantity, avg_price, market_value, updated_at
		FROM positions
		WHERE user_id = $1 AND basket_id = $2
	`

	position := &entities.Position{}
	err := r.db.QueryRowContext(ctx, query, userID, basketID).Scan(
		&position.ID,
		&position.UserID,
		&position.BasketID,
		&position.Quantity,
		&position.AvgPrice,
		&position.MarketValue,
		&position.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		r.logger.Debug("Position not found",
			zap.String("user_id", userID.String()),
			zap.String("basket_id", basketID.String()),
		)
		return nil, nil
	}

	if err != nil {
		r.logger.Error("Failed to get position",
			zap.Error(err),
			zap.String("user_id", userID.String()),
			zap.String("basket_id", basketID.String()),
		)
		return nil, fmt.Errorf("failed to get position: %w", err)
	}

	return position, nil
}

// CreateOrUpdate creates a new position or updates an existing one
func (r *PositionRepository) CreateOrUpdate(ctx context.Context, position *entities.Position) error {
	query := `
		INSERT INTO positions (id, user_id, basket_id, quantity, avg_price, market_value, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id, basket_id)
		DO UPDATE SET
			quantity = $4,
			avg_price = $5,
			market_value = $6,
			updated_at = $7
	`

	// Ensure ID is set
	if position.ID == uuid.Nil {
		position.ID = uuid.New()
	}

	_, err := r.db.ExecContext(ctx, query,
		position.ID,
		position.UserID,
		position.BasketID,
		position.Quantity,
		position.AvgPrice,
		position.MarketValue,
		position.UpdatedAt,
	)

	if err != nil {
		r.logger.Error("Failed to create/update position",
			zap.Error(err),
			zap.String("user_id", position.UserID.String()),
			zap.String("basket_id", position.BasketID.String()),
		)
		return fmt.Errorf("failed to create/update position: %w", err)
	}

	r.logger.Info("Position created/updated",
		zap.String("position_id", position.ID.String()),
		zap.String("user_id", position.UserID.String()),
		zap.String("basket_id", position.BasketID.String()),
	)
	return nil
}

// GetPositionMetrics retrieves aggregated portfolio metrics for a user
func (r *PositionRepository) GetPositionMetrics(ctx context.Context, userID uuid.UUID) (*entities.PortfolioMetrics, error) {
	query := `
		SELECT 
			p.id,
			p.basket_id,
			b.name as basket_name,
			p.quantity,
			p.avg_price,
			p.market_value,
			p.updated_at
		FROM positions p
		INNER JOIN baskets b ON p.basket_id = b.id
		WHERE p.user_id = $1
		ORDER BY p.market_value DESC
	`

	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		r.logger.Error("Failed to query positions for metrics",
			zap.Error(err),
			zap.String("user_id", userID.String()),
		)
		return nil, fmt.Errorf("failed to query positions for metrics: %w", err)
	}
	defer rows.Close()

	var positions []*entities.Position
	var basketNames = make(map[uuid.UUID]string)
	for rows.Next() {
		position := &entities.Position{}
		var basketName string
		if err := rows.Scan(
			&position.ID,
			&position.BasketID,
			&basketName,
			&position.Quantity,
			&position.AvgPrice,
			&position.MarketValue,
			&position.UpdatedAt,
		); err != nil {
			r.logger.Error("Failed to scan position row", zap.Error(err))
			return nil, fmt.Errorf("failed to scan position: %w", err)
		}
		position.UserID = userID
		positions = append(positions, position)
		basketNames[position.BasketID] = basketName
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating position rows", zap.Error(err))
		return nil, fmt.Errorf("error iterating positions: %w", err)
	}

	// Calculate aggregate metrics
	var totalValue float64
	allocationByBasket := make(map[string]float64)
	positionMetrics := make([]entities.PositionMetrics, 0, len(positions))

	for _, pos := range positions {
		marketValue, _ := pos.MarketValue.Float64()
		totalValue += marketValue
	}

	for _, pos := range positions {
		quantity, _ := pos.Quantity.Float64()
		avgPrice, _ := pos.AvgPrice.Float64()
		marketValue, _ := pos.MarketValue.Float64()
		costBasis := quantity * avgPrice
		unrealizedPL := marketValue - costBasis
		unrealizedPLPct := 0.0
		if costBasis > 0 {
			unrealizedPLPct = (unrealizedPL / costBasis) * 100
		}
		weight := 0.0
		if totalValue > 0 {
			weight = marketValue / totalValue
		}

		basketName := basketNames[pos.BasketID]
		allocationByBasket[basketName] = weight

		positionMetrics = append(positionMetrics, entities.PositionMetrics{
			BasketID:        pos.BasketID,
			BasketName:      basketName,
			Quantity:        quantity,
			AvgPrice:        avgPrice,
			CurrentValue:    marketValue,
			UnrealizedPL:    unrealizedPL,
			UnrealizedPLPct: unrealizedPLPct,
			Weight:          weight,
		})
	}

	r.logger.Debug("Retrieved position metrics",
		zap.String("user_id", userID.String()),
		zap.Int("positions_count", len(positionMetrics)),
		zap.Float64("total_value", totalValue),
	)

	// Return portfolio metrics with positions and allocations
	return &entities.PortfolioMetrics{
		TotalValue:         totalValue,
		Positions:          positionMetrics,
		AllocationByBasket: allocationByBasket,
		// Performance history and risk metrics will be populated by the service
	}, nil
}
