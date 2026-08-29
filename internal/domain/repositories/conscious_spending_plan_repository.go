package repositories

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type ConsciousSpendingPlanRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.ConsciousSpendingPlan, error)
	Upsert(ctx context.Context, plan *entities.ConsciousSpendingPlan) error
	ListCommittedCheckIns(ctx context.Context) ([]entities.ConsciousSpendingPlanCheckIn, error)
}
