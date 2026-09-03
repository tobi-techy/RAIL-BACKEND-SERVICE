package ai

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	aiobligations "github.com/rail-service/rail_service/internal/domain/services/ai/obligations"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
)

const ToolGetMoneyOperatingPlan = "get_money_operating_plan"

// FinancialObligationProvider reads user-entered obligations for monthly planning.
// Deprecated: Use aiobligations.FinancialObligationProvider instead.
type FinancialObligationProvider = aiobligations.FinancialObligationProvider

// CurrencyRateProvider reads latest FX rates for geography-aware planning.
type CurrencyRateProvider interface {
	GetLatestRate(ctx context.Context, from, to string) (decimal.Decimal, error)
}

// SetFinancialObligationProvider wires manual obligations into Miriam planning.
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *AgentAdapter) SetFinancialObligationProvider(p FinancialObligationProvider) {
	o.obligations = p
}

func (o *AgentAdapter) SetCurrencyRateProvider(p CurrencyRateProvider) {
	o.currencyRates = p
}

// SetTravel wires the BRIJ travel provider used to resolve the exact booking
// charge for the book_flight confirmation card.
func (o *AgentAdapter) SetTravel(t core.TravelProvider) {
	o.travel = t
}

func MoneyOperatingPlanTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolGetMoneyOperatingPlan,
		Description: "Build Miriam's monthly money operating plan from Rail balances, month flow, financial profile, geography, budget, and manual obligations. Returns safe spend, tax reserve, stash target, family-support cap, obligation coverage, risk flags, and approval-gated next actions.",
		Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
	}
}

func (o *AgentAdapter) executeMoneyOperatingPlan(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	profile, err := o.financialProfile.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get financial profile: %w", err)
	}
	if profile == nil {
		allFields := []string{
			"user_type",
			"residence_country",
			"tax_country",
			"earning_currency",
			"spending_currency",
			"manual_obligations",
		}
		priorityQuestions := allFields
		if len(priorityQuestions) > 3 {
			priorityQuestions = priorityQuestions[:3]
		}
		return map[string]interface{}{
			"has_profile":        false,
			"message":            "No persona/geography profile saved yet. Ask for user type, residence country, tax country, earning currency, spending currency, and obligations before building a paid operating plan.",
			"required_fields":    allFields,
			"priority_questions": priorityQuestions,
		}, nil
	}

	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	spend, stash, total := o.currentBalances(ctx, userID)
	flow := o.monthFlow(ctx, userID, monthStart, now)
	totalOut := flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P).Add(flow.TotalReceipts)
	monthNet := flow.TotalDeposits.Sub(totalOut)

	obligations := []entities.FinancialObligation{}
	if o.obligations != nil {
		obligations, err = o.obligations.ListActive(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("list financial obligations: %w", err)
		}
	}

	obligationPlan := summarizeObligations(obligations, now)
	monthlyIncome := profile.MonthlyIncome
	if monthlyIncome.IsZero() {
		monthlyIncome = flow.TotalDeposits
	}
	taxReserve := calculateTaxReserve(profile, monthlyIncome, obligationPlan.ByType[entities.ObligationTypeTax])
	stashTarget := calculateStashTarget(profile, monthlyIncome, obligationPlan.RequiredThisMonth)
	familyCap := calculateFamilySupportCap(profile, monthlyIncome, obligationPlan.ByType[entities.ObligationTypeFamilySupport])
	safeSpend := calculateSafeSpend(ctx, o, userID, spend, totalOut, obligationPlan.RequiredThisMonth, taxReserve, now)
	coverage := obligationCoverage(spend, obligationPlan.RequiredThisMonth, taxReserve)
	riskFlags := operatingPlanRiskFlags(profile, obligations, spend, monthlyIncome, monthNet, obligationPlan, taxReserve)
	nextActions := operatingPlanNextActions(profile, safeSpend, stashTarget, taxReserve, familyCap, obligationPlan, riskFlags)
	fxContext := o.fxContext(ctx, profile, monthlyIncome, spend, stash)

	return map[string]interface{}{
		"has_profile": true,
		"profile": map[string]interface{}{
			"user_type":              coalesceString(profile.UserType, "individual"),
			"residence_country":      profile.ResidenceCountry,
			"tax_country":            profile.TaxCountry,
			"earning_currency":       profile.EarningCurrency,
			"spending_currency":      profile.SpendingCurrency,
			"family_support_country": profile.FamilySupportCountry,
			"income_frequency":       profile.IncomeFrequency,
		},
		"month": map[string]interface{}{
			"period_start":  monthStart.Format("2006-01-02"),
			"period_end":    now.Format("2006-01-02"),
			"month_income":  flow.TotalDeposits.StringFixed(2),
			"month_outflow": totalOut.StringFixed(2),
			"month_net":     monthNet.StringFixed(2),
		},
		"balances": map[string]interface{}{
			"spend": spend.StringFixed(2),
			"stash": stash.StringFixed(2),
			"total": total.StringFixed(2),
		},
		"operating_plan": map[string]interface{}{
			"safe_spend_today":         safeSpend.SafeToday.StringFixed(2),
			"safe_spend_rest_month":    safeSpend.RestOfMonth.StringFixed(2),
			"days_left":                safeSpend.DaysLeft,
			"tax_reserve_target":       taxReserve.StringFixed(2),
			"stash_target":             stashTarget.StringFixed(2),
			"family_support_cap":       familyCap.StringFixed(2),
			"obligation_coverage":      coverage,
			"professional_review_note": "Tax, legal, estate, and investment outputs are informational. Review with a qualified professional before filing, investing, or changing legal documents.",
		},
		"fx_context":              fxContext,
		"tax_playbook":            taxPlaybook(profile),
		"persona_operating_model": personaOperatingModel(profile, monthlyIncome, total, obligationPlan),
		"obligations": map[string]interface{}{
			"count":               len(obligations),
			"required_this_month": obligationPlan.RequiredThisMonth.StringFixed(2),
			"critical_this_month": obligationPlan.CriticalThisMonth.StringFixed(2),
			"by_type":             decimalMapStrings(obligationPlan.ByType),
			"upcoming":            obligationPlan.Upcoming,
			"invoice_aging":       obligationPlan.InvoiceAging,
		},
		"risk_flags":   riskFlags,
		"next_actions": nextActions,
		"data_used": []string{
			"financial_profile",
			"current_balances",
			"month_money_flow",
			"manual_financial_obligations",
			"budget_if_present",
		},
	}, nil
}

