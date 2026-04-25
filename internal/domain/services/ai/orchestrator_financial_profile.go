package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
)

const (
	ToolGetFinancialProfile    = "get_financial_profile"
	ToolUpdateFinancialProfile = "update_financial_profile"
)

// FinancialProfileProvider reads and writes durable personalization context.
type FinancialProfileProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.FinancialProfile, error)
	Upsert(ctx context.Context, userID uuid.UUID, update entities.FinancialProfileUpdate) (*entities.FinancialProfile, error)
}

// SetFinancialProfileProvider wires the durable personalization provider.
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *Orchestrator) SetFinancialProfileProvider(p FinancialProfileProvider) {
	o.financialProfile = p
}

func FinancialProfileTools() []infraai.Tool {
	return []infraai.Tool{
		{
			Name:        ToolGetFinancialProfile,
			Description: "Get the user's durable financial profile: preferred currency, income cadence, income, fixed costs, savings targets, emergency fund target, risk tolerance, investment horizon, and main goal. Use before personalized planning or recommendations.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
		{
			Name:        ToolUpdateFinancialProfile,
			Description: "Update durable financial profile fields when the user explicitly shares goals, income cadence, monthly income, fixed costs, savings target, emergency fund target, risk tolerance, or investment horizon. Requires user confirmation before saving.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"primary_currency":       map[string]interface{}{"type": "string", "description": "Currency code, for example USD, NGN, GBP, EUR"},
					"income_frequency":       map[string]interface{}{"type": "string", "enum": []string{"weekly", "biweekly", "monthly", "irregular"}},
					"monthly_income":         map[string]interface{}{"type": "number", "description": "Expected monthly income in primary_currency"},
					"monthly_fixed_costs":    map[string]interface{}{"type": "number", "description": "Expected fixed monthly expenses in primary_currency"},
					"monthly_savings_target": map[string]interface{}{"type": "number", "description": "Target amount to save each month in primary_currency"},
					"emergency_fund_target":  map[string]interface{}{"type": "number", "description": "Emergency fund target in primary_currency"},
					"risk_tolerance":         map[string]interface{}{"type": "string", "enum": []string{"low", "medium", "high"}},
					"investment_horizon":     map[string]interface{}{"type": "string", "enum": []string{"short_term", "medium_term", "long_term"}},
					"financial_goal":         map[string]interface{}{"type": "string", "description": "Short plain-language primary financial goal"},
				},
			},
		},
	}
}

func (o *Orchestrator) executeGetFinancialProfile(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.financialProfile == nil {
		return map[string]interface{}{"error": "financial profile not available"}, nil
	}
	profile, err := o.financialProfile.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get financial profile: %w", err)
	}
	if profile == nil {
		return map[string]interface{}{
			"has_profile": false,
			"message":     "No financial profile saved yet. Ask one or two lightweight questions before making highly personalized plans.",
		}, nil
	}
	return map[string]interface{}{
		"has_profile":            true,
		"primary_currency":       profile.PrimaryCurrency,
		"income_frequency":       profile.IncomeFrequency,
		"monthly_income":         profile.MonthlyIncome.StringFixed(2),
		"monthly_fixed_costs":    profile.MonthlyFixedCosts.StringFixed(2),
		"monthly_savings_target": profile.MonthlySavingsTarget.StringFixed(2),
		"emergency_fund_target":  profile.EmergencyFundTarget.StringFixed(2),
		"risk_tolerance":         profile.RiskTolerance,
		"investment_horizon":     profile.InvestmentHorizon,
		"financial_goal":         profile.FinancialGoal,
		"updated_at":             profile.UpdatedAt.Format(time.RFC3339),
	}, nil
}

func (o *Orchestrator) createFinancialProfileAction(ctx context.Context, userID, convID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	params, changedFields, err := buildFinancialProfileParams(args)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}, nil
	}
	if len(params) == 0 {
		return map[string]interface{}{"error": "No supported financial profile fields were provided"}, nil
	}

	description := "Save financial profile update"
	if len(changedFields) > 0 {
		description = fmt.Sprintf("Save financial profile update: %s", strings.Join(changedFields, ", "))
	}
	action := &entities.PendingAction{
		ID:             uuid.New().String(),
		ConversationID: convID,
		UserID:         userID,
		Action:         ToolUpdateFinancialProfile,
		Description:    description,
		Params:         params,
		ExpiresAt:      time.Now().Add(pendingActionTTL),
		CreatedAt:      time.Now(),
	}
	if err := o.pending.Set(ctx, convID, action); err != nil {
		return nil, fmt.Errorf("store pending financial profile action: %w", err)
	}

	return map[string]interface{}{
		"action_required": true,
		"pending_action":  action,
	}, nil
}

