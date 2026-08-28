package obligations

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

type FinancialObligationManager interface {
	List(ctx context.Context, userID uuid.UUID, status, obligationType string) ([]entities.FinancialObligation, error)
	MarkPaid(ctx context.Context, userID, id uuid.UUID) (*entities.FinancialObligation, error)
	MarkCancelled(ctx context.Context, userID, id uuid.UUID) (*entities.FinancialObligation, error)
}

type FinancialObligationProvider interface {
	ListActive(ctx context.Context, userID uuid.UUID) ([]entities.FinancialObligation, error)
}

type ObligationCreator interface {
	CreateObligationFromAI(ctx context.Context, userID uuid.UUID, req AIServiceObligationRequest) (*entities.FinancialObligation, error)
}

type AIServiceObligationRequest struct {
	Type         string
	Name         string
	Amount       decimal.Decimal
	Currency     string
	Cadence      string
	DueDay       *int
	Priority     string
	Counterparty *string
	Status       string
	Metadata     map[string]interface{}
}
