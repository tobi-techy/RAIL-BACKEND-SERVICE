package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type AuditLogFilter struct {
	UserID     *uuid.UUID
	Action     *entities.AuditAction
	Resource   *string
	ResourceID *uuid.UUID
	StartDate  *time.Time
	EndDate    *time.Time
	Limit      int
	Offset     int
}

type AuditRepository interface {
	Create(ctx context.Context, log *entities.AuditLog) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.AuditLog, error)
	List(ctx context.Context, filter AuditLogFilter) ([]*entities.AuditLog, error)
	Count(ctx context.Context, filter AuditLogFilter) (int64, error)
}
