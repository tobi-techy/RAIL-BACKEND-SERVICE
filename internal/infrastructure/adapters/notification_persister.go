package adapters

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

// NotificationPersisterAdapter adapts NotificationRepository to NotificationPersister interface
type NotificationPersisterAdapter struct {
	repo *repositories.NotificationRepository
}

// NewNotificationPersisterAdapter creates a new adapter
func NewNotificationPersisterAdapter(repo *repositories.NotificationRepository) *NotificationPersisterAdapter {
	return &NotificationPersisterAdapter{repo: repo}
}

// Create persists a notification to the database
func (a *NotificationPersisterAdapter) Create(ctx context.Context, userID uuid.UUID, notifType, title, body string, data map[string]interface{}) error {
	n := &repositories.Notification{
		UserID: userID,
		Type:   notifType,
		Title:  title,
		Body:   body,
		Data:   data,
	}
	return a.repo.Create(ctx, n)
}
