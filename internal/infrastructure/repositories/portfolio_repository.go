package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// PortfolioRepository handles portfolio-related database operations
type PortfolioRepository struct {
	db     *sql.DB
	logger *zap.Logger
	tracer trace.Tracer
}

// NewPortfolioRepository creates a new portfolio repository instance
func NewPortfolioRepository(db *sql.DB, logger *zap.Logger) *PortfolioRepository {
	return &PortfolioRepository{
		db:     db,
		logger: logger,
		tracer: otel.Tracer("portfolio-repository"),
	}
}

// GetPortfolioPerformance retrieves portfolio performance data for a user within a date range
func (r *PortfolioRepository) GetPortfolioPerformance(ctx context.Context, userID uuid.UUID, startDate, endDate time.Time) ([]*entities.PerformancePoint, error) {
	ctx, span := r.tracer.Start(ctx, "repository.get_portfolio_performance", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.String("start_date", startDate.Format("2006-01-02")),
		attribute.String("end_date", endDate.Format("2006-01-02")),
	))
	defer span.End()

	query := `
		SELECT date, nav, pnl, created_at
		FROM portfolio_performance
		WHERE user_id = $1 AND date >= $2 AND date <= $3
		ORDER BY date ASC
	`

	rows, err := r.db.QueryContext(ctx, query, userID, startDate, endDate)
	if err != nil {
		span.RecordError(err)
		r.logger.Error("Failed to query portfolio performance",
			zap.Error(err),
			zap.String("user_id", userID.String()),
		)
		return nil, fmt.Errorf("failed to query portfolio performance: %w", err)
	}
	defer rows.Close()

	var points []*entities.PerformancePoint
	for rows.Next() {
		var nav, pnl decimal.Decimal
		var date, createdAt time.Time

		if err := rows.Scan(&date, &nav, &pnl, &createdAt); err != nil {
			span.RecordError(err)
			r.logger.Error("Failed to scan performance row", zap.Error(err))
			return nil, fmt.Errorf("failed to scan performance row: %w", err)
		}

		navFloat, _ := nav.Float64()
		pnlFloat, _ := pnl.Float64()

		points = append(points, &entities.PerformancePoint{
			Date:  date,
			Value: navFloat,
			PnL:   pnlFloat,
		})
	}

	if err := rows.Err(); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("error iterating performance rows: %w", err)
	}

	span.SetAttributes(attribute.Int("points_count", len(points)))

	r.logger.Debug("Retrieved portfolio performance",
		zap.String("user_id", userID.String()),
		zap.Int("points_count", len(points)),
	)

	return points, nil
}

// GetPortfolioValue retrieves the portfolio value for a specific date
func (r *PortfolioRepository) GetPortfolioValue(ctx context.Context, userID uuid.UUID, date time.Time) (decimal.Decimal, error) {
	ctx, span := r.tracer.Start(ctx, "repository.get_portfolio_value", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.String("date", date.Format("2006-01-02")),
	))
	defer span.End()

	query := `
		SELECT nav
		FROM portfolio_performance
		WHERE user_id = $1 AND date = $2
		ORDER BY created_at DESC
		LIMIT 1
	`

	var nav decimal.Decimal
	err := r.db.QueryRowContext(ctx, query, userID, date).Scan(&nav)

	if err == sql.ErrNoRows {
		span.SetAttributes(attribute.Bool("found", false))
		r.logger.Debug("No portfolio value found for date",
			zap.String("user_id", userID.String()),
			zap.String("date", date.Format("2006-01-02")),
		)
		// Return the current total market value from positions as fallback
		return r.getCurrentPortfolioValue(ctx, userID)
	}

	if err != nil {
		span.RecordError(err)
		r.logger.Error("Failed to get portfolio value",
			zap.Error(err),
			zap.String("user_id", userID.String()),
		)
		return decimal.Zero, fmt.Errorf("failed to get portfolio value: %w", err)
	}

	span.SetAttributes(attribute.Bool("found", true))
	return nav, nil
}

// getCurrentPortfolioValue calculates current portfolio value from positions
func (r *PortfolioRepository) getCurrentPortfolioValue(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	query := `
		SELECT COALESCE(SUM(market_value), 0)
		FROM positions
		WHERE user_id = $1
	`

	var totalValue decimal.Decimal
	err := r.db.QueryRowContext(ctx, query, userID).Scan(&totalValue)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to calculate current portfolio value: %w", err)
	}

	return totalValue, nil
}

// RecordPerformance stores a portfolio performance snapshot
func (r *PortfolioRepository) RecordPerformance(ctx context.Context, userID uuid.UUID, date time.Time, nav, pnl decimal.Decimal) error {
	ctx, span := r.tracer.Start(ctx, "repository.record_performance", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.String("date", date.Format("2006-01-02")),
	))
	defer span.End()

	query := `
		INSERT INTO portfolio_performance (user_id, date, nav, pnl, created_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (user_id, date)
		DO UPDATE SET
			nav = EXCLUDED.nav,
			pnl = EXCLUDED.pnl,
			created_at = EXCLUDED.created_at
	`

	_, err := r.db.ExecContext(ctx, query, userID, date, nav, pnl)
	if err != nil {
		span.RecordError(err)
		r.logger.Error("Failed to record performance",
			zap.Error(err),
			zap.String("user_id", userID.String()),
		)
		return fmt.Errorf("failed to record performance: %w", err)
	}

	r.logger.Debug("Portfolio performance recorded",
		zap.String("user_id", userID.String()),
		zap.String("date", date.Format("2006-01-02")),
	)

	return nil
}
