package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Notification represents an in-app notification
type Notification struct {
	ID        uuid.UUID              `db:"id" json:"id"`
	UserID    uuid.UUID              `db:"user_id" json:"user_id"`
	Type      string                 `db:"type" json:"type"`
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
	n.CreatedAt = time.Now()

	dataJSON, _ := json.Marshal(n.Data)
	if n.Data == nil {
		dataJSON = []byte("{}")
	}

	query := `
		INSERT INTO notifications (id, user_id, type, title, body, data, image_url, action_url, read, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, false, $9)`

	_, err := r.db.ExecContext(ctx, query, n.ID, n.UserID, n.Type, n.Title, n.Body, dataJSON, n.ImageURL, n.ActionURL, n.CreatedAt)
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
	return notifications, nil
}

// GetUnreadCount returns the count of unread notifications
func (r *NotificationRepository) GetUnreadCount(ctx context.Context, userID uuid.UUID) (int, error) {
	query := `SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND read = false`
	var count int
	err := r.db.QueryRowContext(ctx, query, userID).Scan(&count)
	return count, err
}

// MarkAsRead marks a single notification as read
func (r *NotificationRepository) MarkAsRead(ctx context.Context, userID, notificationID uuid.UUID) error {
	query := `UPDATE notifications SET read = true, read_at = NOW() WHERE id = $1 AND user_id = $2`
	_, err := r.db.ExecContext(ctx, query, notificationID, userID)
	return err
}

// MarkAllAsRead marks all notifications as read for a user
func (r *NotificationRepository) MarkAllAsRead(ctx context.Context, userID uuid.UUID) error {
	query := `UPDATE notifications SET read = true, read_at = NOW() WHERE user_id = $1 AND read = false`
	_, err := r.db.ExecContext(ctx, query, userID)
	return err
}
