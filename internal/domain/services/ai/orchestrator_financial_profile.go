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
	ToolGetPersonaMoneyContext = "get_persona_money_context"
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
			Description: "Get the user's durable financial profile: user type, geography, preferred currency, income cadence, income, fixed costs, savings targets, emergency fund target, risk tolerance, investment horizon, and main goal. Use before personalized planning or recommendations.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
		{
			Name:        ToolGetPersonaMoneyContext,
			Description: "Get a deterministic persona and geography-aware money context for individuals, freelancers, founders, families, and high earners. Use before answering persona-specific planning questions about budgeting, debt, savings, investing, taxes, invoices, runway, payroll, family goals, insurance, education planning, asset allocation, estate prompts, or advisor coordination.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
		{
			Name:        ToolUpdateFinancialProfile,
			Description: "Update durable financial profile fields when the user explicitly shares their user type, residence/tax country, earning/spending currency, family-support country, goals, income cadence, monthly income, fixed costs, savings target, emergency fund target, risk tolerance, or investment horizon. Requires user confirmation before saving.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"user_type":              map[string]interface{}{"type": "string", "enum": []string{"individual", "freelancer", "founder", "family", "high_earner"}, "description": "Primary user persona Miriam should optimize for"},
					"residence_country":      map[string]interface{}{"type": "string", "description": "ISO 3166-1 alpha-2 country of residence, for example NG, GB, US"},
					"tax_country":            map[string]interface{}{"type": "string", "description": "Primary tax country, for example NG, GB, US"},
					"primary_currency":       map[string]interface{}{"type": "string", "description": "Currency code, for example USD, NGN, GBP, EUR"},
					"earning_currency":       map[string]interface{}{"type": "string", "description": "Currency the user mostly earns in"},
					"spending_currency":      map[string]interface{}{"type": "string", "description": "Currency the user mostly spends in"},
					"family_support_country": map[string]interface{}{"type": "string", "description": "Country where the user regularly supports family, if applicable"},
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
		"user_type":              profile.UserType,
		"residence_country":      profile.ResidenceCountry,
		"tax_country":            profile.TaxCountry,
		"primary_currency":       profile.PrimaryCurrency,
		"earning_currency":       profile.EarningCurrency,
		"spending_currency":      profile.SpendingCurrency,
		"family_support_country": profile.FamilySupportCountry,
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

func (o *Orchestrator) executePersonaMoneyContext(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
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
			"message":     "No persona/geography profile saved yet. Ask for user type, residence country, tax country, earning currency, and spending currency before giving a detailed plan.",
			"required_fields": []string{
				"user_type",
				"residence_country",
				"tax_country",
				"earning_currency",
				"spending_currency",
			},
		}, nil
	}

	userType := coalesceString(profile.UserType, "individual")
	spend, stash, total := o.currentBalances(ctx, userID)
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	flow := o.monthFlow(ctx, userID, monthStart, now)
	totalOut := flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P).Add(flow.TotalReceipts)
	netFlow := flow.TotalDeposits.Sub(totalOut)

	missing := missingPersonaFields(profile)
	priorities, workflows := personaPriorities(userType)
	geo := geoPlaybook(profile)

	return map[string]interface{}{
		"has_profile": true,
		"profile": map[string]interface{}{
			"user_type":              userType,
			"residence_country":      profile.ResidenceCountry,
			"tax_country":            profile.TaxCountry,
			"primary_currency":       profile.PrimaryCurrency,
			"earning_currency":       profile.EarningCurrency,
			"spending_currency":      profile.SpendingCurrency,
			"family_support_country": profile.FamilySupportCountry,
			"income_frequency":       profile.IncomeFrequency,
			"risk_tolerance":         profile.RiskTolerance,
			"investment_horizon":     profile.InvestmentHorizon,
			"financial_goal":         profile.FinancialGoal,
		},
		"current_snapshot": map[string]interface{}{
			"spend_balance":       spend.StringFixed(2),
			"stash_balance":       stash.StringFixed(2),
			"total_balance":       total.StringFixed(2),
			"month_income":        flow.TotalDeposits.StringFixed(2),
			"month_outflow":       totalOut.StringFixed(2),
			"month_net_flow":      netFlow.StringFixed(2),
			"monthly_income":      profile.MonthlyIncome.StringFixed(2),
			"monthly_fixed_costs": profile.MonthlyFixedCosts.StringFixed(2),
		},
		"persona_priorities": priorities,
		"paid_workflows":     workflows,
		"geo_playbook":       geo,
		"missing_fields":     missing,
		"guidance":           personaGuidance(userType),
		"data_used":          []string{"financial_profile", "current_balances", "month_money_flow"},
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
			"user_type":              profile.UserType,
			"residence_country":      profile.ResidenceCountry,
			"tax_country":            profile.TaxCountry,
			"primary_currency":       profile.PrimaryCurrency,
			"earning_currency":       profile.EarningCurrency,
			"spending_currency":      profile.SpendingCurrency,
			"family_support_country": profile.FamilySupportCountry,
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
		"[User financial profile — type: %s | residence: %s | tax country: %s | primary currency: %s | earns in: %s | spends in: %s | family support country: %s | income frequency: %s | monthly income: %s | fixed costs: %s | savings target: %s | emergency target: %s | risk tolerance: %s | horizon: %s | goal: %s. Use this only as personalization context; still call tools for current balances, transactions, budgets, and activity.]",
		coalesceString(profile.UserType, "individual"),
		profile.ResidenceCountry,
		profile.TaxCountry,
		profile.PrimaryCurrency,
		profile.EarningCurrency,
		profile.SpendingCurrency,
		profile.FamilySupportCountry,
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

	if v, ok, err := stringEnumArg(args, "user_type", map[string]bool{"individual": true, "freelancer": true, "founder": true, "family": true, "high_earner": true}); err != nil {
		return nil, nil, err
	} else if ok {
		params["user_type"] = v
		changed = append(changed, "user type")
	}
	if v, ok, err := countryCodeArg(args, "residence_country"); err != nil {
		return nil, nil, err
	} else if ok {
		params["residence_country"] = v
		changed = append(changed, "residence country")
	}
	if v, ok, err := countryCodeArg(args, "tax_country"); err != nil {
		return nil, nil, err
	} else if ok {
		params["tax_country"] = v
		changed = append(changed, "tax country")
	}
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
	if v, ok, err := currencyCodeArg(args, "earning_currency"); err != nil {
		return nil, nil, err
	} else if ok {
		params["earning_currency"] = v
		changed = append(changed, "earning currency")
	}
	if v, ok, err := currencyCodeArg(args, "spending_currency"); err != nil {
		return nil, nil, err
	} else if ok {
		params["spending_currency"] = v
		changed = append(changed, "spending currency")
	}
	if v, ok, err := countryCodeArg(args, "family_support_country"); err != nil {
		return nil, nil, err
	} else if ok {
		params["family_support_country"] = v
		changed = append(changed, "family support country")
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
	if v, ok := stringParam(params, "user_type"); ok {
		update.UserType = &v
	}
	if v, ok := stringParam(params, "residence_country"); ok {
		update.ResidenceCountry = &v
	}
	if v, ok := stringParam(params, "tax_country"); ok {
		update.TaxCountry = &v
	}
	if v, ok := stringParam(params, "primary_currency"); ok {
		update.PrimaryCurrency = &v
	}
	if v, ok := stringParam(params, "earning_currency"); ok {
		update.EarningCurrency = &v
	}
	if v, ok := stringParam(params, "spending_currency"); ok {
		update.SpendingCurrency = &v
	}
	if v, ok := stringParam(params, "family_support_country"); ok {
		update.FamilySupportCountry = &v
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

func currencyCodeArg(args map[string]interface{}, key string) (string, bool, error) {
	v, ok, err := stringEnumArg(args, key, nil)
	if err != nil || !ok {
		return "", ok, err
	}
	v = strings.ToUpper(v)
	if len(v) < 3 || len(v) > 10 {
		return "", false, fmt.Errorf("%s must be a valid currency code", key)
	}
	return v, true, nil
}

func countryCodeArg(args map[string]interface{}, key string) (string, bool, error) {
	v, ok, err := stringEnumArg(args, key, nil)
	if err != nil || !ok {
		return "", ok, err
	}
	v = strings.ToUpper(v)
	if len(v) != 2 {
		return "", false, fmt.Errorf("%s must be a 2-letter country code", key)
	}
	return v, true, nil
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

func coalesceString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "unknown") {
		return fallback
	}
	return value
}

func missingPersonaFields(profile *entities.FinancialProfile) []string {
	if profile == nil {
		return []string{"user_type", "residence_country", "tax_country", "earning_currency", "spending_currency"}
	}
	var missing []string
	if strings.TrimSpace(profile.UserType) == "" {
		missing = append(missing, "user_type")
	}
	if strings.TrimSpace(profile.ResidenceCountry) == "" {
		missing = append(missing, "residence_country")
	}
	if strings.TrimSpace(profile.TaxCountry) == "" {
		missing = append(missing, "tax_country")
	}
	if strings.TrimSpace(profile.EarningCurrency) == "" {
		missing = append(missing, "earning_currency")
	}
	if strings.TrimSpace(profile.SpendingCurrency) == "" {
		missing = append(missing, "spending_currency")
	}
	if strings.EqualFold(profile.IncomeFrequency, "unknown") {
		missing = append(missing, "income_frequency")
	}
	if profile.MonthlyIncome.IsZero() {
		missing = append(missing, "monthly_income")
	}
	return missing
}

func personaPriorities(userType string) ([]string, []string) {
	switch userType {
	case "freelancer":
		return []string{"irregular income", "tax reserve", "invoice visibility", "deduction capture", "cash buffer"},
			[]string{"income smoothing plan", "quarterly tax reserve", "invoice aging review", "receipt deduction report", "client concentration check"}
	case "founder":
		return []string{"runway", "burn", "payroll", "vendor obligations", "investor updates"},
			[]string{"runway forecast", "burn-rate review", "payroll/vendor calendar", "cash planning brief", "investor update draft"}
	case "family":
		return []string{"shared budget", "shared goals", "family support", "insurance reminders", "education planning"},
			[]string{"household budget review", "shared goal contribution plan", "family support cap", "insurance checklist", "education fund plan"}
	case "high_earner":
		return []string{"tax exposure", "asset allocation", "concentration risk", "estate planning prompts", "advisor coordination"},
			[]string{"tax readiness packet", "allocation risk review", "concentration alert", "estate planning checklist", "advisor/accountant briefing"}
	default:
		return []string{"budgeting", "debt", "savings", "investing", "taxes"},
			[]string{"safe-to-spend forecast", "debt payoff plan", "emergency fund plan", "portfolio risk summary", "tax readiness summary"}
	}
}

func personaGuidance(userType string) string {
	switch userType {
	case "freelancer":
		return "Treat cash flow as uneven. Separate tax reserve, living costs, and investable surplus before recommending extra transfers."
	case "founder":
		return "Lead with runway, burn, and obligations. Do not suggest aggressive savings or investing until payroll/vendor cash needs are protected."
	case "family":
		return "Optimize for shared obligations and dependents. Ask before assuming who contributes, who spends, or what education/insurance goals matter."
	case "high_earner":
		return "Focus on coordination and risk. Keep tax, estate, and legal guidance informational and recommend professional review for decisions."
	default:
		return "Cover budget, debt, savings, investing, and taxes in that order. Protect liquidity before suggesting more Stash or investing."
	}
}

func geoPlaybook(profile *entities.FinancialProfile) []string {
	if profile == nil {
		return nil
	}
	country := strings.ToUpper(strings.TrimSpace(profile.ResidenceCountry))
	taxCountry := strings.ToUpper(strings.TrimSpace(profile.TaxCountry))
	var playbook []string
	switch country {
	case "NG":
		playbook = append(playbook, "Show USD stash as protection against NGN weakness when useful.", "Track NGN cash-out and family support pressure.", "Keep small amounts meaningful; do not dismiss low balances.")
	case "GB":
		playbook = append(playbook, "Use UK tax-year framing where relevant.", "Track GBP income vs USD/NGN support flows.", "Prepare accountant-friendly summaries, not tax conclusions.")
	case "US":
		playbook = append(playbook, "Flag digital-asset tax reporting readiness.", "Track estimated-tax style reminders for freelancers/founders.", "Prepare CPA-friendly transaction summaries.")
	default:
		if country != "" {
			playbook = append(playbook, "Use local residence context, but avoid unsupported country-specific tax claims.")
		}
	}
	if taxCountry != "" && taxCountry != country {
		playbook = append(playbook, "User may have cross-border tax context; separate residence-country spending from tax-country reporting.")
	}
	if profile.EarningCurrency != "" && profile.SpendingCurrency != "" && !strings.EqualFold(profile.EarningCurrency, profile.SpendingCurrency) {
		playbook = append(playbook, "User earns and spends in different currencies; explain FX exposure and local safe-to-spend carefully.")
	}
	if profile.FamilySupportCountry != "" {
		playbook = append(playbook, "Include family-support budget pressure when discussing affordability.")
	}
	return playbook
}
