package repositories

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

var ErrPlanNotFound = errors.New("conscious spending plan not found")

type ConsciousSpendingPlanRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.ConsciousSpendingPlan, error)
	GetActiveVersion(ctx context.Context, userID uuid.UUID) (*entities.ConsciousSpendingPlan, error)
	Commit(ctx context.Context, userID uuid.UUID, in PlanHeaderInput) (*entities.ConsciousSpendingPlan, error)
	Supersede(ctx context.Context, userID uuid.UUID, version int) (*entities.ConsciousSpendingPlan, error)
	Pause(ctx context.Context, userID uuid.UUID, version int) (*entities.ConsciousSpendingPlan, error)
	SaveItems(ctx context.Context, planID uuid.UUID, items []entities.ConsciousSpendingPlanItem) error
	SaveNetWorth(ctx context.Context, snapshot *entities.ConsciousSpendingNetWorth) error
	ListCommittedCheckIns(ctx context.Context) ([]entities.ConsciousSpendingPlanCheckIn, error)
}

type PlanHeaderInput struct {
	Scope                   string
	Country                 string
	BaseCurrency            string
	GrossMonthlyIncome      decimal.Decimal
	PayrollDeductions       decimal.Decimal
	PreTaxInvestments       decimal.Decimal
	TakeHomeIncome          decimal.Decimal
	IncomeCadence           string
	IncomeBasis             string
	IncomeSource            string
	IncomeConfidence        string
	FixedCostsSubtotal      decimal.Decimal
	MiscBufferRate          decimal.Decimal
	MiscBufferAmount        decimal.Decimal
	FixedCosts              decimal.Decimal
	PostTaxInvestments      decimal.Decimal
	Savings                 decimal.Decimal
	GuiltFreeSpending       decimal.Decimal
	FixedCostsPct           decimal.Decimal
	InvestmentsPct          decimal.Decimal
	SavingsPct              decimal.Decimal
	GuiltFreeSpendingPct    decimal.Decimal
	Status                  string
	CheckInCadence          string
	CommittedAt             *time.Time
	SupersededAt            *time.Time
	Items                   []entities.ConsciousSpendingPlanItem
	NetWorth                *entities.ConsciousSpendingNetWorth
}
