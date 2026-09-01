package actions

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

type StepUpVerifier interface {
	VerifyStepUp(ctx context.Context, userID uuid.UUID, token string) (bool, error)
}

type FundsTransferer interface {
	TransferSpendToStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error
	TransferStashToSpend(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error
	GetSpendBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
	GetStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
}

type EmergencyWithdrawer interface {
	IsStashLocked(ctx context.Context, userID uuid.UUID) (bool, error)
	EmergencyWithdrawalPreview(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (*entities.EmergencyWithdrawalPreviewResponse, error)
	EmergencyStashToSpending(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) (*entities.EmergencyWithdrawalResult, error)
}

type GoalProtectionProvider interface {
	GetTotalGoalAllocated(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
	GetGoalAccounts(ctx context.Context, userID uuid.UUID) ([]*entities.LedgerAccount, error)
	GetUnallocatedStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
	GetWithdrawableStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
}

type UserAccountChecker interface {
	IsActiveAndUnfrozen(ctx context.Context, userID uuid.UUID) (active bool, frozen bool, err error)
}

type ActionAuditor interface {
	RecordAction(ctx context.Context, entry *entities.ActionAuditEntry) error
}

type SavingsGoalStore interface {
	Set(ctx context.Context, userID uuid.UUID, goal *SavingsGoal) error
	Get(ctx context.Context, userID uuid.UUID) (*SavingsGoal, error)
}

type SharedGoalCreator interface {
	CreateGoalFromAI(ctx context.Context, userID uuid.UUID, name, targetAmount string, deadline *string) (uuid.UUID, error)
}

type AutomationCreator interface {
	CreateAutomationFromAI(ctx context.Context, userID uuid.UUID, req AIServiceAutomationRequest) (*entities.MiriamAutomation, error)
}

type SavingsGoal struct {
	Name      string `json:"name"`
	Target    string `json:"target"`
	Deadline  string `json:"deadline,omitempty"`
	CreatedAt string `json:"created_at"`
}

type AIServiceAutomationRequest struct {
	Name              string
	Description       *string
	TriggerType       string
	TriggerConfig     map[string]interface{}
	ActionType        string
	ActionConfig      map[string]interface{}
	MaxTriggersPerDay int
	CooldownMinutes   int
}
