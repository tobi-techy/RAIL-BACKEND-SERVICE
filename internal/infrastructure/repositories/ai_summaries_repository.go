package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	domainrepos "github.com/rail-service/rail_service/internal/domain/repositories"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// AISummaryRepository provides database operations for AI summaries
type AISummaryRepository struct {
	db     *sql.DB
	logger *zap.Logger
	tracer trace.Tracer
}

// NewAISummaryRepository creates a new AI summary repository
func NewAISummaryRepository(db *sql.DB, logger *zap.Logger) domainrepos.AISummaryRepository {
	return &AISummaryRepository{
		db:     db,
		logger: logger,
		tracer: otel.Tracer("ai-summary-repository"),
	}
}

// Create creates a new AI summary record
func (r *AISummaryRepository) Create(ctx context.Context, summary *domainrepos.AISummary) error {
	ctx, span := r.tracer.Start(ctx, "repository.create_ai_summary", trace.WithAttributes(
		attribute.String("user_id", summary.UserID.String()),
		attribute.String("week_start", summary.WeekStart.Format("2006-01-02")),
	))
	defer span.End()

	query := `
		INSERT INTO ai_summaries (id, user_id, week_start, summary_md, artifact_uri, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, week_start) 
		DO UPDATE SET 
			summary_md = EXCLUDED.summary_md,
			artifact_uri = EXCLUDED.artifact_uri,
			created_at = EXCLUDED.created_at
	`

	_, err := r.db.ExecContext(
		ctx,
		query,
		summary.ID,
		summary.UserID,
		summary.WeekStart,
		summary.SummaryMD,
		summary.ArtifactURI,
		summary.CreatedAt,
	)

	if err != nil {
		span.RecordError(err)
		r.logger.Error("Failed to create AI summary",
			zap.Error(err),
			zap.String("user_id", summary.UserID.String()),
			zap.String("week_start", summary.WeekStart.Format("2006-01-02")),
		)
		return fmt.Errorf("failed to create AI summary: %w", err)
	}

	r.logger.Debug("AI summary created successfully",
		zap.String("id", summary.ID.String()),
		zap.String("user_id", summary.UserID.String()),
		zap.String("week_start", summary.WeekStart.Format("2006-01-02")),
	)

	return nil
}

// GetByID retrieves an AI summary by its ID
func (r *AISummaryRepository) GetByID(ctx context.Context, id uuid.UUID) (*domainrepos.AISummary, error) {
	ctx, span := r.tracer.Start(ctx, "repository.get_ai_summary_by_id", trace.WithAttributes(
		attribute.String("summary_id", id.String()),
	))
	defer span.End()

	query := `
		SELECT id, user_id, week_start, summary_md, artifact_uri, created_at
		FROM ai_summaries 
		WHERE id = $1
	`

	row := r.db.QueryRowContext(ctx, query, id)
	
	summary := &domainrepos.AISummary{}
	err := row.Scan(
		&summary.ID,
		&summary.UserID,
		&summary.WeekStart,
		&summary.SummaryMD,
		&summary.ArtifactURI,
		&summary.CreatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			span.SetAttributes(attribute.Bool("found", false))
			return nil, fmt.Errorf("AI summary not found: %w", err)
		}
		span.RecordError(err)
		r.logger.Error("Failed to get AI summary by ID",
			zap.Error(err),
			zap.String("summary_id", id.String()),
		)
		return nil, fmt.Errorf("failed to get AI summary: %w", err)
	}

	span.SetAttributes(attribute.Bool("found", true))
	return summary, nil
}

// GetLatestByUserID retrieves the most recent AI summary for a user
func (r *AISummaryRepository) GetLatestByUserID(ctx context.Context, userID uuid.UUID) (*domainrepos.AISummary, error) {
	ctx, span := r.tracer.Start(ctx, "repository.get_latest_ai_summary", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
	))
	defer span.End()

	query := `
		SELECT id, user_id, week_start, summary_md, artifact_uri, created_at
		FROM ai_summaries 
		WHERE user_id = $1
		ORDER BY week_start DESC, created_at DESC
		LIMIT 1
	`

	row := r.db.QueryRowContext(ctx, query, userID)
	
	summary := &domainrepos.AISummary{}
	err := row.Scan(
		&summary.ID,
		&summary.UserID,
		&summary.WeekStart,
		&summary.SummaryMD,
		&summary.ArtifactURI,
		&summary.CreatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			span.SetAttributes(attribute.Bool("found", false))
			return nil, fmt.Errorf("no AI summaries found for user")
		}
		span.RecordError(err)
		r.logger.Error("Failed to get latest AI summary",
			zap.Error(err),
			zap.String("user_id", userID.String()),
		)
		return nil, fmt.Errorf("failed to get latest AI summary: %w", err)
	}

	span.SetAttributes(
		attribute.Bool("found", true),
		attribute.String("week_start", summary.WeekStart.Format("2006-01-02")),
	)
	
	r.logger.Debug("Latest AI summary retrieved",
		zap.String("user_id", userID.String()),
		zap.String("summary_id", summary.ID.String()),
		zap.String("week_start", summary.WeekStart.Format("2006-01-02")),
	)

	return summary, nil
}

