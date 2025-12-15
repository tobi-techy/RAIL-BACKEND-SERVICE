package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// InvestmentStreakRepository handles investment streak data persistence
type InvestmentStreakRepository struct {
	db     *sql.DB
	logger *zap.Logger
	tracer trace.Tracer
}

// NewInvestmentStreakRepository creates a new investment streak repository
func NewInvestmentStreakRepository(db *sql.DB, logger *zap.Logger) *InvestmentStreakRepository {
	return &InvestmentStreakRepository{
		db:     db,
		logger: logger,
		tracer: otel.Tracer("investment-streak-repository"),
	}
}

// Upsert creates or updates an investment streak record
func (r *InvestmentStreakRepository) Upsert(ctx context.Context, streak *entities.InvestmentStreak) error {
	ctx, span := r.tracer.Start(ctx, "repository.upsert_investment_streak", trace.WithAttributes(
		attribute.String("user_id", streak.UserID.String()),
		attribute.Int("current_streak", streak.CurrentStreak),
	))
	defer span.End()

	query := `
		INSERT INTO investment_streaks (user_id, current_streak, longest_streak, last_investment_date, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id) 
		DO UPDATE SET 
			current_streak = EXCLUDED.current_streak,
			longest_streak = EXCLUDED.longest_streak,
			last_investment_date = EXCLUDED.last_investment_date,
			updated_at = EXCLUDED.updated_at
	`

	_, err := r.db.ExecContext(
		ctx,
		query,
		streak.UserID,
		streak.CurrentStreak,
		streak.LongestStreak,
		streak.LastInvestmentDate,
		streak.UpdatedAt,
	)

	if err != nil {
		span.RecordError(err)
		r.logger.Error("Failed to upsert investment streak",
			zap.Error(err),
			zap.String("user_id", streak.UserID.String()),
		)
		return fmt.Errorf("failed to upsert investment streak: %w", err)
	}

	r.logger.Debug("Investment streak upserted",
		zap.String("user_id", streak.UserID.String()),
		zap.Int("current_streak", streak.CurrentStreak),
		zap.Int("longest_streak", streak.LongestStreak),
	)

	return nil
}

// GetByUserID retrieves an investment streak by user ID
func (r *InvestmentStreakRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.InvestmentStreak, error) {
	ctx, span := r.tracer.Start(ctx, "repository.get_investment_streak", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
	))
	defer span.End()

	query := `
		SELECT user_id, current_streak, longest_streak, last_investment_date, updated_at
		FROM investment_streaks
		WHERE user_id = $1
	`

	row := r.db.QueryRowContext(ctx, query, userID)
	streak, err := r.scanStreak(row)
	if err != nil {
		if err == sql.ErrNoRows {
			span.SetAttributes(attribute.Bool("found", false))
			// Return empty streak for new users
			return &entities.InvestmentStreak{
				UserID:        userID,
				CurrentStreak: 0,
				LongestStreak: 0,
				UpdatedAt:     time.Now(),
			}, nil
		}
		span.RecordError(err)
		return nil, fmt.Errorf("failed to get investment streak: %w", err)
	}

	span.SetAttributes(
		attribute.Bool("found", true),
		attribute.Int("current_streak", streak.CurrentStreak),
	)
	return streak, nil
}

// UpdateStreakOnInvestment updates the streak when a user makes an investment
func (r *InvestmentStreakRepository) UpdateStreakOnInvestment(ctx context.Context, userID uuid.UUID, investmentDate time.Time) error {
	ctx, span := r.tracer.Start(ctx, "repository.update_streak_on_investment", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
	))
	defer span.End()

	// Get current streak
	currentStreak, err := r.GetByUserID(ctx, userID)
	if err != nil {
		return err
	}

	// Calculate new streak
	newStreak := r.calculateNewStreak(currentStreak, investmentDate)

	// Update
	if err := r.Upsert(ctx, newStreak); err != nil {
		return err
	}

	r.logger.Info("Investment streak updated",
		zap.String("user_id", userID.String()),
		zap.Int("old_streak", currentStreak.CurrentStreak),
		zap.Int("new_streak", newStreak.CurrentStreak),
	)

	return nil
}

// calculateNewStreak calculates the new streak based on investment date
func (r *InvestmentStreakRepository) calculateNewStreak(current *entities.InvestmentStreak, investmentDate time.Time) *entities.InvestmentStreak {
	newStreak := &entities.InvestmentStreak{
		UserID:             current.UserID,
		CurrentStreak:      current.CurrentStreak,
		LongestStreak:      current.LongestStreak,
		LastInvestmentDate: &investmentDate,
		UpdatedAt:          time.Now(),
	}

	// If no previous investment, start streak at 1
	if current.LastInvestmentDate == nil {
		newStreak.CurrentStreak = 1
		newStreak.LongestStreak = 1
		return newStreak
	}

	// Calculate days between investments
	daysSince := int(investmentDate.Sub(*current.LastInvestmentDate).Hours() / 24)

	// If invested today or yesterday, continue streak
	if daysSince <= 1 {
		// Only increment if it's a different day
		if daysSince == 1 {
			newStreak.CurrentStreak = current.CurrentStreak + 1
		} else {
			newStreak.CurrentStreak = current.CurrentStreak
		}
	} else {
		// Streak broken, reset to 1
		newStreak.CurrentStreak = 1
	}

	// Update longest streak if current is higher
	if newStreak.CurrentStreak > current.LongestStreak {
		newStreak.LongestStreak = newStreak.CurrentStreak
	} else {
		newStreak.LongestStreak = current.LongestStreak
	}

	return newStreak
}

