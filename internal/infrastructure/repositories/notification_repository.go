package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Notification represents an in-app notification
type Notification struct {
	ID        uuid.UUID              `db:"id" json:"id"`
	UserID    uuid.UUID              `db:"user_id" json:"user_id"`
	Channel   string                 `db:"channel" json:"channel"`
	Type      string                 `db:"type" json:"type"`
	Priority  string                 `db:"priority" json:"priority"`
	Title     string                 `db:"title" json:"title"`
	Body      string                 `db:"body" json:"body"`
	Data      map[string]interface{} `db:"data" json:"data,omitempty"`
	ImageURL  *string                `db:"image_url" json:"image_url,omitempty"`
	ActionURL *string                `db:"action_url" json:"action_url,omitempty"`
	Read      bool                   `db:"read" json:"read"`
	ReadAt    *time.Time             `db:"read_at" json:"read_at,omitempty"`
	CreatedAt time.Time              `db:"created_at" json:"created_at"`
}

// NotificationRepository handles notification persistence
type NotificationRepository struct {
	db *sql.DB
}

// NewNotificationRepository creates a new notification repository
func NewNotificationRepository(db *sql.DB) *NotificationRepository {
	return &NotificationRepository{db: db}
}

// Create stores a new notification
func (r *NotificationRepository) Create(ctx context.Context, n *Notification) error {
	if n.ID == uuid.Nil {
		n.ID = uuid.New()
	}
	if n.Channel == "" {
		n.Channel = "push"
	}
	if n.Priority == "" {
		n.Priority = "medium"
	}
	n.CreatedAt = time.Now()

	dataJSON, _ := json.Marshal(n.Data)
	if n.Data == nil {
		dataJSON = []byte("{}")
	}

	query := `
		INSERT INTO notifications (id, user_id, channel, type, priority, title, body, data, image_url, action_url, read, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, false, $11)`

	_, err := r.db.ExecContext(ctx, query, n.ID, n.UserID, n.Channel, n.Type, n.Priority, n.Title, n.Body, dataJSON, n.ImageURL, n.ActionURL, n.CreatedAt)
	return err
}

// GetByUserID returns paginated notifications for a user
func (r *NotificationRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Notification, error) {
	if limit <= 0 {
		limit = 20
	}

	query := `
		SELECT id, user_id, type, title, body, data, image_url, action_url, read, read_at, created_at
		FROM notifications
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`

	rows, err := r.db.QueryContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notifications []*Notification
	for rows.Next() {
		n := &Notification{}
		var dataJSON []byte
		if err := rows.Scan(&n.ID, &n.UserID, &n.Type, &n.Title, &n.Body, &dataJSON, &n.ImageURL, &n.ActionURL, &n.Read, &n.ReadAt, &n.CreatedAt); err != nil {
			return nil, err
		}
		if len(dataJSON) > 0 {
			json.Unmarshal(dataJSON, &n.Data)
		}
		notifications = append(notifications, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}
	return notifications, nil
}

// GetUnreadCount returns the count of unread notifications
func (r *NotificationRepository) GetUnreadCount(ctx context.Context, userID uuid.UUID) (int, error) {
	query := `SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND read = false`
	var count int
	err := r.db.QueryRowContext(ctx, query, userID).Scan(&count)
	return count, err
}

// MarkAsRead marks a single notification as read. Returns true if a row was updated.
func (r *NotificationRepository) MarkAsRead(ctx context.Context, userID, notificationID uuid.UUID) (bool, error) {
	query := `UPDATE notifications SET read = true, read_at = NOW() WHERE id = $1 AND user_id = $2 AND read = false`
	result, err := r.db.ExecContext(ctx, query, notificationID, userID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

// MarkAllAsRead marks all notifications as read for a user
func (r *NotificationRepository) MarkAllAsRead(ctx context.Context, userID uuid.UUID) error {
	query := `UPDATE notifications SET read = true, read_at = NOW() WHERE user_id = $1 AND read = false`
	_, err := r.db.ExecContext(ctx, query, userID)
	return err
}

// DeleteOlderThan removes notifications older than the given duration.
// Intended to be called by a periodic cleanup job.
func (r *NotificationRepository) DeleteOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	cutoff := time.Now().Add(-age)
	query := `DELETE FROM notifications WHERE created_at < $1`
	result, err := r.db.ExecContext(ctx, query, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete old notifications: %w", err)
	}
	return result.RowsAffected()
}

// DeleteReadOlderThan removes read notifications older than the given duration.
func (r *NotificationRepository) DeleteReadOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	cutoff := time.Now().Add(-age)
	query := `DELETE FROM notifications WHERE read = true AND created_at < $1`
	result, err := r.db.ExecContext(ctx, query, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete old read notifications: %w", err)
	}
	return result.RowsAffected()
}
