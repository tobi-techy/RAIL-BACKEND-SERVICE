package di

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/automation"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

// --- IncomeFlowReader adapter ---

// automationIncomeFlowAdapter adapts LedgerSpendingRepository to automation.IncomeFlowReader.
type automationIncomeFlowAdapter struct {
	repo *repositories.LedgerSpendingRepository
}

func (a *automationIncomeFlowAdapter) GetMoneyFlow(ctx context.Context, userID uuid.UUID, start, end time.Time) (*entities.MoneyFlowSummary, error) {
	return a.repo.GetMoneyFlow(ctx, userID, start, end)
}

// --- AnomalyRunner adapter ---

// automationAnomalyAdapter wraps the AI AnomalyEngine to satisfy automation.AnomalyRunner.
type automationAnomalyAdapter struct {
	engine *aiservice.AnomalyEngine
}

func (a *automationAnomalyAdapter) RunAllChecks(ctx context.Context, userID uuid.UUID, now time.Time) []automation.AnomalyResult {
	raw := a.engine.RunAllChecks(ctx, userID, now)
	out := make([]automation.AnomalyResult, len(raw))
	for i, r := range raw {
		out[i] = automation.AnomalyResult{
			Type:       string(r.Type),
			Severity:   string(r.Severity),
			Details:    r.Details,
			DetectedAt: r.DetectedAt,
		}
	}
	return out
}

// --- PaydaySignalReader adapter ---

// automationPaydaySignalAdapter adapts ContextSignalRepository to automation.PaydaySignalReader.
type automationPaydaySignalAdapter struct {
	repo *repositories.ContextSignalRepository
}

func (a *automationPaydaySignalAdapter) GetByType(ctx context.Context, userID uuid.UUID, signalType string) ([]entities.UserContextSignal, error) {
	return a.repo.GetByType(ctx, userID, signalType)
}

// --- LifeEventDetector adapter ---

// automationLifeEventAdapter combines spending flow and signal data for life event detection.
type automationLifeEventAdapter struct {
	spendingRepo *repositories.LedgerSpendingRepository
	signalsRepo  *repositories.ContextSignalRepository
}

func (a *automationLifeEventAdapter) GetMoneyFlow(ctx context.Context, userID uuid.UUID, start, end time.Time) (*entities.MoneyFlowSummary, error) {
	return a.spendingRepo.GetMoneyFlow(ctx, userID, start, end)
}

func (a *automationLifeEventAdapter) GetByType(ctx context.Context, userID uuid.UUID, signalType string) ([]entities.UserContextSignal, error) {
	return a.signalsRepo.GetByType(ctx, userID, signalType)
}

// wireAutomationTriggers sets the trigger evaluation dependencies on the automation service.
// Call this after the AnomalyEngine is available (created in application.go).
func wireAutomationTriggers(svc *automation.Service, spendingRepo *repositories.LedgerSpendingRepository, signalsRepo *repositories.ContextSignalRepository, anomalyEngine *aiservice.AnomalyEngine) {
	if spendingRepo != nil {
		svc.SetIncomeFlowReader(&automationIncomeFlowAdapter{repo: spendingRepo})
		svc.SetLifeEventDetector(&automationLifeEventAdapter{spendingRepo: spendingRepo, signalsRepo: signalsRepo})
	}
	if signalsRepo != nil {
		svc.SetPaydaySignalReader(&automationPaydaySignalAdapter{repo: signalsRepo})
	}
	if anomalyEngine != nil {
		svc.SetAnomalyRunner(&automationAnomalyAdapter{engine: anomalyEngine})
	}
}
