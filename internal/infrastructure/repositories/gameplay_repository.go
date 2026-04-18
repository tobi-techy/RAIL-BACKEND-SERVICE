package repositories

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// GameplayRepository handles all gameplay-related database operations
type GameplayRepository struct {
	db *sqlx.DB
}

func NewGameplayRepository(db *sqlx.DB) *GameplayRepository {
	return &GameplayRepository{db: db}
}

// --- Streaks ---

func (r *GameplayRepository) UpsertStreak(ctx context.Context, s *entities.UserStreak) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_streaks (id, user_id, streak_type, current_count, longest_count, last_activity_at, started_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
		ON CONFLICT (user_id, streak_type) DO UPDATE SET
			current_count = $4, longest_count = GREATEST(user_streaks.longest_count, $5),
			last_activity_at = $6, started_at = COALESCE($7, user_streaks.started_at),
			broken_at = NULL, updated_at = NOW()`,
		s.ID, s.UserID, s.StreakType, s.CurrentCount, s.LongestCount, s.LastActivityAt, s.StartedAt)
	return err
}

func (r *GameplayRepository) GetStreaksByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.UserStreak, error) {
	var streaks []*entities.UserStreak
	err := r.db.SelectContext(ctx, &streaks, `SELECT * FROM user_streaks WHERE user_id = $1`, userID)
	return streaks, err
}

func (r *GameplayRepository) GetStreak(ctx context.Context, userID uuid.UUID, streakType entities.StreakType) (*entities.UserStreak, error) {
	var s entities.UserStreak
	err := r.db.GetContext(ctx, &s, `SELECT * FROM user_streaks WHERE user_id = $1 AND streak_type = $2`, userID, streakType)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &s, err
}

func (r *GameplayRepository) ResetStreak(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE user_streaks SET current_count = 0, broken_at = NOW(), started_at = NULL, updated_at = NOW() WHERE id = $1`, id)
	return err
}

func (r *GameplayRepository) GetExpiredStreaks(ctx context.Context) ([]*entities.UserStreak, error) {
	var streaks []*entities.UserStreak
	err := r.db.SelectContext(ctx, &streaks, `
		SELECT * FROM user_streaks WHERE current_count > 0 AND (
			(streak_type = 'deposit' AND last_activity_at < NOW() - INTERVAL '7 days') OR
			(streak_type = 'roundup' AND last_activity_at < NOW() - INTERVAL '7 days') OR
			(streak_type = 'stash_growth' AND last_activity_at < NOW() - INTERVAL '7 days')
		)`)
	return streaks, err
}

func (r *GameplayRepository) GetNearBreakingStreaks(ctx context.Context) ([]*entities.UserStreak, error) {
	var streaks []*entities.UserStreak
	err := r.db.SelectContext(ctx, &streaks, `
		SELECT * FROM user_streaks WHERE current_count > 0 AND (
			(streak_type = 'deposit' AND last_activity_at < NOW() - INTERVAL '6 days' AND last_activity_at >= NOW() - INTERVAL '7 days') OR
			(streak_type = 'roundup' AND last_activity_at < NOW() - INTERVAL '6 days' AND last_activity_at >= NOW() - INTERVAL '7 days')
		)`)
	return streaks, err
}

// --- XP ---

func (r *GameplayRepository) GetUserXP(ctx context.Context, userID uuid.UUID) (*entities.UserXP, error) {
	var xp entities.UserXP
	err := r.db.GetContext(ctx, &xp, `SELECT * FROM user_xp WHERE user_id = $1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &xp, err
}

func (r *GameplayRepository) AwardXP(ctx context.Context, userID uuid.UUID, amount int, newLevel int) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_xp (id, user_id, total_xp, current_level, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, $3, NOW(), NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			total_xp = user_xp.total_xp + $2, current_level = $3, updated_at = NOW()`,
		userID, amount, newLevel)
	return err
}

