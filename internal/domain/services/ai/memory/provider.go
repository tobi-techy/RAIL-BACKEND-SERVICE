package memory

import (
	"context"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type MemoryProvider interface {
	GetControlLevel(ctx context.Context, userID uuid.UUID) (string, error)
	SetControlLevel(ctx context.Context, userID uuid.UUID, level string) error
	SetPersonalityMode(ctx context.Context, userID uuid.UUID, mode string) error
	GetActiveFacts(ctx context.Context, userID uuid.UUID) ([]*entities.MiriamUserFact, error)
	ForgetFact(ctx context.Context, userID, factID uuid.UUID) error
	ForgetCategory(ctx context.Context, userID uuid.UUID, category string) error
	BuildMemoryContextWithSummary(ctx context.Context, userID uuid.UUID, message string) string
	ExtractMoment(userID uuid.UUID, userMessage, assistantResponse string)
}
