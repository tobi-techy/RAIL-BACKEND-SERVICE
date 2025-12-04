package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// UserNewsRepository handles user news data persistence
type UserNewsRepository struct {
	db     *sql.DB
	logger *zap.Logger
	tracer trace.Tracer
}

// NewUserNewsRepository creates a new user news repository
func NewUserNewsRepository(db *sql.DB, logger *zap.Logger) *UserNewsRepository {
	return &UserNewsRepository{
		db:     db,
		logger: logger,
		tracer: otel.Tracer("user-news-repository"),
	}
}

// Create creates a new user news record
func (r *UserNewsRepository) Create(ctx context.Context, news *entities.UserNews) error {
	ctx, span := r.tracer.Start(ctx, "repository.create_user_news", trace.WithAttributes(
		attribute.String("user_id", news.UserID.String()),
		attribute.String("source", news.Source),
	))
	defer span.End()

	query := `
		INSERT INTO user_news (id, user_id, source, title, summary, url, related_symbols, published_at, is_read, relevance_score, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`

	_, err := r.db.ExecContext(
		ctx,
		query,
		news.ID,
		news.UserID,
		news.Source,
		news.Title,
		news.Summary,
		news.URL,
		pq.Array(news.RelatedSymbols),
		news.PublishedAt,
		news.IsRead,
		news.RelevanceScore,
		news.CreatedAt,
	)

	if err != nil {
		span.RecordError(err)
		r.logger.Error("Failed to create user news",
			zap.Error(err),
			zap.String("user_id", news.UserID.String()),
		)
		return fmt.Errorf("failed to create user news: %w", err)
	}

	r.logger.Debug("User news created",
		zap.String("id", news.ID.String()),
		zap.String("user_id", news.UserID.String()),
		zap.String("title", news.Title),
	)

	return nil
}

// GetByID retrieves a user news article by ID
func (r *UserNewsRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.UserNews, error) {
	ctx, span := r.tracer.Start(ctx, "repository.get_user_news_by_id", trace.WithAttributes(
		attribute.String("news_id", id.String()),
	))
	defer span.End()

	query := `
		SELECT id, user_id, source, title, summary, url, related_symbols, published_at, is_read, relevance_score, created_at
		FROM user_news
		WHERE id = $1
	`

	row := r.db.QueryRowContext(ctx, query, id)
	news, err := r.scanNews(row)
	if err != nil {
		if err == sql.ErrNoRows {
			span.SetAttributes(attribute.Bool("found", false))
			return nil, fmt.Errorf("news not found")
		}
		span.RecordError(err)
		return nil, fmt.Errorf("failed to get news: %w", err)
	}

	span.SetAttributes(attribute.Bool("found", true))
	return news, nil
}

// GetByUserID retrieves news for a user with optional filters
func (r *UserNewsRepository) GetByUserID(ctx context.Context, userID uuid.UUID, isRead *bool, symbols []string, limit, offset int) ([]*entities.UserNews, error) {
	ctx, span := r.tracer.Start(ctx, "repository.get_user_news", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.Int("limit", limit),
		attribute.Int("offset", offset),
	))
	defer span.End()

	query := `
		SELECT id, user_id, source, title, summary, url, related_symbols, published_at, is_read, relevance_score, created_at
		FROM user_news
		WHERE user_id = $1
	`
	args := []interface{}{userID}
	argCount := 1

	if isRead != nil {
		argCount++
		query += fmt.Sprintf(" AND is_read = $%d", argCount)
		args = append(args, *isRead)
	}

	if len(symbols) > 0 {
		argCount++
		query += fmt.Sprintf(" AND related_symbols && $%d", argCount)
		args = append(args, pq.Array(symbols))
	}

	query += " ORDER BY published_at DESC, relevance_score DESC"

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
		r.logger.Error("Failed to query user news", zap.Error(err), zap.String("user_id", userID.String()))
		return nil, fmt.Errorf("failed to query news: %w", err)
	}
	defer rows.Close()

	newsList := []*entities.UserNews{}
	for rows.Next() {
		news, err := r.scanNews(rows)
		if err != nil {
			span.RecordError(err)
			return nil, fmt.Errorf("failed to scan news: %w", err)
		}
		newsList = append(newsList, news)
	}

	if err := rows.Err(); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	span.SetAttributes(attribute.Int("count", len(newsList)))
	return newsList, nil
}

// GetWeeklyNews retrieves news for a user within a week
func (r *UserNewsRepository) GetWeeklyNews(ctx context.Context, userID uuid.UUID, weekStart, weekEnd time.Time) ([]*entities.UserNews, error) {
	ctx, span := r.tracer.Start(ctx, "repository.get_weekly_news", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
	))
	defer span.End()

	query := `
		SELECT id, user_id, source, title, summary, url, related_symbols, published_at, is_read, relevance_score, created_at
		FROM user_news
		WHERE user_id = $1 AND published_at >= $2 AND published_at <= $3
		ORDER BY relevance_score DESC, published_at DESC
		LIMIT 10
	`

	rows, err := r.db.QueryContext(ctx, query, userID, weekStart, weekEnd)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to query weekly news: %w", err)
	}
	defer rows.Close()

	newsList := []*entities.UserNews{}
	for rows.Next() {
		news, err := r.scanNews(rows)
		if err != nil {
			span.RecordError(err)
			return nil, fmt.Errorf("failed to scan news: %w", err)
		}
		newsList = append(newsList, news)
	}

	if err := rows.Err(); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	span.SetAttributes(attribute.Int("count", len(newsList)))
	return newsList, nil
}