func (r *GameplayRepository) CreateXPEvent(ctx context.Context, e *entities.XPEvent) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO xp_events (id, user_id, event_type, xp_amount, source_id, description, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())`,
		e.ID, e.UserID, e.EventType, e.XPAmount, e.SourceID, e.Description)
	return err
}

func (r *GameplayRepository) GetXPHistory(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.XPEvent, error) {
	var events []*entities.XPEvent
	err := r.db.SelectContext(ctx, &events, `
		SELECT * FROM xp_events WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		userID, limit, offset)
	return events, err
}

// --- Challenges ---

func (r *GameplayRepository) GetChallengesByType(ctx context.Context, challengeType entities.ChallengeType) ([]*entities.Challenge, error) {
	var challenges []*entities.Challenge
	err := r.db.SelectContext(ctx, &challenges, `
		SELECT * FROM challenges WHERE challenge_type = $1 AND is_active = true`, challengeType)
	return challenges, err
}

func (r *GameplayRepository) GetUserChallenges(ctx context.Context, userID uuid.UUID, status entities.ChallengeStatus) ([]*entities.UserChallenge, error) {
	var ucs []*entities.UserChallenge
	err := r.db.SelectContext(ctx, &ucs, `
		SELECT uc.* FROM user_challenges uc WHERE uc.user_id = $1 AND uc.status = $2 ORDER BY uc.started_at DESC`,
		userID, status)
	if err != nil {
		return nil, err
	}
	// Load challenge details
	for _, uc := range ucs {
		var c entities.Challenge
		if err := r.db.GetContext(ctx, &c, `SELECT * FROM challenges WHERE id = $1`, uc.ChallengeID); err == nil {
			uc.Challenge = &c
		}
	}
	return ucs, nil
}

func (r *GameplayRepository) AssignChallenge(ctx context.Context, userID, challengeID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_challenges (id, user_id, challenge_id, progress, status, started_at, created_at)
		VALUES (gen_random_uuid(), $1, $2, 0, 'active', NOW(), NOW())
		ON CONFLICT (user_id, challenge_id) DO NOTHING`, userID, challengeID)
	return err
}

func (r *GameplayRepository) UpdateChallengeProgress(ctx context.Context, id uuid.UUID, progress decimal.Decimal) error {
	_, err := r.db.ExecContext(ctx, `UPDATE user_challenges SET progress = $1 WHERE id = $2`, progress, id)
	return err
}

func (r *GameplayRepository) CompleteChallenge(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE user_challenges SET status = 'completed', completed_at = NOW() WHERE id = $1`, id)
	return err
}

func (r *GameplayRepository) ExpireChallenges(ctx context.Context, challengeType entities.ChallengeType) (int64, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE user_challenges SET status = 'expired'
		WHERE status = 'active' AND challenge_id IN (
			SELECT id FROM challenges WHERE challenge_type = $1
		)`, challengeType)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *GameplayRepository) GetUserChallengeByMetric(ctx context.Context, userID uuid.UUID, metric string) (*entities.UserChallenge, error) {
	var uc entities.UserChallenge
	err := r.db.GetContext(ctx, &uc, `
		SELECT uc.* FROM user_challenges uc
		JOIN challenges c ON c.id = uc.challenge_id
		WHERE uc.user_id = $1 AND uc.status = 'active' AND c.target_metric = $2
		LIMIT 1`, userID, metric)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c entities.Challenge
	if err := r.db.GetContext(ctx, &c, `SELECT * FROM challenges WHERE id = $1`, uc.ChallengeID); err == nil {
		uc.Challenge = &c
	}
	return &uc, nil
}

// --- Achievements ---

func (r *GameplayRepository) GetAllAchievements(ctx context.Context) ([]*entities.Achievement, error) {
	var achievements []*entities.Achievement
	err := r.db.SelectContext(ctx, &achievements, `SELECT * FROM achievements ORDER BY rarity, name`)
	return achievements, err
}

func (r *GameplayRepository) GetUserAchievements(ctx context.Context, userID uuid.UUID) ([]*entities.UserAchievement, error) {
	var uas []*entities.UserAchievement
	err := r.db.SelectContext(ctx, &uas, `SELECT * FROM user_achievements WHERE user_id = $1`, userID)
	return uas, err
}

func (r *GameplayRepository) HasAchievement(ctx context.Context, userID, achievementID uuid.UUID) (bool, error) {
	var count int
	err := r.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM user_achievements WHERE user_id = $1 AND achievement_id = $2`, userID, achievementID)
	return count > 0, err
}