// GetTopStreaks retrieves users with the highest current streaks
func (r *InvestmentStreakRepository) GetTopStreaks(ctx context.Context, limit int) ([]*entities.InvestmentStreak, error) {
	ctx, span := r.tracer.Start(ctx, "repository.get_top_streaks", trace.WithAttributes(
		attribute.Int("limit", limit),
	))
	defer span.End()

	query := `
		SELECT user_id, current_streak, longest_streak, last_investment_date, updated_at
		FROM investment_streaks
		WHERE current_streak > 0
		ORDER BY current_streak DESC, longest_streak DESC
		LIMIT $1
	`

	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		span.RecordError(err)
		r.logger.Error("Failed to query top streaks", zap.Error(err))
		return nil, fmt.Errorf("failed to query top streaks: %w", err)
	}
	defer rows.Close()

	streaks := []*entities.InvestmentStreak{}
	for rows.Next() {
		streak, err := r.scanStreak(rows)
		if err != nil {
			span.RecordError(err)
			return nil, fmt.Errorf("failed to scan streak: %w", err)
		}
		streaks = append(streaks, streak)
	}

	if err := rows.Err(); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	span.SetAttributes(attribute.Int("count", len(streaks)))
	return streaks, nil
}

// ResetInactiveStreaks resets streaks for users who haven't invested in over a day
func (r *InvestmentStreakRepository) ResetInactiveStreaks(ctx context.Context, cutoffDate time.Time) (int64, error) {
	ctx, span := r.tracer.Start(ctx, "repository.reset_inactive_streaks")
	defer span.End()

	query := `
		UPDATE investment_streaks
		SET current_streak = 0, updated_at = $1
		WHERE last_investment_date < $2 AND current_streak > 0
	`

	result, err := r.db.ExecContext(ctx, query, time.Now(), cutoffDate)
	if err != nil {
		span.RecordError(err)
		return 0, fmt.Errorf("failed to reset inactive streaks: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	span.SetAttributes(attribute.Int64("reset_count", affected))
	r.logger.Info("Reset inactive streaks",
		zap.Int64("count", affected),
		zap.Time("cutoff_date", cutoffDate),
	)

	return affected, nil
}

// GetStreakStats retrieves aggregate statistics about investment streaks
func (r *InvestmentStreakRepository) GetStreakStats(ctx context.Context) (map[string]interface{}, error) {
	ctx, span := r.tracer.Start(ctx, "repository.get_streak_stats")
	defer span.End()

	query := `
		SELECT 
			COUNT(*) as total_users,
			COUNT(CASE WHEN current_streak > 0 THEN 1 END) as active_streaks,
			COALESCE(AVG(current_streak), 0) as avg_current_streak,
			COALESCE(MAX(current_streak), 0) as max_current_streak,
			COALESCE(AVG(longest_streak), 0) as avg_longest_streak,
			COALESCE(MAX(longest_streak), 0) as max_longest_streak
		FROM investment_streaks
	`

	var stats struct {
		TotalUsers        int
		ActiveStreaks     int
		AvgCurrentStreak  float64
		MaxCurrentStreak  int
		AvgLongestStreak  float64
		MaxLongestStreak  int
	}

	err := r.db.QueryRowContext(ctx, query).Scan(
		&stats.TotalUsers,
		&stats.ActiveStreaks,
		&stats.AvgCurrentStreak,
		&stats.MaxCurrentStreak,
		&stats.AvgLongestStreak,
		&stats.MaxLongestStreak,
	)

	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to get streak stats: %w", err)
	}

	result := map[string]interface{}{
		"total_users":         stats.TotalUsers,
		"active_streaks":      stats.ActiveStreaks,
		"avg_current_streak":  stats.AvgCurrentStreak,
		"max_current_streak":  stats.MaxCurrentStreak,
		"avg_longest_streak":  stats.AvgLongestStreak,
		"max_longest_streak":  stats.MaxLongestStreak,
	}

	return result, nil
}

// scanStreak scans a row into an InvestmentStreak entity
func (r *InvestmentStreakRepository) scanStreak(scanner interface {
	Scan(dest ...interface{}) error
}) (*entities.InvestmentStreak, error) {
	var streak entities.InvestmentStreak
	var lastInvestmentDate sql.NullTime

	err := scanner.Scan(
		&streak.UserID,
		&streak.CurrentStreak,
		&streak.LongestStreak,
		&lastInvestmentDate,
		&streak.UpdatedAt,
	)

	if err != nil {
		return nil, err
	}

	if lastInvestmentDate.Valid {
		streak.LastInvestmentDate = &lastInvestmentDate.Time
	}

	return &streak, nil
}