type safeSpendPlan struct {
	SafeToday   decimal.Decimal
	RestOfMonth decimal.Decimal
	DaysLeft    int
}

type obligationSummary struct {
	RequiredThisMonth decimal.Decimal
	CriticalThisMonth decimal.Decimal
	ByType            map[string]decimal.Decimal
	Upcoming          []map[string]interface{}
	HasInvoice        bool
	HasPayroll        bool
	HasInsurance      bool
	HasEducation      bool
	InvoiceAging      map[string]interface{}
}

func summarizeObligations(obligations []entities.FinancialObligation, now time.Time) obligationSummary {
	summary := obligationSummary{ByType: map[string]decimal.Decimal{}}
	overdueInvoices, dueThisMonthInvoices := 0, 0
	expectedThisMonth := decimal.Zero
	for _, obligation := range obligations {
		monthly := monthlyEquivalent(obligation, now)
		if monthly.IsZero() {
			continue
		}
		summary.RequiredThisMonth = summary.RequiredThisMonth.Add(monthly)
		summary.ByType[obligation.Type] = summary.ByType[obligation.Type].Add(monthly)
		if obligation.Priority == entities.ObligationPriorityCritical {
			summary.CriticalThisMonth = summary.CriticalThisMonth.Add(monthly)
		}
		switch obligation.Type {
		case entities.ObligationTypeInvoice:
			summary.HasInvoice = true
			if obligation.DueDate != nil {
				if obligation.DueDate.Before(now) {
					overdueInvoices++
				}
				if obligation.DueDate.Year() == now.Year() && obligation.DueDate.Month() == now.Month() {
					dueThisMonthInvoices++
					expectedThisMonth = expectedThisMonth.Add(obligation.Amount)
				}
			} else {
				expectedThisMonth = expectedThisMonth.Add(monthly)
			}
		case entities.ObligationTypePayroll:
			summary.HasPayroll = true
		case entities.ObligationTypeInsurance:
			summary.HasInsurance = true
		case entities.ObligationTypeEducation:
			summary.HasEducation = true
		}
		if len(summary.Upcoming) < 8 {
			summary.Upcoming = append(summary.Upcoming, map[string]interface{}{
				"id":       obligation.ID.String(),
				"type":     obligation.Type,
				"name":     obligation.Name,
				"amount":   obligation.Amount.StringFixed(2),
				"currency": obligation.Currency,
				"cadence":  obligation.Cadence,
				"due_day":  obligation.DueDay,
				"priority": obligation.Priority,
			})
		}
	}
	summary.InvoiceAging = map[string]interface{}{
		"overdue_count":         overdueInvoices,
		"due_this_month_count":  dueThisMonthInvoices,
		"expected_this_month":   expectedThisMonth.StringFixed(2),
		"needs_collection_view": summary.HasInvoice,
	}
	return summary
}

