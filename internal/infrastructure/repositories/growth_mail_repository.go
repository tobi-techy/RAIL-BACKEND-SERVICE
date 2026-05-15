package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type GrowthMailRepository struct {
	db *sql.DB
}

func NewGrowthMailRepository(db *sql.DB) *GrowthMailRepository {
	return &GrowthMailRepository{db: db}
}

func (r *GrowthMailRepository) ListCandidates(ctx context.Context, limit int) ([]entities.GrowthMailCandidate, error) {
	if limit <= 0 {
		limit = 500
	}

	query := `
		SELECT
			u.id,
			u.email,
			COALESCE(u.first_name, '') AS first_name,
			COALESCE(u.kyc_status, '') AS kyc_status,
			u.created_at,
			u.last_login_at,
			COUNT(d.id) FILTER (WHERE d.status = 'confirmed') AS deposit_count
		FROM users u
		JOIN user_preferences up ON up.user_id = u.id
		LEFT JOIN deposits d ON d.user_id = u.id
		WHERE u.is_active = true
		  AND u.anonymized_at IS NULL
		  AND u.email <> ''
		  AND u.email_verified = true
		  AND up.marketing_emails = true
		GROUP BY u.id, u.email, u.first_name, u.kyc_status, u.created_at, u.last_login_at
		ORDER BY u.created_at ASC
		LIMIT $1`

	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list growth mail candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]entities.GrowthMailCandidate, 0)
	for rows.Next() {
		var c entities.GrowthMailCandidate
		var lastLogin sql.NullTime
		if err := rows.Scan(&c.UserID, &c.Email, &c.FirstName, &c.KYCStatus, &c.CreatedAt, &lastLogin, &c.DepositCount); err != nil {
			return nil, fmt.Errorf("scan growth mail candidate: %w", err)
		}
		if lastLogin.Valid {
			c.LastLoginAt = &lastLogin.Time
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate growth mail candidates: %w", err)
	}
	return candidates, nil
}

func (r *GrowthMailRepository) HasSuccessfulSend(ctx context.Context, userID uuid.UUID, campaignKey string) (bool, error) {
	var exists bool
	query := `
		SELECT EXISTS (
			SELECT 1
			FROM growth_email_events
			WHERE user_id = $1
			  AND campaign_key = $2
			  AND status = $3
		)`
	if err := r.db.QueryRowContext(ctx, query, userID, campaignKey, entities.GrowthMailStatusSent).Scan(&exists); err != nil {
		return false, fmt.Errorf("check growth mail send: %w", err)
	}
	return exists, nil
}

func (r *GrowthMailRepository) RecordSend(ctx context.Context, event *entities.GrowthMailEvent) error {
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	now := time.Now().UTC()
	if event.CreatedAt.IsZero() {
		event.CreatedAt = now
	}
	if event.Status == entities.GrowthMailStatusSent && event.SentAt == nil {
		event.SentAt = &now
	}

	query := `
		INSERT INTO growth_email_events (
			id, user_id, campaign_key, campaign, subject, status, error, sent_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, $9)
		ON CONFLICT (user_id, campaign_key) DO UPDATE SET
			status = EXCLUDED.status,
			error = EXCLUDED.error,
			sent_at = COALESCE(EXCLUDED.sent_at, growth_email_events.sent_at)`
	_, err := r.db.ExecContext(
		ctx,
		query,
		event.ID,
		event.UserID,
		event.CampaignKey,
		string(event.Campaign),
		event.Subject,
		event.Status,
		event.Error,
		event.SentAt,
		event.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("record growth mail send: %w", err)
	}
	return nil
}
