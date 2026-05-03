package ai

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	spendingsvc "github.com/rail-service/rail_service/internal/domain/services/spending"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type operatingPlanObligationsFake struct {
	obligations []entities.FinancialObligation
}

func (f operatingPlanObligationsFake) ListActive(_ context.Context, _ uuid.UUID) ([]entities.FinancialObligation, error) {
	return f.obligations, nil
}

type operatingPlanSpendingFake struct {
	flow *entities.MoneyFlowSummary
}

func (f operatingPlanSpendingFake) GetSummary(_ context.Context, _ uuid.UUID, _, _ time.Time) (*spendingsvc.Summary, error) {
	return &spendingsvc.Summary{}, nil
}

func (f operatingPlanSpendingFake) GetTransactions(_ context.Context, _ uuid.UUID, _, _ time.Time, _ int) ([]entities.SpendingTransaction, error) {
	return nil, nil
}

func (f operatingPlanSpendingFake) GetMoneyFlow(_ context.Context, _ uuid.UUID, _, _ time.Time) (*entities.MoneyFlowSummary, error) {
	return f.flow, nil
}

func (f operatingPlanSpendingFake) GetDailyTrend(_ context.Context, _ uuid.UUID, _, _ time.Time) ([]entities.SpendingByPeriod, error) {
	return nil, nil
}

func TestMoneyOperatingPlanForNGFreelancer(t *testing.T) {
	userID := uuid.New()
	dueDay := 25
	o := &Orchestrator{
		aggregateStats: govAggregateStatsFake{spend: decimalRequire("1200"), stash: decimalRequire("500")},
		spending: operatingPlanSpendingFake{flow: &entities.MoneyFlowSummary{
			TotalDeposits:    decimalRequire("3000"),
			TotalWithdrawals: decimalRequire("500"),
			TotalCardSpend:   decimalRequire("400"),
			TotalP2P:         decimalRequire("100"),
		}},
		financialProfile: govFinancialProfileFake{profile: &entities.FinancialProfile{
			UserID:               userID,
			UserType:             "freelancer",
			ResidenceCountry:     "NG",
			TaxCountry:           "NG",
			PrimaryCurrency:      "USD",
			EarningCurrency:      "USD",
			SpendingCurrency:     "NGN",
			FamilySupportCountry: "NG",
			IncomeFrequency:      "irregular",
			MonthlyIncome:        decimalRequire("3000"),
		}},
		obligations: operatingPlanObligationsFake{obligations: []entities.FinancialObligation{
			{
				ID:       uuid.New(),
				UserID:   userID,
				Type:     entities.ObligationTypeFamilySupport,
				Name:     "Family support",
				Amount:   decimalRequire("300"),
				Currency: "USD",
				Cadence:  entities.ObligationCadenceMonthly,
				DueDay:   &dueDay,
				Priority: entities.ObligationPriorityHigh,
				Status:   entities.ObligationStatusActive,
			},
			{
				ID:       uuid.New(),
				UserID:   userID,
				Type:     entities.ObligationTypeInvoice,
				Name:     "Client receivable",
				Amount:   decimalRequire("1500"),
				Currency: "USD",
				Cadence:  entities.ObligationCadenceOneTime,
				Priority: entities.ObligationPriorityMedium,
				Status:   entities.ObligationStatusActive,
			},
		}},
		logger: zap.NewNop(),
	}

	result, err := o.executeMoneyOperatingPlan(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, true, result["has_profile"])

	plan := result["operating_plan"].(map[string]interface{})
	require.Equal(t, "450.00", plan["tax_reserve_target"])
	require.Equal(t, "300.00", plan["family_support_cap"])
	require.NotEmpty(t, result["fx_context"])
	require.NotEmpty(t, result["tax_playbook"])
	require.NotEmpty(t, result["persona_operating_model"])

	obligationData := result["obligations"].(map[string]interface{})
	require.NotEmpty(t, obligationData["invoice_aging"])

	flags := result["risk_flags"].([]map[string]interface{})
	require.Contains(t, riskCodes(flags), "ng_freelancer_fx_pressure")
	require.Contains(t, riskCodes(flags), "irregular_income_smoothing")

	actions := result["next_actions"].([]map[string]interface{})
	require.NotEmpty(t, actions)
	for _, action := range actions {
		params := action["params"].(map[string]interface{})
		require.Equal(t, true, params["requires_confirmation"])
		require.Equal(t, "approval_gated_pending_action", params["execution"])
	}
}

func TestStageOperatingPlanActionUsesPendingConfirmation(t *testing.T) {
	userID := uuid.New()
	convID := uuid.New()
	o := &Orchestrator{
		budgetProvider: govBudgetProviderFake{},
		pending:        newInMemoryPendingActions(),
		logger:         zap.NewNop(),
	}

	result, err := o.StageOperatingPlanAction(context.Background(), userID, convID, "set_budget", map[string]interface{}{
		"monthly_limit": float64(1200),
	})
	require.NoError(t, err)
	require.Equal(t, true, result["action_required"])
	pending := o.pending.Get(context.Background(), convID)
	require.NotNil(t, pending)
	require.Equal(t, ToolSetBudget, pending.Action)
}

func TestMoneyOperatingPlanCoversPersonaTypes(t *testing.T) {
	personas := []string{"individual", "freelancer", "founder", "family", "high_earner"}
	for _, persona := range personas {
		t.Run(persona, func(t *testing.T) {
			userID := uuid.New()
			o := &Orchestrator{
				aggregateStats: govAggregateStatsFake{spend: decimalRequire("2000"), stash: decimalRequire("1000")},
				spending: operatingPlanSpendingFake{flow: &entities.MoneyFlowSummary{
					TotalDeposits: decimalRequire("5000"),
				}},
				financialProfile: govFinancialProfileFake{profile: &entities.FinancialProfile{
					UserID:           userID,
					UserType:         persona,
					ResidenceCountry: "US",
					TaxCountry:       "US",
					EarningCurrency:  "USD",
					SpendingCurrency: "USD",
					MonthlyIncome:    decimalRequire("5000"),
				}},
				obligations: operatingPlanObligationsFake{},
				logger:      zap.NewNop(),
			}

			result, err := o.executeMoneyOperatingPlan(context.Background(), userID)
			require.NoError(t, err)
			profile := result["profile"].(map[string]interface{})
			require.Equal(t, persona, profile["user_type"])
			require.NotEmpty(t, result["next_actions"])
		})
	}
}

func riskCodes(flags []map[string]interface{}) []string {
	codes := make([]string, 0, len(flags))
	for _, flag := range flags {
		if code, ok := flag["code"].(string); ok {
			codes = append(codes, code)
		}
	}
	return codes
}