// GetByUserAndWeek retrieves an AI summary for a specific user and week
func (r *AISummaryRepository) GetByUserAndWeek(ctx context.Context, userID uuid.UUID, weekStart time.Time) (*domainrepos.AISummary, error) {
	ctx, span := r.tracer.Start(ctx, "repository.get_ai_summary_by_week", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.String("week_start", weekStart.Format("2006-01-02")),
	))
	defer span.End()

	query := `
		SELECT id, user_id, week_start, summary_md, artifact_uri, created_at
		FROM ai_summaries 
		WHERE user_id = $1 AND week_start = $2
	`

	row := r.db.QueryRowContext(ctx, query, userID, weekStart)
	
	summary := &domainrepos.AISummary{}
	err := row.Scan(
		&summary.ID,
		&summary.UserID,
		&summary.WeekStart,
		&summary.SummaryMD,
		&summary.ArtifactURI,
		&summary.CreatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			span.SetAttributes(attribute.Bool("found", false))
			return nil, sql.ErrNoRows // Return the standard not found error
		}
		span.RecordError(err)
		r.logger.Error("Failed to get AI summary by week",
			zap.Error(err),
			zap.String("user_id", userID.String()),
			zap.String("week_start", weekStart.Format("2006-01-02")),
		)
		return nil, fmt.Errorf("failed to get AI summary by week: %w", err)
	}

	span.SetAttributes(attribute.Bool("found", true))
	return summary, nil
}

// ListByUserID retrieves AI summaries for a user with pagination
func (r *AISummaryRepository) ListByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domainrepos.AISummary, error) {
	ctx, span := r.tracer.Start(ctx, "repository.list_ai_summaries", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.Int("limit", limit),
		attribute.Int("offset", offset),
	))
	defer span.End()

	query := `
		SELECT id, user_id, week_start, summary_md, artifact_uri, created_at
		FROM ai_summaries 
		WHERE user_id = $1
		ORDER BY week_start DESC, created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := r.db.QueryContext(ctx, query, userID, limit, offset)
	if err != nil {
		span.RecordError(err)
		r.logger.Error("Failed to list AI summaries",
			zap.Error(err),
			zap.String("user_id", userID.String()),
		)
		return nil, fmt.Errorf("failed to list AI summaries: %w", err)
	}
	defer rows.Close()

	var summaries []*domainrepos.AISummary
	for rows.Next() {
		summary := &domainrepos.AISummary{}
		err := rows.Scan(
			&summary.ID,
			&summary.UserID,
			&summary.WeekStart,
			&summary.SummaryMD,
			&summary.ArtifactURI,
			&summary.CreatedAt,
		)
		if err != nil {
			span.RecordError(err)
			r.logger.Error("Failed to scan AI summary row",
				zap.Error(err),
				zap.String("user_id", userID.String()),
			)
			return nil, fmt.Errorf("failed to scan AI summary: %w", err)
		}
		summaries = append(summaries, summary)
	}

	if err := rows.Err(); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to iterate AI summaries: %w", err)
	}

	span.SetAttributes(attribute.Int("count", len(summaries)))
	
	r.logger.Debug("AI summaries retrieved",
		zap.String("user_id", userID.String()),
		zap.Int("count", len(summaries)),
		zap.Int("limit", limit),
		zap.Int("offset", offset),
	)

	return summaries, nil
}

// Update updates an existing AI summary record
func (r *AISummaryRepository) Update(ctx context.Context, summary *domainrepos.AISummary) error {
	ctx, span := r.tracer.Start(ctx, "repository.update_ai_summary", trace.WithAttributes(
		attribute.String("summary_id", summary.ID.String()),
		attribute.String("user_id", summary.UserID.String()),
	))
	defer span.End()

	query := `
		UPDATE ai_summaries 
		SET summary_md = $2, artifact_uri = $3, created_at = $4
		WHERE id = $1
	`

	result, err := r.db.ExecContext(
		ctx,
		query,
		summary.ID,
		summary.SummaryMD,
		summary.ArtifactURI,
		summary.CreatedAt,
	)

	if err != nil {
		span.RecordError(err)
		r.logger.Error("Failed to update AI summary",
			zap.Error(err),
			zap.String("summary_id", summary.ID.String()),
		)
		return fmt.Errorf("failed to update AI summary: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		span.SetAttributes(attribute.Bool("found", false))
		return fmt.Errorf("AI summary not found for update")
	}

	span.SetAttributes(attribute.Bool("updated", true))
	
	r.logger.Debug("AI summary updated successfully",
		zap.String("summary_id", summary.ID.String()),
		zap.String("user_id", summary.UserID.String()),
	)

	return nil
}

// Delete deletes an AI summary by ID
func (r *AISummaryRepository) Delete(ctx context.Context, id uuid.UUID) error {
	ctx, span := r.tracer.Start(ctx, "repository.delete_ai_summary", trace.WithAttributes(
		attribute.String("summary_id", id.String()),
	))
	defer span.End()

	query := `DELETE FROM ai_summaries WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		span.RecordError(err)
		r.logger.Error("Failed to delete AI summary",
			zap.Error(err),
			zap.String("summary_id", id.String()),
		)
		return fmt.Errorf("failed to delete AI summary: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		span.SetAttributes(attribute.Bool("found", false))
		return fmt.Errorf("AI summary not found for deletion")
	}

	span.SetAttributes(attribute.Bool("deleted", true))
	
	r.logger.Debug("AI summary deleted successfully",
		zap.String("summary_id", id.String()),
	)

	return nil
}