func monthlyEquivalent(obligation entities.FinancialObligation, now time.Time) decimal.Decimal {
	switch obligation.Cadence {
	case entities.ObligationCadenceWeekly:
		return obligation.Amount.Mul(decimal.NewFromFloat(4.33))
	case entities.ObligationCadenceBiweekly:
		return obligation.Amount.Mul(decimal.NewFromFloat(2.17))
	case entities.ObligationCadenceMonthly:
		return obligation.Amount
	case entities.ObligationCadenceQuarterly:
		return obligation.Amount.Div(decimal.NewFromInt(3))
	case entities.ObligationCadenceAnnual:
		return obligation.Amount.Div(decimal.NewFromInt(12))
	case entities.ObligationCadenceOneTime:
		if obligation.DueDate == nil {
			return obligation.Amount
		}
		if obligation.DueDate.Year() == now.Year() && obligation.DueDate.Month() == now.Month() {
			return obligation.Amount
		}
	}
	return decimal.Zero
}

func calculateTaxReserve(profile *entities.FinancialProfile, monthlyIncome, explicitTax decimal.Decimal) decimal.Decimal {
	if explicitTax.IsPositive() {
		return explicitTax
	}
	rate := decimal.NewFromFloat(0.10)
	switch coalesceString(profile.UserType, "individual") {
	case "freelancer":
		rate = decimal.NewFromFloat(0.15)
	case "founder":
		rate = decimal.NewFromFloat(0.12)
	case "high_earner":
		rate = decimal.NewFromFloat(0.20)
	}
	if monthlyIncome.IsZero() {
		return decimal.Zero
	}
	return monthlyIncome.Mul(rate)
}

func calculateStashTarget(profile *entities.FinancialProfile, monthlyIncome, obligations decimal.Decimal) decimal.Decimal {
	if profile.MonthlySavingsTarget.IsPositive() {
		return profile.MonthlySavingsTarget
	}
	if monthlyIncome.IsZero() {
		return decimal.Zero
	}
	rate := decimal.NewFromFloat(0.20)
	switch coalesceString(profile.UserType, "individual") {
	case "freelancer":
		rate = decimal.NewFromFloat(0.30)
	case "founder":
		rate = decimal.NewFromFloat(0.10)
	case "high_earner":
		rate = decimal.NewFromFloat(0.25)
	}
	target := monthlyIncome.Sub(obligations).Mul(rate)
	if target.IsNegative() {
		return decimal.Zero
	}
	return target
}

func calculateFamilySupportCap(profile *entities.FinancialProfile, monthlyIncome, explicitFamilySupport decimal.Decimal) decimal.Decimal {
	if explicitFamilySupport.IsPositive() {
		return explicitFamilySupport
	}
	if profile.FamilySupportCountry == "" || monthlyIncome.IsZero() {
		return decimal.Zero
	}
	return monthlyIncome.Mul(decimal.NewFromFloat(0.10))
}

