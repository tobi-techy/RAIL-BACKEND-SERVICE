package di

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	"github.com/rail-service/rail_service/internal/domain/services/automation"
	obligationservice "github.com/rail-service/rail_service/internal/domain/services/obligation"
)

type automationCreatorAdapter struct {
	service *automation.Service
}

func (a *automationCreatorAdapter) CreateAutomationFromAI(ctx context.Context, userID uuid.UUID, req aiservice.AIServiceAutomationRequest) (*entities.MiriamAutomation, error) {
	return a.service.Create(ctx, userID, &automation.CreateAutomationRequest{
		Name:              req.Name,
		Description:       req.Description,
		TriggerType:       req.TriggerType,
		TriggerConfig:     req.TriggerConfig,
		ActionType:        req.ActionType,
		ActionConfig:      req.ActionConfig,
		MaxTriggersPerDay: req.MaxTriggersPerDay,
		CooldownMinutes:   req.CooldownMinutes,
	})
}

type obligationCreatorAdapter struct {
	service *obligationservice.Service
}

func (a *obligationCreatorAdapter) CreateObligationFromAI(ctx context.Context, userID uuid.UUID, req aiservice.AIServiceObligationRequest) (*entities.FinancialObligation, error) {
	return a.service.Create(ctx, userID, obligationservice.CreateRequest{
		Type:         req.Type,
		Name:         req.Name,
		Amount:       req.Amount,
		Currency:     req.Currency,
		Cadence:      req.Cadence,
		DueDay:       req.DueDay,
		Priority:     req.Priority,
		Counterparty: req.Counterparty,
		Metadata:     req.Metadata,
	})
}

type obligationManagerAdapter struct {
	service *obligationservice.Service
}

func (a *obligationManagerAdapter) List(ctx context.Context, userID uuid.UUID, status, obligationType string) ([]entities.FinancialObligation, error) {
	return a.service.List(ctx, userID, obligationservice.ListFilter{Status: status, Type: obligationType})
}

func (a *obligationManagerAdapter) MarkPaid(ctx context.Context, userID, id uuid.UUID) (*entities.FinancialObligation, error) {
	paid := entities.ObligationStatusPaid
	return a.service.Update(ctx, userID, id, obligationservice.UpdateRequest{Status: &paid})
}

func (a *obligationManagerAdapter) MarkCancelled(ctx context.Context, userID, id uuid.UUID) (*entities.FinancialObligation, error) {
	cancelled := entities.ObligationStatusCancelled
	return a.service.Update(ctx, userID, id, obligationservice.UpdateRequest{Status: &cancelled})
}
