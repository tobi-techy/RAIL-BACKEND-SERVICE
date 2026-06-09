package di

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/rail-service/rail_service/internal/domain/services/sharedgoal"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
)

// goalTransferAdapter adapts the ledger service to the automation GoalTransferExecutor interface.
type goalTransferAdapter struct {
	ledger *ledger.Service
}

func (a *goalTransferAdapter) TransferSpendToGoal(ctx context.Context, userID, goalID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error {
	return a.ledger.TransferSpendToGoal(ctx, userID, goalID, amount, idempotencyKey)
}

func (a *goalTransferAdapter) GetGoalBalance(ctx context.Context, userID, goalID uuid.UUID) (decimal.Decimal, error) {
	return a.ledger.GetGoalBalance(ctx, userID, goalID)
}

func (a *goalTransferAdapter) GetTotalGoalAllocated(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	return a.ledger.GetTotalGoalAllocated(ctx, userID)
}

// goalCheckerAdapter adapts the shared goal repo + ledger to the automation GoalChecker interface.
// A goal is considered "reached" when its goal_balance ledger account >= target amount.
type goalCheckerAdapter struct {
	goalRepo *repositories.SharedGoalRepository
	ledger   *ledger.Service
}

func (a *goalCheckerAdapter) IsGoalReached(ctx context.Context, userID uuid.UUID, goalID uuid.UUID) (bool, error) {
	goal, err := a.goalRepo.GetByID(ctx, goalID)
	if err != nil {
		return false, fmt.Errorf("get goal: %w", err)
	}
	balance, err := a.ledger.GetGoalBalance(ctx, userID, goalID)
	if err != nil {
		return false, fmt.Errorf("get goal balance: %w", err)
	}
	return balance.GreaterThanOrEqual(goal.TargetAmount), nil
}

func (a *goalCheckerAdapter) GetGoalTarget(ctx context.Context, goalID uuid.UUID) (decimal.Decimal, error) {
	goal, err := a.goalRepo.GetByID(ctx, goalID)
	if err != nil {
		return decimal.Zero, err
	}
	return goal.TargetAmount, nil
}

// goalContributorAdapter adapts the shared goal repo to the automation GoalContributor interface.
type goalContributorAdapter struct {
	goalRepo *repositories.SharedGoalRepository
}

func (a *goalContributorAdapter) RecordAutomationContribution(ctx context.Context, userID, goalID uuid.UUID, amount decimal.Decimal) error {
	contrib := &entities.SharedGoalContribution{
		ID:     uuid.New(),
		GoalID: goalID,
		UserID: userID,
		Amount: amount,
		Source: entities.ContribAutomation,
	}
	return a.goalRepo.Contribute(ctx, contrib)
}

// sharedGoalCreatorAdapter adapts SharedGoal service to the AI orchestrator's SharedGoalCreator interface.
type sharedGoalCreatorAdapter struct {
	svc *sharedgoal.Service
}

func (a *sharedGoalCreatorAdapter) CreateGoalFromAI(ctx context.Context, userID uuid.UUID, name, targetAmount string, deadline *string) (uuid.UUID, error) {
	goal, err := a.svc.Create(ctx, userID, &sharedgoal.CreateGoalRequest{
		Name:         name,
		TargetAmount: targetAmount,
		Deadline:     deadline,
		Visibility:   "private",
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("create shared goal from AI: %w", err)
	}
	return goal.ID, nil
}

// stationObligationAdapter adapts the obligation service to the station's ObligationProvider.
type stationObligationAdapter struct {
	obligations interface {
		ListActive(ctx context.Context, userID uuid.UUID) ([]entities.FinancialObligation, error)
	}
}

func (a *stationObligationAdapter) GetUpcomingTotal(ctx context.Context, userID uuid.UUID, withinDays int) (decimal.Decimal, error) {
	obs, err := a.obligations.ListActive(ctx, userID)
	if err != nil {
		return decimal.Zero, err
	}
	now := time.Now().UTC()
	var total decimal.Decimal
	for _, ob := range obs {
		if ob.DueDay == nil {
			continue
		}
		dueThisMonth := time.Date(now.Year(), now.Month(), *ob.DueDay, 0, 0, 0, 0, time.UTC)
		if dueThisMonth.Before(now) {
			dueThisMonth = dueThisMonth.AddDate(0, 1, 0)
		}
		if int(dueThisMonth.Sub(now).Hours()/24) <= withinDays {
			total = total.Add(ob.Amount)
		}
	}
	return total, nil
}