func calculateSafeSpend(ctx context.Context, o *AgentAdapter, userID uuid.UUID, spend, monthOut, obligations, taxReserve decimal.Decimal, now time.Time) safeSpendPlan {
	daysLeft := daysRemainingInMonth(now)
	if daysLeft < 1 {
		daysLeft = 1
	}
	available := spend.Sub(obligations).Sub(taxReserve)
	if o.budgetProvider != nil {
		if budget, err := o.budgetProvider.GetByUserID(ctx, userID); err == nil && budget != nil && budget.MonthlyLimit.IsPositive() {
			budgetRemaining := budget.MonthlyLimit.Sub(monthOut)
			if budgetRemaining.LessThan(available) {
				available = budgetRemaining
			}
		}
	}
	if available.IsNegative() {
		available = decimal.Zero
	}
	return safeSpendPlan{
		SafeToday:   available.Div(decimal.NewFromInt(int64(daysLeft))).Round(2),
		RestOfMonth: available.Round(2),
		DaysLeft:    daysLeft,
	}
}

func obligationCoverage(spend, obligations, taxReserve decimal.Decimal) map[string]interface{} {
	required := obligations.Add(taxReserve)
	coveragePct := decimal.Zero
	if required.IsPositive() {
		coveragePct = spend.Div(required).Mul(decimal.NewFromInt(100))
	}
	status := "covered"
	if spend.LessThan(required) {
		status = "shortfall"
	}
	return map[string]interface{}{
		"status":          status,
		"required":        required.StringFixed(2),
		"available_spend": spend.StringFixed(2),
		"coverage_pct":    coveragePct.StringFixed(1),
		"shortfall":       decimal.Max(required.Sub(spend), decimal.Zero).StringFixed(2),
	}
}

func operatingPlanRiskFlags(profile *entities.FinancialProfile, obligations []entities.FinancialObligation, spend, monthlyIncome, monthNet decimal.Decimal, summary obligationSummary, taxReserve decimal.Decimal) []map[string]interface{} {
	flags := make([]map[string]interface{}, 0)
	add := func(code, severity, message string) {
		flags = append(flags, map[string]interface{}{"code": code, "severity": severity, "message": message})
	}
	if monthlyIncome.IsZero() {
		add("missing_income", "high", "Monthly income is missing, so the plan is based only on this month's observed Rail deposits.")
	}
	if len(obligations) == 0 {
		add("no_manual_obligations", "high", "No manual obligations are saved yet; rent, tax, invoices, payroll, family support, and subscriptions may be missing from the plan.")
	}
	if spend.LessThan(summary.RequiredThisMonth.Add(taxReserve)) {
		add("obligation_shortfall", "critical", "Spend balance does not fully cover this month's obligations plus tax reserve.")
	}
	if profile.EarningCurrency != "" && profile.SpendingCurrency != "" && profile.EarningCurrency != profile.SpendingCurrency {
		add("currency_exposure", "medium", "Earning and spending currencies differ; separate operating cash from savings and avoid forced conversion at bad rates.")
	}
	if profile.ResidenceCountry != "" && profile.TaxCountry != "" && profile.ResidenceCountry != profile.TaxCountry {
		add("cross_border_tax_context", "medium", "Residence country and tax country differ; keep clean records and review with a tax professional.")
	}
	if profile.UserType == "freelancer" {
		if profile.IncomeFrequency == "irregular" || monthNet.IsNegative() {
			add("irregular_income_smoothing", "high", "Income is irregular; safe spend should be based on low-case cash, not the best month.")
		}
		if !summary.HasInvoice {
			add("invoice_tracking_missing", "medium", "No invoice obligations are tracked; add receivables so Miriam can separate expected cash from actual cash.")
		}
		if profile.ResidenceCountry == "NG" && profile.EarningCurrency != "" && profile.EarningCurrency != "NGN" && profile.SpendingCurrency == "NGN" {
			add("ng_freelancer_fx_pressure", "high", "NG freelancer pattern detected: protect USD earnings, plan NGN conversions, and reserve tax before family support and lifestyle spend.")
		}
	}
	if profile.UserType == "founder" && !summary.HasPayroll {
		add("payroll_calendar_missing", "high", "Founder profile has no payroll obligation saved; runway and investor updates need payroll and vendor dates.")
	}
	if profile.UserType == "family" && (!summary.HasEducation || !summary.HasInsurance) {
		add("family_protection_gap", "medium", "Family profile is missing education or insurance obligations; shared plans should include both.")
	}
	if profile.UserType == "high_earner" && taxReserve.IsZero() {
		add("tax_packet_missing", "high", "High earner profile has no tax reserve or tax obligation; prepare a tax packet for professional review.")
	}
	// Debt-aware allocation note: if obligations include debts, suggest 80/20 sprint allocation
	hasDebt := false
	for _, ob := range obligations {
		if ob.Type == entities.ObligationTypeDebt && ob.Status == entities.ObligationStatusActive {
			hasDebt = true
			break
		}
	}
	if hasDebt {
		add("allocation_note", "info", "While debts exist, consider shifting more to debt (e.g. 80/20 spend/debt) instead of the standard 70/30 split.")
	}
	return flags
}