// MarkAsRead marks a news article as read
func (r *UserNewsRepository) MarkAsRead(ctx context.Context, newsID uuid.UUID) error {
	ctx, span := r.tracer.Start(ctx, "repository.mark_news_as_read", trace.WithAttributes(
		attribute.String("news_id", newsID.String()),
	))
	defer span.End()

	query := `UPDATE user_news SET is_read = true WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, newsID)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to mark news as read: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if affected == 0 {
		return fmt.Errorf("news not found")
	}

	r.logger.Debug("News marked as read", zap.String("news_id", newsID.String()))
	return nil
}

// MarkMultipleAsRead marks multiple news articles as read
func (r *UserNewsRepository) MarkMultipleAsRead(ctx context.Context, newsIDs []uuid.UUID) error {
	ctx, span := r.tracer.Start(ctx, "repository.mark_multiple_news_as_read", trace.WithAttributes(
		attribute.Int("count", len(newsIDs)),
	))
	defer span.End()

	if len(newsIDs) == 0 {
		return nil
	}

	query := `UPDATE user_news SET is_read = true WHERE id = ANY($1)`

	// Convert UUIDs to strings for pq.Array
	ids := make([]string, len(newsIDs))
	for i, id := range newsIDs {
		ids[i] = id.String()
	}

	result, err := r.db.ExecContext(ctx, query, pq.Array(ids))
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to mark news as read: %w", err)
	}

	affected, _ := result.RowsAffected()
	span.SetAttributes(attribute.Int64("affected", affected))

	r.logger.Debug("Multiple news marked as read", zap.Int("count", len(newsIDs)), zap.Int64("affected", affected))
	return nil
}

// GetUnreadCount returns the count of unread news for a user
func (r *UserNewsRepository) GetUnreadCount(ctx context.Context, userID uuid.UUID) (int, error) {
	ctx, span := r.tracer.Start(ctx, "repository.get_unread_count", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
	))
	defer span.End()

	query := `SELECT COUNT(*) FROM user_news WHERE user_id = $1 AND is_read = false`

	var count int
	err := r.db.QueryRowContext(ctx, query, userID).Scan(&count)
	if err != nil {
		span.RecordError(err)
		return 0, fmt.Errorf("failed to get unread count: %w", err)
	}

	span.SetAttributes(attribute.Int("unread_count", count))
	return count, nil
}

// DeleteOlderThan deletes news older than the specified date
func (r *UserNewsRepository) DeleteOlderThan(ctx context.Context, date time.Time) (int64, error) {
	ctx, span := r.tracer.Start(ctx, "repository.delete_old_news")
	defer span.End()

	query := `DELETE FROM user_news WHERE created_at < $1`

	result, err := r.db.ExecContext(ctx, query, date)
	if err != nil {
		span.RecordError(err)
		return 0, fmt.Errorf("failed to delete old news: %w", err)
	}

	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	span.SetAttributes(attribute.Int64("deleted_count", deleted))
	r.logger.Info("Deleted old news", zap.Int64("count", deleted), zap.Time("before_date", date))

	return deleted, nil
}

// GetBySymbols retrieves news related to specific symbols for a user
func (r *UserNewsRepository) GetBySymbols(ctx context.Context, userID uuid.UUID, symbols []string, limit int) ([]*entities.UserNews, error) {
	ctx, span := r.tracer.Start(ctx, "repository.get_news_by_symbols", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
		attribute.StringSlice("symbols", symbols),
	))
	defer span.End()

	if len(symbols) == 0 {
		return []*entities.UserNews{}, nil
	}

	query := `
		SELECT id, user_id, source, title, summary, url, related_symbols, published_at, is_read, relevance_score, created_at
		FROM user_news
		WHERE user_id = $1 AND related_symbols && $2
		ORDER BY relevance_score DESC, published_at DESC
		LIMIT $3
	`

	rows, err := r.db.QueryContext(ctx, query, userID, pq.Array(symbols), limit)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to query news by symbols: %w", err)
	}
	defer rows.Close()

	newsList := []*entities.UserNews{}
	for rows.Next() {
		news, err := r.scanNews(rows)
		if err != nil {
			span.RecordError(err)
			return nil, fmt.Errorf("failed to scan news: %w", err)
		}
		newsList = append(newsList, news)
	}

	if err := rows.Err(); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	span.SetAttributes(attribute.Int("count", len(newsList)))
	return newsList, nil
}

// scanNews scans a row into a UserNews entity
func (r *UserNewsRepository) scanNews(scanner interface {
	Scan(dest ...interface{}) error
}) (*entities.UserNews, error) {
	var news entities.UserNews
	var summary sql.NullString
	var relatedSymbols pq.StringArray

	err := scanner.Scan(
		&news.ID,
		&news.UserID,
		&news.Source,
		&news.Title,
		&summary,
		&news.URL,
		&relatedSymbols,
		&news.PublishedAt,
		&news.IsRead,
		&news.RelevanceScore,
		&news.CreatedAt,
	)

	if err != nil {
		return nil, err
	}

	if summary.Valid {
		news.Summary = summary.String
	}

	news.RelatedSymbols = relatedSymbols

	return &news, nil
}