func (r *GameplayRepository) UnlockAchievement(ctx context.Context, userID, achievementID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_achievements (id, user_id, achievement_id, unlocked_at)
		VALUES (gen_random_uuid(), $1, $2, NOW())
		ON CONFLICT (user_id, achievement_id) DO NOTHING`, userID, achievementID)
	return err
}

// --- Subscriptions ---

func (r *GameplayRepository) CreateSubscription(ctx context.Context, s *entities.Subscription) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO subscriptions (id, user_id, plan, status, started_at, current_period_start, current_period_end, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())`,
		s.ID, s.UserID, s.Plan, s.Status, s.StartedAt, s.CurrentPeriodStart, s.CurrentPeriodEnd)
	return err
}

func (r *GameplayRepository) GetSubscription(ctx context.Context, userID uuid.UUID) (*entities.Subscription, error) {
	var s entities.Subscription
	err := r.db.GetContext(ctx, &s, `SELECT * FROM subscriptions WHERE user_id = $1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &s, err
}

func (r *GameplayRepository) UpdateSubscription(ctx context.Context, s *entities.Subscription) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE subscriptions SET status = $1, current_period_start = $2, current_period_end = $3,
			cancelled_at = $4, updated_at = NOW() WHERE id = $5`,
		s.Status, s.CurrentPeriodStart, s.CurrentPeriodEnd, s.CancelledAt, s.ID)
	return err
}

func (r *GameplayRepository) CreateCharge(ctx context.Context, c *entities.SubscriptionCharge) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO subscription_charges (id, subscription_id, user_id, amount, ledger_transaction_id, status, period_start, period_end, charged_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())`,
		c.ID, c.SubscriptionID, c.UserID, c.Amount, c.LedgerTransactionID, c.Status, c.PeriodStart, c.PeriodEnd, c.ChargedAt)
	return err
}

func (r *GameplayRepository) GetDueSubscriptions(ctx context.Context) ([]*entities.Subscription, error) {
	var subs []*entities.Subscription
	err := r.db.SelectContext(ctx, &subs, `
		SELECT * FROM subscriptions WHERE status IN ('active', 'past_due') AND current_period_end <= NOW()`)
	return subs, err
}

func (r *GameplayRepository) CountFailedCharges(ctx context.Context, subscriptionID uuid.UUID, periodStart time.Time) (int, error) {
	var count int
	err := r.db.GetContext(ctx, &count, `
		SELECT COUNT(*) FROM subscription_charges
		WHERE subscription_id = $1 AND period_start = $2 AND status IN ('failed', 'insufficient_funds')`,
		subscriptionID, periodStart)
	return count, err
}

// --- Active Users (for workers) ---

func (r *GameplayRepository) GetActiveUserIDs(ctx context.Context) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := r.db.SelectContext(ctx, &ids, `SELECT id FROM users WHERE is_active = true`)
	return ids, err
}

// --- Activity heatmap ---

func (r *GameplayRepository) GetDepositDates(ctx context.Context, userID uuid.UUID, since time.Time) ([]time.Time, error) {
	var dates []time.Time
	err := r.db.SelectContext(ctx, &dates, `
		SELECT DISTINCT DATE(created_at) FROM deposits
		WHERE user_id = $1 AND status = 'completed' AND created_at >= $2
		ORDER BY DATE(created_at)`, userID, since)
	return dates, err
}

// --- Card transaction counting (for no-spend streak) ---

func (r *GameplayRepository) CountCardTransactionsForDate(ctx context.Context, userID uuid.UUID, date time.Time) (int, error) {
	var count int
	err := r.db.GetContext(ctx, &count, `
		SELECT COUNT(*) FROM card_transactions
		WHERE user_id = $1 AND status = 'completed'
		AND created_at >= $2 AND created_at < $3`,
		userID, date.Truncate(24*time.Hour), date.Truncate(24*time.Hour).Add(24*time.Hour))
	return count, err
}