func operatingPlanNextActions(profile *entities.FinancialProfile, safeSpend safeSpendPlan, stashTarget, taxReserve, familyCap decimal.Decimal, summary obligationSummary, flags []map[string]interface{}) []map[string]interface{} {
	actions := make([]map[string]interface{}, 0)
	add := func(actionType, title string, params map[string]interface{}) {
		params["requires_confirmation"] = true
		params["execution"] = "approval_gated_pending_action"
		actions = append(actions, map[string]interface{}{
			"type":        actionType,
			"title":       title,
			"description": actionDescription(actionType, profile),
			"params":      params,
		})
	}
	if safeSpend.RestOfMonth.IsPositive() {
		add("set_budget", "Set this month's spending budget", map[string]interface{}{
			"monthly_limit": safeSpend.RestOfMonth.InexactFloat64(),
			"currency":      coalesceString(profile.SpendingCurrency, "USD"),
		})
	}
	if stashTarget.IsPositive() {
		add("transfer_to_stash", "Move planned savings into Stash", map[string]interface{}{
			"amount":   math.Round(stashTarget.InexactFloat64()*100) / 100,
			"from":     "spend",
			"to":       "stash",
			"currency": "USD",
		})
		add("create_automation", "Automate the monthly Stash move", map[string]interface{}{
			"name":         "Monthly Stash transfer",
			"description":  "Move the approved monthly savings target from Spend to Stash.",
			"trigger_type": "schedule",
			"trigger_config": map[string]interface{}{
				"weekdays": []int{1},
				"hour":     9,
				"timezone": "UTC",
			},
			"action_type": "transfer_to_stash",
			"action_config": map[string]interface{}{
				"amount":                           math.Round(stashTarget.InexactFloat64()*100) / 100,
				"from_wallet":                      "spend",
				"to_wallet":                        "stash",
				"acknowledged_future_transfer":     false,
				"requires_future_transfer_consent": true,
			},
		})
	}
	if summary.RequiredThisMonth.IsPositive() {
		add("create_obligation_reminders", "Create reminders for this month's obligations", map[string]interface{}{
			"obligation_amount": summary.RequiredThisMonth.StringFixed(2),
		})
	}
	if taxReserve.IsPositive() {
		add("reserve_tax", "Ring-fence tax reserve before discretionary spending", map[string]interface{}{
			"amount": taxReserve.StringFixed(2),
		})
	}
	if familyCap.IsPositive() {
		add("cap_family_support", "Set a family-support cap for this month", map[string]interface{}{
			"amount":   familyCap.StringFixed(2),
			"currency": coalesceString(profile.SpendingCurrency, "USD"),
		})
	}
	if len(flags) > 0 {
		add("review_risks", "Review plan risks before executing actions", map[string]interface{}{
			"risk_count": len(flags),
		})
	}
	return actions
}

func actionDescription(actionType string, profile *entities.FinancialProfile) string {
	switch actionType {
	case "set_budget":
		return "Create a budget pending action; Miriam should ask for confirmation before saving it."
	case "transfer_to_stash":
		return "Create a transfer pending action; no money should move until the user confirms."
	case "create_automation":
		return "Create an automation only after the user approves the schedule and amount."
	case "create_obligation_reminders":
		return "Create reminder-style obligation actions so bills, taxes, payroll, and support dates are visible."
	case "reserve_tax":
		if coalesceString(profile.UserType, "individual") == "freelancer" {
			return "For freelancers, reserve tax before lifestyle spending; review final tax treatment with a professional."
		}
		return "Keep tax reserve informational and review with a tax professional."
	case "cap_family_support":
		return "Set a compassionate limit so family support does not quietly break the operating plan."
	default:
		return "Approval-gated action proposal."
	}
}

