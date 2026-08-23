package repositories

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// UserGoalRepository is the domain-facing interface for free-standing savings
// goals surfaced by Miriam. Distinct from the existing `goals` table (which is
// bound to automation evaluation); the new persistence is the storage layer
// for the Baby Steps ladder and milestone notifications.
//
// The implementation lives in
// `internal/infrastructure/repositories/user_goal_repository.go`.
type UserGoalRepository interface {
	Create(ctx context.Context, goal *entities.UserGoal) error
	GetByID(ctx context.Context, userID, goalID uuid.UUID) (*entities.UserGoal, error)
	ListByUser(ctx context.Context, userID uuid.UUID, includeArchived bool) ([]entities.UserGoal, error)
	ListActiveByStep(ctx context.Context, userID uuid.UUID, step int) ([]entities.UserGoal, error)
	UpdateProgress(ctx context.Context, userID, goalID uuid.UUID, currentAmount decimal.Decimal) error
	Complete(ctx context.Context, userID, goalID uuid.UUID) error
	Archive(ctx context.Context, userID, goalID uuid.UUID) error
	AppendProgressEvent(ctx context.Context, event *entities.UserGoalProgressEvent) error
	HasAnyGoal(ctx context.Context, userID uuid.UUID) (bool, error)
	ListAllActiveUsers(ctx context.Context) ([]uuid.UUID, error)
}
