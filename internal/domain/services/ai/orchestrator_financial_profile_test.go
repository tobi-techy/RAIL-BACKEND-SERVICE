package ai

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type fakeKnowledgeSearcher struct {
	results []entities.KnowledgeSearchResult
}

func (f fakeKnowledgeSearcher) Search(_ context.Context, _ string, _ int) ([]entities.KnowledgeSearchResult, error) {
	return f.results, nil
}

func TestExecuteKnowledgeSearchIncludesSourceDocs(t *testing.T) {
	o := &Orchestrator{
		knowledge: fakeKnowledgeSearcher{results: []entities.KnowledgeSearchResult{
			{
				KnowledgeChunk: entities.KnowledgeChunk{
					ID:         uuid.New(),
					SourceDoc:  "emergency-fund.md",
					ChunkIndex: 2,
					ChunkText:  "Keep emergency savings liquid and separate from daily spending.",
					Metadata:   map[string]interface{}{"topic": "saving"},
				},
				Similarity: 0.91,
			},
			{
				KnowledgeChunk: entities.KnowledgeChunk{
					ID:         uuid.New(),
					SourceDoc:  "low-quality.md",
					ChunkIndex: 0,
					ChunkText:  "This should be filtered out.",
				},
				Similarity: 0.20,
			},
		}},
		logger: zap.NewNop(),
	}

	result, err := o.executeKnowledgeSearch(context.Background(), map[string]interface{}{"query": "emergency fund"})
	require.NoError(t, err)
	require.Equal(t, true, result["found"])
	require.Equal(t, 1, result["sources"])
	require.Contains(t, result["context"], "[Source: emergency-fund.md]")

	sourceDocs, ok := result["source_docs"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, sourceDocs, 1)
	require.Equal(t, "emergency-fund.md", sourceDocs[0]["source_doc"])
	require.Equal(t, 2, sourceDocs[0]["chunk_index"])
	require.Equal(t, 0.91, sourceDocs[0]["similarity"])
}

func TestBuildFinancialProfileParamsValidatesAndNormalizes(t *testing.T) {
	params, changed, err := buildFinancialProfileParams(map[string]interface{}{
		"user_type":              "freelancer",
		"residence_country":      "ng",
		"tax_country":            "gb",
		"primary_currency":       "ngn",
		"earning_currency":       "gbp",
		"spending_currency":      "ngn",
		"family_support_country": "ng",
		"income_frequency":       "monthly",
		"monthly_income":         float64(450000),
		"monthly_fixed_costs":    "120000.50",
		"monthly_savings_target": float64(75000),
		"risk_tolerance":         "medium",
		"investment_horizon":     "long_term",
		"financial_goal":         "Build a six-month emergency fund",
	})

	require.NoError(t, err)
	require.Contains(t, changed, "user type")
	require.Equal(t, "freelancer", params["user_type"])
	require.Equal(t, "NG", params["residence_country"])
	require.Equal(t, "GB", params["tax_country"])
	require.Contains(t, changed, "currency")
	require.Equal(t, "NGN", params["primary_currency"])
	require.Equal(t, "GBP", params["earning_currency"])
	require.Equal(t, "NGN", params["spending_currency"])
	require.Equal(t, "NG", params["family_support_country"])
	require.Equal(t, "450000.00", params["monthly_income"])
	require.Equal(t, "120000.50", params["monthly_fixed_costs"])
	require.Equal(t, "medium", params["risk_tolerance"])
	require.Equal(t, "long_term", params["investment_horizon"])
	require.Equal(t, "Build a six-month emergency fund", params["financial_goal"])
}

func TestBuildFinancialProfileParamsRejectsUnsupportedRisk(t *testing.T) {
	_, _, err := buildFinancialProfileParams(map[string]interface{}{
		"risk_tolerance": "reckless",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported")
}

func TestBuildFinancialProfileParamsRejectsBadCountryCode(t *testing.T) {
	_, _, err := buildFinancialProfileParams(map[string]interface{}{
		"residence_country": "nigeria",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "2-letter")
}

func TestExecutePersonaMoneyContextForFounder(t *testing.T) {
	userID := uuid.New()
	o := &Orchestrator{
		aggregateStats: govAggregateStatsFake{
			spend: decimalRequire("1200"),
			stash: decimalRequire("800"),
		},
		financialProfile: govFinancialProfileFake{
			profile: &entities.FinancialProfile{
				UserID:               userID,
				UserType:             "founder",
				ResidenceCountry:     "NG",
				TaxCountry:           "US",
				PrimaryCurrency:      "USD",
				EarningCurrency:      "USD",
				SpendingCurrency:     "NGN",
				FamilySupportCountry: "NG",
				IncomeFrequency:      "irregular",
				MonthlyIncome:        decimalRequire("5000"),
				MonthlyFixedCosts:    decimalRequire("2400"),
				RiskTolerance:        "medium",
				InvestmentHorizon:    "long_term",
				FinancialGoal:        "Extend startup runway",
			},
		},
		logger: zap.NewNop(),
	}

	result, err := o.executePersonaMoneyContext(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, true, result["has_profile"])

	priorities, ok := result["persona_priorities"].([]string)
	require.True(t, ok)
	require.Contains(t, priorities, "runway")
	require.Contains(t, priorities, "payroll")

	playbook, ok := result["geo_playbook"].([]string)
	require.True(t, ok)
	require.NotEmpty(t, playbook)
	require.Contains(t, playbook, "User may have cross-border tax context; separate residence-country spending from tax-country reporting.")
}

func decimalRequire(value string) decimal.Decimal {
	d, err := decimal.NewFromString(value)
	if err != nil {
		panic(err)
	}
	return d
}
