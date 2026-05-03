package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// FinancialProfileRepository persists durable financial context for the AI agent.
type FinancialProfileRepository struct {
	db *sqlx.DB
}

func NewFinancialProfileRepository(db *sqlx.DB) *FinancialProfileRepository {
	return &FinancialProfileRepository{db: db}
}

func (r *FinancialProfileRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.FinancialProfile, error) {
	var profile entities.FinancialProfile
	var metadataJSON []byte
	err := r.db.QueryRowxContext(ctx, `
			SELECT user_id, user_type, residence_country, tax_country, primary_currency,
			       earning_currency, spending_currency, family_support_country,
			       income_frequency, monthly_income, monthly_fixed_costs,
			       monthly_savings_target, emergency_fund_target, risk_tolerance, investment_horizon,
			       financial_goal, metadata, created_at, updated_at
			FROM financial_profiles
			WHERE user_id = $1`, userID).Scan(
		&profile.UserID,
		&profile.UserType,
		&profile.ResidenceCountry,
		&profile.TaxCountry,
		&profile.PrimaryCurrency,
		&profile.EarningCurrency,
		&profile.SpendingCurrency,
		&profile.FamilySupportCountry,
		&profile.IncomeFrequency,
		&profile.MonthlyIncome,
		&profile.MonthlyFixedCosts,
		&profile.MonthlySavingsTarget,
		&profile.EmergencyFundTarget,
		&profile.RiskTolerance,
		&profile.InvestmentHorizon,
		&profile.FinancialGoal,
		&metadataJSON,
		&profile.CreatedAt,
		&profile.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get financial profile: %w", err)
	}
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &profile.Metadata); err != nil {
			return nil, fmt.Errorf("decode financial profile metadata: %w", err)
		}
	}
	return &profile, nil
}

func (r *FinancialProfileRepository) Upsert(ctx context.Context, userID uuid.UUID, update entities.FinancialProfileUpdate) (*entities.FinancialProfile, error) {
	metadataJSON := []byte(`{}`)
	if update.Metadata != nil {
		var err error
		metadataJSON, err = json.Marshal(update.Metadata)
		if err != nil {
			return nil, fmt.Errorf("encode financial profile metadata: %w", err)
		}
	}

	_, err := r.db.ExecContext(ctx, `
			INSERT INTO financial_profiles (
				user_id, user_type, residence_country, tax_country, primary_currency,
				earning_currency, spending_currency, family_support_country,
				income_frequency, monthly_income, monthly_fixed_costs,
				monthly_savings_target, emergency_fund_target, risk_tolerance, investment_horizon,
				financial_goal, metadata, created_at, updated_at
			)
			VALUES (
				$1,
				COALESCE($2, 'individual'),
				COALESCE($3, ''),
				COALESCE($4, ''),
				COALESCE($5, 'USD'),
				COALESCE($6, ''),
				COALESCE($7, ''),
				COALESCE($8, ''),
				COALESCE($9, 'unknown'),
				COALESCE($10::numeric, 0),
				COALESCE($11::numeric, 0),
				COALESCE($12::numeric, 0),
				COALESCE($13::numeric, 0),
				COALESCE($14, 'unknown'),
				COALESCE($15, 'unknown'),
				COALESCE($16, ''),
				COALESCE($17::jsonb, '{}'::jsonb),
				NOW(),
				NOW()
			)
			ON CONFLICT (user_id) DO UPDATE SET
				user_type = COALESCE($2, financial_profiles.user_type),
				residence_country = COALESCE($3, financial_profiles.residence_country),
				tax_country = COALESCE($4, financial_profiles.tax_country),
				primary_currency = COALESCE($5, financial_profiles.primary_currency),
				earning_currency = COALESCE($6, financial_profiles.earning_currency),
				spending_currency = COALESCE($7, financial_profiles.spending_currency),
				family_support_country = COALESCE($8, financial_profiles.family_support_country),
				income_frequency = COALESCE($9, financial_profiles.income_frequency),
				monthly_income = COALESCE($10::numeric, financial_profiles.monthly_income),
				monthly_fixed_costs = COALESCE($11::numeric, financial_profiles.monthly_fixed_costs),
				monthly_savings_target = COALESCE($12::numeric, financial_profiles.monthly_savings_target),
				emergency_fund_target = COALESCE($13::numeric, financial_profiles.emergency_fund_target),
				risk_tolerance = COALESCE($14, financial_profiles.risk_tolerance),
				investment_horizon = COALESCE($15, financial_profiles.investment_horizon),
				financial_goal = COALESCE($16, financial_profiles.financial_goal),
				metadata = CASE
					WHEN $17::jsonb IS NULL THEN financial_profiles.metadata
					ELSE financial_profiles.metadata || $17::jsonb
				END,
				updated_at = NOW()`,
		userID,
		update.UserType,
		update.ResidenceCountry,
		update.TaxCountry,
		update.PrimaryCurrency,
		update.EarningCurrency,
		update.SpendingCurrency,
		update.FamilySupportCountry,
		update.IncomeFrequency,
		update.MonthlyIncome,
		update.MonthlyFixedCosts,
		update.MonthlySavingsTarget,
		update.EmergencyFundTarget,
		update.RiskTolerance,
		update.InvestmentHorizon,
		update.FinancialGoal,
		jsonOrNil(update.Metadata, metadataJSON),
	)
	if err != nil {
		return nil, fmt.Errorf("upsert financial profile: %w", err)
	}
	return r.GetByUserID(ctx, userID)
}

func jsonOrNil(metadata map[string]interface{}, encoded []byte) interface{} {
	if metadata == nil {
		return nil
	}
	return string(encoded)
}