func (o *Orchestrator) executeUpdateFinancialProfile(ctx context.Context, userID uuid.UUID, params map[string]interface{}) (map[string]interface{}, error) {
	if o.financialProfile == nil {
		return map[string]interface{}{"error": "financial profile not available"}, nil
	}
	update, err := financialProfileUpdateFromParams(params)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}, nil
	}
	profile, err := o.financialProfile.Upsert(ctx, userID, update)
	if err != nil {
		return nil, fmt.Errorf("update financial profile: %w", err)
	}
	return map[string]interface{}{
		"success": true,
		"profile": map[string]interface{}{
			"primary_currency":       profile.PrimaryCurrency,
			"income_frequency":       profile.IncomeFrequency,
			"monthly_income":         profile.MonthlyIncome.StringFixed(2),
			"monthly_fixed_costs":    profile.MonthlyFixedCosts.StringFixed(2),
			"monthly_savings_target": profile.MonthlySavingsTarget.StringFixed(2),
			"emergency_fund_target":  profile.EmergencyFundTarget.StringFixed(2),
			"risk_tolerance":         profile.RiskTolerance,
			"investment_horizon":     profile.InvestmentHorizon,
			"financial_goal":         profile.FinancialGoal,
		},
	}, nil
}

func (o *Orchestrator) buildFinancialProfileContext(ctx context.Context, userID uuid.UUID) string {
	if o.financialProfile == nil {
		return ""
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	profile, err := o.financialProfile.GetByUserID(fetchCtx, userID)
	if err != nil || profile == nil {
		return ""
	}
	return fmt.Sprintf(
		"[User financial profile — currency: %s | income frequency: %s | monthly income: %s | fixed costs: %s | savings target: %s | emergency target: %s | risk tolerance: %s | horizon: %s | goal: %s. Use this only as personalization context; still call tools for current balances, transactions, budgets, and activity.]",
		profile.PrimaryCurrency,
		profile.IncomeFrequency,
		profile.MonthlyIncome.StringFixed(2),
		profile.MonthlyFixedCosts.StringFixed(2),
		profile.MonthlySavingsTarget.StringFixed(2),
		profile.EmergencyFundTarget.StringFixed(2),
		profile.RiskTolerance,
		profile.InvestmentHorizon,
		profile.FinancialGoal,
	)
}

func buildFinancialProfileParams(args map[string]interface{}) (map[string]interface{}, []string, error) {
	params := make(map[string]interface{})
	var changed []string

	if v, ok, err := stringEnumArg(args, "primary_currency", nil); err != nil {
		return nil, nil, err
	} else if ok {
		v = strings.ToUpper(v)
		if len(v) < 3 || len(v) > 10 {
			return nil, nil, fmt.Errorf("primary_currency must be a valid currency code")
		}
		params["primary_currency"] = v
		changed = append(changed, "currency")
	}
	if v, ok, err := stringEnumArg(args, "income_frequency", map[string]bool{"weekly": true, "biweekly": true, "monthly": true, "irregular": true}); err != nil {
		return nil, nil, err
	} else if ok {
		params["income_frequency"] = v
		changed = append(changed, "income frequency")
	}
	if v, ok, err := nonNegativeDecimalArg(args, "monthly_income"); err != nil {
		return nil, nil, err
	} else if ok {
		params["monthly_income"] = v.StringFixed(2)
		changed = append(changed, "monthly income")
	}
	if v, ok, err := nonNegativeDecimalArg(args, "monthly_fixed_costs"); err != nil {
		return nil, nil, err
	} else if ok {
		params["monthly_fixed_costs"] = v.StringFixed(2)
		changed = append(changed, "fixed costs")
	}
	if v, ok, err := nonNegativeDecimalArg(args, "monthly_savings_target"); err != nil {
		return nil, nil, err
	} else if ok {
		params["monthly_savings_target"] = v.StringFixed(2)
		changed = append(changed, "savings target")
	}
	if v, ok, err := nonNegativeDecimalArg(args, "emergency_fund_target"); err != nil {
		return nil, nil, err
	} else if ok {
		params["emergency_fund_target"] = v.StringFixed(2)
		changed = append(changed, "emergency fund target")
	}
	if v, ok, err := stringEnumArg(args, "risk_tolerance", map[string]bool{"low": true, "medium": true, "high": true}); err != nil {
		return nil, nil, err
	} else if ok {
		params["risk_tolerance"] = v
		changed = append(changed, "risk tolerance")
	}
	if v, ok, err := stringEnumArg(args, "investment_horizon", map[string]bool{"short_term": true, "medium_term": true, "long_term": true}); err != nil {
		return nil, nil, err
	} else if ok {
		params["investment_horizon"] = v
		changed = append(changed, "investment horizon")
	}
	if v, ok, err := freeTextArg(args, "financial_goal"); err != nil {
		return nil, nil, err
	} else if ok {
		if len(v) > 280 {
			return nil, nil, fmt.Errorf("financial_goal must be 280 characters or less")
		}
		params["financial_goal"] = v
		changed = append(changed, "goal")
	}
	return params, changed, nil
}

func financialProfileUpdateFromParams(params map[string]interface{}) (entities.FinancialProfileUpdate, error) {
	var update entities.FinancialProfileUpdate
	if v, ok := stringParam(params, "primary_currency"); ok {
		update.PrimaryCurrency = &v
	}
	if v, ok := stringParam(params, "income_frequency"); ok {
		update.IncomeFrequency = &v
	}
	if v, ok, err := decimalParam(params, "monthly_income"); err != nil {
		return update, err
	} else if ok {
		update.MonthlyIncome = &v
	}
	if v, ok, err := decimalParam(params, "monthly_fixed_costs"); err != nil {
		return update, err
	} else if ok {
		update.MonthlyFixedCosts = &v
	}
	if v, ok, err := decimalParam(params, "monthly_savings_target"); err != nil {
		return update, err
	} else if ok {
		update.MonthlySavingsTarget = &v
	}
	if v, ok, err := decimalParam(params, "emergency_fund_target"); err != nil {
		return update, err
	} else if ok {
		update.EmergencyFundTarget = &v
	}
	if v, ok := stringParam(params, "risk_tolerance"); ok {
		update.RiskTolerance = &v
	}
	if v, ok := stringParam(params, "investment_horizon"); ok {
		update.InvestmentHorizon = &v
	}
	if v, ok := stringParam(params, "financial_goal"); ok {
		update.FinancialGoal = &v
	}
	update.Metadata = map[string]interface{}{"updated_via": "ai_chat"}
	return update, nil
}

func stringEnumArg(args map[string]interface{}, key string, allowed map[string]bool) (string, bool, error) {
	raw, ok := args[key]
	if !ok {
		return "", false, nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", false, fmt.Errorf("%s must be a string", key)
	}
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "", false, nil
	}
	if allowed != nil && !allowed[value] {
		return "", false, fmt.Errorf("%s has unsupported value %q", key, value)
	}
	return value, true, nil
}

