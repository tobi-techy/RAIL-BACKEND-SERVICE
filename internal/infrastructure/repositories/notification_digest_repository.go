package repositories

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	miriamsvc "github.com/rail-service/rail_service/internal/domain/services/miriam"
)

type NotificationDigestRepository struct {
	db *sqlx.DB
}

func NewNotificationDigestRepository(db *sqlx.DB) *NotificationDigestRepository {
	return &NotificationDigestRepository{db: db}
}

func (r *NotificationDigestRepository) SaveDigest(ctx context.Context, d *miriamsvc.NotificationDigest) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("marshal notification digest: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO miriam_notification_digests (id, user_id, generated_at, period_start, period_end, summary, data)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		uuid.New(), d.UserID, d.GeneratedAt, d.PeriodStart, d.PeriodEnd, d.Summary, raw)
	if err != nil {
		return fmt.Errorf("save notification digest: %w", err)
	}
	return nil
}

func (r *NotificationDigestRepository) GetRecentDigests(ctx context.Context, userID uuid.UUID, limit int) ([]miriamsvc.NotificationDigest, error) {
	if limit <= 0 {
		limit = 10
	} else if limit > 50 {
		limit = 50
	}
	var rawData []json.RawMessage
	err := r.db.SelectContext(ctx, &rawData, `
		SELECT data FROM miriam_notification_digests
		WHERE user_id = $1
		ORDER BY generated_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("get recent notification digests: %w", err)
	}

	digests := make([]miriamsvc.NotificationDigest, 0, len(rawData))
	for _, raw := range rawData {
		var d miriamsvc.NotificationDigest
		if err := json.Unmarshal(raw, &d); err != nil {
			continue
		}
		digests = append(digests, d)
	}
	return digests, nil
}