func (o *AgentAdapter) fxContext(ctx context.Context, profile *entities.FinancialProfile, monthlyIncome, spend, stash decimal.Decimal) map[string]interface{} {
	from := profile.EarningCurrency
	to := profile.SpendingCurrency
	if from == "" || to == "" || from == to {
		return map[string]interface{}{"has_cross_currency": false}
	}
	out := map[string]interface{}{
		"has_cross_currency": true,
		"from_currency":      from,
		"to_currency":        to,
		"rate_available":     false,
		"note":               "Separate operating cash from long-term USD/Stash savings so forced conversions do not drive decisions.",
	}
	if o.currencyRates == nil {
		return out
	}
	rate, err := o.currencyRates.GetLatestRate(ctx, from, to)
	if err != nil || !rate.IsPositive() {
		return out
	}
	out["rate_available"] = true
	out["latest_rate"] = rate.StringFixed(6)
	out["monthly_income_in_spending_currency"] = monthlyIncome.Mul(rate).StringFixed(2)
	out["spend_balance_in_spending_currency"] = spend.Mul(rate).StringFixed(2)
	out["stash_balance_in_spending_currency"] = stash.Mul(rate).StringFixed(2)
	return out
}

func taxPlaybook(profile *entities.FinancialProfile) []string {
	country := coalesceString(profile.TaxCountry, profile.ResidenceCountry)
	base := []string{
		"Keep this as an informational planning reserve, not a tax filing conclusion.",
		"Export deposits, withdrawals, yield, invoices, and deductible expenses before speaking with a professional.",
	}
	switch country {
	case "NG":
		return append([]string{
			"For Nigeria, keep freelance or business income, FX conversions, deductible expenses, and withholding evidence in one packet.",
			"Separate family support from business expenses; do not treat support as a deduction without professional review.",
		}, base...)
	case "US":
		return append([]string{
			"For the US, separate employment income, contractor income, estimated tax records, investment/yield records, and business expenses.",
			"High earners and founders should prepare an accountant packet before quarter-end.",
		}, base...)
	case "GB", "UK":
		return append([]string{
			"For the UK, track self-assessment income, allowable expenses, savings/investment income, and foreign income evidence.",
			"Cross-border users should separate UK tax residency facts from family-support cash flow.",
		}, base...)
	default:
		return base
	}
}

func personaOperatingModel(profile *entities.FinancialProfile, monthlyIncome, total decimal.Decimal, summary obligationSummary) map[string]interface{} {
	model := map[string]interface{}{"persona": coalesceString(profile.UserType, "individual")}
	switch coalesceString(profile.UserType, "individual") {
	case "freelancer":
		model["focus"] = []string{"income smoothing", "invoice collection", "tax reserve", "deductions", "family-support pressure"}
		model["low_case_cash_rule"] = "Base spending on confirmed cash and overdue invoices, not optimistic receivables."
	case "founder":
		burn := profile.MonthlyFixedCosts.Add(summary.ByType[entities.ObligationTypePayroll]).Add(summary.ByType[entities.ObligationTypeVendorBill])
		runway := "unknown"
		if burn.IsPositive() {
			runway = total.Div(burn).StringFixed(1)
		}
		model["focus"] = []string{"runway", "burn", "payroll", "vendor calendar", "investor update draft"}
		model["estimated_runway_months"] = runway
	case "family":
		model["focus"] = []string{"shared budget", "education fund", "insurance reminders", "family contribution plan"}
	case "high_earner":
		model["focus"] = []string{"tax packet", "allocation review", "concentration risk", "estate checklist", "advisor briefing"}
	default:
		model["focus"] = []string{"budgeting", "debt payoff", "emergency fund", "investing risk summary", "tax readiness"}
	}
	if monthlyIncome.IsPositive() {
		model["monthly_income"] = monthlyIncome.StringFixed(2)
	}
	return model
}

func decimalMapStrings(values map[string]decimal.Decimal) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value.StringFixed(2)
	}
	return out
}