func freeTextArg(args map[string]interface{}, key string) (string, bool, error) {
	raw, ok := args[key]
	if !ok {
		return "", false, nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", false, fmt.Errorf("%s must be a string", key)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false, nil
	}
	return value, true, nil
}

func nonNegativeDecimalArg(args map[string]interface{}, key string) (decimal.Decimal, bool, error) {
	raw, ok := args[key]
	if !ok {
		return decimal.Zero, false, nil
	}
	var value decimal.Decimal
	switch v := raw.(type) {
	case float64:
		value = decimal.NewFromFloat(v)
	case int:
		value = decimal.NewFromInt(int64(v))
	case string:
		parsed, err := decimal.NewFromString(strings.TrimSpace(v))
		if err != nil {
			return decimal.Zero, false, fmt.Errorf("%s must be a number", key)
		}
		value = parsed
	default:
		return decimal.Zero, false, fmt.Errorf("%s must be a number", key)
	}
	if value.IsNegative() {
		return decimal.Zero, false, fmt.Errorf("%s must be non-negative", key)
	}
	if value.GreaterThan(decimal.NewFromInt(10000000)) {
		return decimal.Zero, false, fmt.Errorf("%s is too large", key)
	}
	return value, true, nil
}

func stringParam(params map[string]interface{}, key string) (string, bool) {
	raw, ok := params[key]
	if !ok {
		return "", false
	}
	value := strings.TrimSpace(fmt.Sprintf("%v", raw))
	return value, value != ""
}

func decimalParam(params map[string]interface{}, key string) (decimal.Decimal, bool, error) {
	raw, ok := params[key]
	if !ok {
		return decimal.Zero, false, nil
	}
	value, err := decimal.NewFromString(strings.TrimSpace(fmt.Sprintf("%v", raw)))
	if err != nil {
		return decimal.Zero, false, fmt.Errorf("invalid %s: %w", key, err)
	}
	return value, true, nil
}
