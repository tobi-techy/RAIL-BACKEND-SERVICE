package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// OrderRepository handles order database operations
type OrderRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewOrderRepository creates a new order repository instance
func NewOrderRepository(db *sql.DB, logger *zap.Logger) *OrderRepository {
	return &OrderRepository{
		db:     db,
		logger: logger,
	}
}

// Create creates a new order record
func (r *OrderRepository) Create(ctx context.Context, order *entities.Order) error {
	query := `
		INSERT INTO orders (id, user_id, basket_id, side, amount, status, brokerage_ref, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err := r.db.ExecContext(ctx, query,
		order.ID,
		order.UserID,
		order.BasketID,
		order.Side,
		order.Amount,
		order.Status,
		order.BrokerageRef,
		order.CreatedAt,
		order.UpdatedAt,
	)

	if err != nil {
		r.logger.Error("Failed to create order",
			zap.Error(err),
			zap.String("order_id", order.ID.String()),
			zap.String("user_id", order.UserID.String()),
		)
		return fmt.Errorf("failed to create order: %w", err)
	}

	r.logger.Info("Order created",
		zap.String("order_id", order.ID.String()),
		zap.String("user_id", order.UserID.String()),
		zap.String("status", string(order.Status)),
	)
	return nil
}

// GetByID retrieves an order by ID
func (r *OrderRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.Order, error) {
	query := `
		SELECT id, user_id, basket_id, side, amount, status, brokerage_ref, created_at, updated_at
		FROM orders
		WHERE id = $1
	`

	order := &entities.Order{}
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&order.ID,
		&order.UserID,
		&order.BasketID,
		&order.Side,
		&order.Amount,
		&order.Status,
		&order.BrokerageRef,
		&order.CreatedAt,
		&order.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		r.logger.Debug("Order not found", zap.String("order_id", id.String()))
		return nil, nil
	}

	if err != nil {
		r.logger.Error("Failed to get order", zap.Error(err), zap.String("order_id", id.String()))
		return nil, fmt.Errorf("failed to get order: %w", err)
	}

	return order, nil
}

// GetByUserID retrieves orders for a specific user with pagination and optional status filter
func (r *OrderRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int, status *entities.OrderStatus) ([]*entities.Order, error) {
	var query string
	var args []interface{}

	if status != nil {
		query = `
			SELECT id, user_id, basket_id, side, amount, status, brokerage_ref, created_at, updated_at
			FROM orders
			WHERE user_id = $1 AND status = $2
			ORDER BY created_at DESC
			LIMIT $3 OFFSET $4
		`
		args = []interface{}{userID, *status, limit, offset}
	} else {
		query = `
			SELECT id, user_id, basket_id, side, amount, status, brokerage_ref, created_at, updated_at
			FROM orders
			WHERE user_id = $1
			ORDER BY created_at DESC
			LIMIT $2 OFFSET $3
		`
		args = []interface{}{userID, limit, offset}
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to query orders", zap.Error(err), zap.String("user_id", userID.String()))
		return nil, fmt.Errorf("failed to query orders: %w", err)
	}
	defer rows.Close()

	var orders []*entities.Order
	for rows.Next() {
		order := &entities.Order{}
		if err := rows.Scan(
			&order.ID,
			&order.UserID,
			&order.BasketID,
			&order.Side,
			&order.Amount,
			&order.Status,
			&order.BrokerageRef,
			&order.CreatedAt,
			&order.UpdatedAt,
		); err != nil {
			r.logger.Error("Failed to scan order row", zap.Error(err))
			return nil, fmt.Errorf("failed to scan order: %w", err)
		}
		orders = append(orders, order)
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating order rows", zap.Error(err))
		return nil, fmt.Errorf("error iterating orders: %w", err)
	}

	r.logger.Debug("Retrieved orders", zap.String("user_id", userID.String()), zap.Int("count", len(orders)))
	return orders, nil
}

// UpdateStatus updates an order's status and optionally sets the brokerage reference
func (r *OrderRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status entities.OrderStatus, brokerageRef *string) error {
	query := `
		UPDATE orders
		SET status = $1, brokerage_ref = $2, updated_at = NOW()
		WHERE id = $3
	`

	result, err := r.db.ExecContext(ctx, query, status, brokerageRef, id)
	if err != nil {
		r.logger.Error("Failed to update order status",
			zap.Error(err),
			zap.String("order_id", id.String()),
			zap.String("status", string(status)),
		)
		return fmt.Errorf("failed to update order status: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		r.logger.Warn("Order not found for status update", zap.String("order_id", id.String()))
		return fmt.Errorf("order not found: %s", id.String())
	}

	r.logger.Info("Order status updated",
		zap.String("order_id", id.String()),
		zap.String("status", string(status)),
	)
	return nil
}

// GetStuckOrders returns orders stuck in non-terminal states beyond the SLA threshold
// Used for status enquiry reconciliation when webhooks fail
func (r *OrderRepository) GetStuckOrders(ctx context.Context, slaThreshold time.Duration) ([]*entities.Order, error) {
	cutoff := time.Now().Add(-slaThreshold)
	query := `
		SELECT id, user_id, basket_id, side, amount, status, brokerage_ref, created_at, updated_at
		FROM orders
		WHERE status NOT IN ($1, $2, $3, $4)
		  AND updated_at < $5
		ORDER BY updated_at ASC
		LIMIT 100
	`

	rows, err := r.db.QueryContext(ctx, query,
		entities.OrderStatusFilled,
		entities.OrderStatusFailed,
		entities.OrderStatusCanceled,
		entities.OrderStatusRejected,
		cutoff,
	)
	if err != nil {
		r.logger.Error("Failed to query stuck orders", zap.Error(err))
		return nil, fmt.Errorf("failed to get stuck orders: %w", err)
	}
	defer rows.Close()

	var orders []*entities.Order
	for rows.Next() {
		order := &entities.Order{}
		if err := rows.Scan(
			&order.ID,
			&order.UserID,
			&order.BasketID,
			&order.Side,
			&order.Amount,
			&order.Status,
			&order.BrokerageRef,
			&order.CreatedAt,
			&order.UpdatedAt,
		); err != nil {
			r.logger.Error("Failed to scan stuck order row", zap.Error(err))
			return nil, fmt.Errorf("failed to scan order: %w", err)
		}
		orders = append(orders, order)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating stuck orders: %w", err)
	}

	r.logger.Info("Found stuck orders", zap.Int("count", len(orders)))
	return orders, nil
}

// MarkTimeout marks an order as timed out (no response within SLA)
func (r *OrderRepository) MarkTimeout(ctx context.Context, id uuid.UUID) error {
	query := `
		UPDATE orders
		SET status = $1, updated_at = NOW()
		WHERE id = $2
	`

	result, err := r.db.ExecContext(ctx, query, entities.OrderStatusTimeout, id)
	if err != nil {
		r.logger.Error("Failed to mark order timeout", zap.Error(err), zap.String("order_id", id.String()))
		return fmt.Errorf("failed to mark order timeout: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("order not found: %s", id.String())
	}

	r.logger.Info("Order marked as timeout", zap.String("order_id", id.String()))
	return nil
}
