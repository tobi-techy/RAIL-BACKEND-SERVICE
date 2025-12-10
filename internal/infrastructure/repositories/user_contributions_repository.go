package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// UserContributionsRepository handles user contribution data persistence
type UserContributionsRepository struct {
	db     *sql.DB
	logger *zap.Logger
	tracer trace.Tracer
}

// NewUserContributionsRepository creates a new user contributions repository
func NewUserContributionsRepository(db *sql.DB, logger *zap.Logger) *UserContributionsRepository {
	return &UserContributionsRepository{
		db:     db,
		logger: logger,
		tracer: otel.Tracer("user-contributions-repository"),
	}
}

// Create creates a new user contribution record
func (r *UserContributionsRepository) Create(ctx context.Context, contribution *entities.UserContribution) error {
	ctx, span := r.tracer.Start(ctx, "repository.create_user_contribution", trace.WithAttributes(
		attribute.String("user_id", contribution.UserID.String()),
		attribute.String("type", string(contribution.Type)),
	))
	defer span.End()

	metadataJSON, err := json.Marshal(contribution.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := `
		INSERT INTO user_contributions (id, user_id, type, amount, source, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`

	_, err = r.db.ExecContext(
		ctx,
		query,
		contribution.ID,
		contribution.UserID,
		contribution.Type,
		contribution.Amount,
		contribution.Source,
		metadataJSON,
		contribution.CreatedAt,
	)

	if err != nil {
		span.RecordError(err)
		r.logger.Error("Failed to create user contribution",
			zap.Error(err),
			zap.String("user_id", contribution.UserID.String()),
			zap.String("type", string(contribution.Type)),
		)
		return fmt.Errorf("failed to create user contribution: %w", err)
	}

	r.logger.Debug("User contribution created",
		zap.String("id", contribution.ID.String()),
		zap.String("user_id", contribution.UserID.String()),
		zap.String("type", string(contribution.Type)),
	)

	return nil
}

// GetByID retrieves a user contribution by ID
func (r *UserContributionsRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.UserContribution, error) {
	ctx, span := r.tracer.Start(ctx, "repository.get_user_contribution_by_id", trace.WithAttributes(
		attribute.String("contribution_id", id.String()),
	))
	defer span.End()

	query := `
		SELECT id, user_id, type, amount, source, metadata, created_at
		FROM user_contributions
		WHERE id = $1
	`

	row := r.db.QueryRowContext(ctx, query, id)
	contribution, err := r.scanContribution(row)
	if err != nil {
		if err == sql.ErrNoRows {
			span.SetAttributes(attribute.Bool("found", false))
			return nil, fmt.Errorf("contribution not found")
		}
		span.RecordError(err)
		return nil, fmt.Errorf("failed to get contribution: %w", err)
	}

	span.SetAttributes(attribute.Bool("found", true))
	return contribution, nil
}

// GetByUserID retrieves all contributions for a user with optional filters
func (r *UserContributionsRepository) GetByUserID(ctx context.Context, userID uuid.UUID, contributionType *entities.ContributionType, startDate, endDate *time.Time, limit, offset int) ([]*entities.UserContribution, error) {
	ctx, span := r.tracer.Start(ctx, "repository.get_user_contributions", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.Int("limit", limit),
		attribute.Int("offset", offset),
	))
	defer span.End()

	query := `
		SELECT id, user_id, type, amount, source, metadata, created_at
		FROM user_contributions
		WHERE user_id = $1
	`
	args := []interface{}{userID}
	argCount := 1

	if contributionType != nil {
		argCount++
		query += fmt.Sprintf(" AND type = $%d", argCount)
		args = append(args, *contributionType)
	}

	if startDate != nil {
		argCount++
		query += fmt.Sprintf(" AND created_at >= $%d", argCount)
		args = append(args, *startDate)
	}

	if endDate != nil {
		argCount++
		query += fmt.Sprintf(" AND created_at <= $%d", argCount)
		args = append(args, *endDate)
	}

	query += " ORDER BY created_at DESC"

	if limit > 0 {
		argCount++
		query += fmt.Sprintf(" LIMIT $%d", argCount)
		args = append(args, limit)
	}

	if offset > 0 {
		argCount++
		query += fmt.Sprintf(" OFFSET $%d", argCount)
		args = append(args, offset)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		span.RecordError(err)
		r.logger.Error("Failed to query user contributions", zap.Error(err), zap.String("user_id", userID.String()))
		return nil, fmt.Errorf("failed to query contributions: %w", err)
	}
	defer rows.Close()

	contributions := []*entities.UserContribution{}
	for rows.Next() {
		contribution, err := r.scanContribution(rows)
		if err != nil {
			span.RecordError(err)
			return nil, fmt.Errorf("failed to scan contribution: %w", err)
		}
		contributions = append(contributions, contribution)
	}

	if err := rows.Err(); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	span.SetAttributes(attribute.Int("count", len(contributions)))
	return contributions, nil
}

// GetTotalByType calculates total contributions by type for a user within a date range
func (r *UserContributionsRepository) GetTotalByType(ctx context.Context, userID uuid.UUID, startDate, endDate time.Time) (map[entities.ContributionType]string, error) {
	ctx, span := r.tracer.Start(ctx, "repository.get_total_contributions_by_type", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
	))
	defer span.End()

	query := `
		SELECT type, COALESCE(SUM(amount), 0) as total
		FROM user_contributions
		WHERE user_id = $1 AND created_at >= $2 AND created_at <= $3
		GROUP BY type
	`

	rows, err := r.db.QueryContext(ctx, query, userID, startDate, endDate)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to calculate totals: %w", err)
	}
	defer rows.Close()

	totals := make(map[entities.ContributionType]string)
	for rows.Next() {
		var contributionType entities.ContributionType
		var total string
		if err := rows.Scan(&contributionType, &total); err != nil {
			span.RecordError(err)
			return nil, fmt.Errorf("failed to scan total: %w", err)
		}
		totals[contributionType] = total
	}

	return totals, nil
}

// scanContribution scans a row into a UserContribution entity
func (r *UserContributionsRepository) scanContribution(scanner interface {
	Scan(dest ...interface{}) error
}) (*entities.UserContribution, error) {
	var contribution entities.UserContribution
	var metadataJSON []byte
	var source sql.NullString

	err := scanner.Scan(
		&contribution.ID,
		&contribution.UserID,
		&contribution.Type,
		&contribution.Amount,
		&source,
		&metadataJSON,
		&contribution.CreatedAt,
	)

	if err != nil {
		return nil, err
	}

	if source.Valid {
		contribution.Source = source.String
	}

	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &contribution.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}

	return &contribution, nil
}

// DeleteOlderThan deletes contributions older than the specified date
func (r *UserContributionsRepository) DeleteOlderThan(ctx context.Context, date time.Time) (int64, error) {
	ctx, span := r.tracer.Start(ctx, "repository.delete_old_contributions")
	defer span.End()

	query := `DELETE FROM user_contributions WHERE created_at < $1`
	
	result, err := r.db.ExecContext(ctx, query, date)
	if err != nil {
		span.RecordError(err)
		return 0, fmt.Errorf("failed to delete old contributions: %w", err)
	}

	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	span.SetAttributes(attribute.Int64("deleted_count", deleted))
	r.logger.Info("Deleted old contributions", zap.Int64("count", deleted), zap.Time("before_date", date))

	return deleted, nil
}
