package obligation

import (
	"testing"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestValidateFinancialObligationAcceptsSupportedValues(t *testing.T) {
	dueDay := 15
	obligation := &entities.FinancialObligation{
		Type:     entities.ObligationTypeFamilySupport,
		Name:     "Mum support",
		Amount:   decimal.RequireFromString("75000"),
		Currency: "NGN",
		Cadence:  entities.ObligationCadenceMonthly,
		DueDay:   &dueDay,
		Priority: entities.ObligationPriorityHigh,
		Status:   entities.ObligationStatusActive,
	}

	require.NoError(t, validate(obligation))
}

func TestValidateFinancialObligationRejectsBadInputs(t *testing.T) {
	tests := []struct {
		name       string
		obligation entities.FinancialObligation
		want       string
	}{
		{
			name: "unsupported type",
			obligation: entities.FinancialObligation{
				Type: "loan_shark", Name: "Loan", Amount: decimal.NewFromInt(1), Currency: "USD", Cadence: entities.ObligationCadenceMonthly, Priority: entities.ObligationPriorityMedium, Status: entities.ObligationStatusActive,
			},
			want: "unsupported obligation type",
		},
		{
			name: "bad amount",
			obligation: entities.FinancialObligation{
				Type: entities.ObligationTypeDebt, Name: "Loan", Amount: decimal.Zero, Currency: "USD", Cadence: entities.ObligationCadenceMonthly, Priority: entities.ObligationPriorityMedium, Status: entities.ObligationStatusActive,
			},
			want: "amount",
		},
		{
			name: "bad cadence",
			obligation: entities.FinancialObligation{
				Type: entities.ObligationTypeDebt, Name: "Loan", Amount: decimal.NewFromInt(1), Currency: "USD", Cadence: "daily", Priority: entities.ObligationPriorityMedium, Status: entities.ObligationStatusActive,
			},
			want: "unsupported obligation cadence",
		},
		{
			name: "bad due day",
			obligation: func() entities.FinancialObligation {
				dueDay := 35
				return entities.FinancialObligation{
					Type: entities.ObligationTypeDebt, Name: "Loan", Amount: decimal.NewFromInt(1), Currency: "USD", Cadence: entities.ObligationCadenceMonthly, DueDay: &dueDay, Priority: entities.ObligationPriorityMedium, Status: entities.ObligationStatusActive,
				}
			}(),
			want: "due_day",
		},
		{
			name: "bad status",
			obligation: entities.FinancialObligation{
				Type: entities.ObligationTypeDebt, Name: "Loan", Amount: decimal.NewFromInt(1), Currency: "USD", Cadence: entities.ObligationCadenceMonthly, Priority: entities.ObligationPriorityMedium, Status: "forgotten",
			},
			want: "unsupported obligation status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(&tt.obligation)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.want)
		})
	}
}
