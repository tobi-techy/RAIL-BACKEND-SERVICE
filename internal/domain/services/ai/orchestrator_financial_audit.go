package ai

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"

	aicontext "github.com/rail-service/rail_service/internal/domain/services/ai/context"
	spendingsvc "github.com/rail-service/rail_service/internal/domain/services/spending"


	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
)

const ToolGetFinancialAudit = "get_financial_audit"

var financialAuditPeriods = []string{
	"last_90_days",
	"last_6_months",
	"last_12_months",
	"this_month",
	"last_month",
	"last_7_days",
	"last_30_days",
}

// IsFinancialAuditPeriod reports whether a period is accepted by the audit tool.
func IsFinancialAuditPeriod(period string) bool {
	normalized := strings.ToLower(strings.TrimSpace(period))
	for _, allowed := range financialAuditPeriods {
		if normalized == allowed {
			return true
		}
	}
	return false
}

// FinancialAuditTool returns the deterministic audit tool Miriam uses for
// opt-in accountability sessions.
func FinancialAuditTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolGetFinancialAudit,
		Description: "Run Miriam's opt-in financial audit: scores cash flow, spending control, stash discipline, obligation coverage, goal alignment, top leaks, contradictions, and recommended next actions. Use when the user says audit me, hard mode, roast my finances, reality check, no sugarcoating, financial audit, or wants a Caleb-style accountability session. This is read-only; suggested money moves still require confirmation.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"period": map[string]interface{}{
					"type":        "string",
					"enum":        financialAuditPeriods,
					"description": "Time period to audit. Default to last_90_days for a real financial audit; use last_6_months or last_12_months when the user asks for a deeper audit.",
				},
				"intensity": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"gentle", "direct", "hard"},
					"description": "Delivery intensity. hard is opt-in accountability, not humiliation.",
				},
				"depth": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"summary", "standard", "deep"},
					"description": "Level of detail. summary = key metrics only, standard = default, deep = everything including monthly trends and merchant detail.",
					"default":     "standard",
				},
			},
			"required":             []string{},
			"additionalProperties": false,
		},
	}
}

func (o *AgentAdapter) executeFinancialAudit(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.spending == nil || o.aggregateStats == nil {
		return map[string]interface{}{"error": "financial audit service is unavailable: spending and balance providers are not configured"}, nil
	}

	period := normalizeAuditPeriod(auditStringArg(args, "period", "last_90_days"))
	intensity := normalizeAuditIntensity(auditStringArg(args, "intensity", "direct"))
	depth := normalizeAuditDepth(auditStringArg(args, "depth", "standard"))
	start, end := parsePeriod(period)
	now := time.Now().UTC()
	if period == "this_month" {
		end = now
	}

	spend, stash, totalBalance := o.currentBalances(ctx, userID)
	flow, err := o.spending.GetMoneyFlow(ctx, userID, start, end)
	if err != nil {
		return nil, fmt.Errorf("audit money flow: %w", err)
	}
	if flow == nil {
		flow = &entities.MoneyFlowSummary{}
	}
	summary, err := o.spending.GetSummary(ctx, userID, start, end)
	if err != nil {
		return nil, fmt.Errorf("audit spending summary: %w", err)
	}
	if summary == nil {
		summary = &spendingsvc.Summary{}
	}

	totalDigitalOut := flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P)
	totalOut := totalDigitalOut.Add(flow.TotalReceipts)
	netFlow := flow.TotalDeposits.Sub(totalOut)
	savingsRate := percentOf(netFlow, flow.TotalDeposits)
	stashRatio := percentOf(stash, totalBalance)
	observedMonths := auditObservedMonths(start, end)
	monthlyOut := totalOut.Div(observedMonths)
	monthlyNetFlow := netFlow.Div(observedMonths)
	monthlyDeposits := flow.TotalDeposits.Div(observedMonths)

	topCategories := auditTopCategories(summary.Categories, 8)
	topMerchants := auditTopMerchants(summary.Merchants, 8)

	// Enrich top merchants with plain descriptions and context
	if enrichmentMap := aicontext.EnrichMerchantMap(ctx, o.merchantEnricher, userID); enrichmentMap != nil {
		for _, m := range topMerchants {
			aicontext.EnrichMerchantEntry(m, enrichmentMap)
		}
	}

	biggestLeak := auditBiggestLeak(topCategories, totalOut)
	monthlyTrend := o.auditMonthlyTrend(ctx, userID, start, end)
	dataCoverage := auditDataCoverage(summary, monthlyTrend, start, end)

	profileData, profile := o.auditProfile(ctx, userID)
	obligationData, obligationRequired, obligationWarnings := o.auditObligations(ctx, userID, now)
	budgetData := o.auditBudget(ctx, userID, monthlyOut, now)
	recurringData, recurringMonthly, recurringWarnings := o.auditRecurring(ctx, userID)

	score := auditScore{
		CashFlow:           scoreCashFlow(netFlow, flow.TotalDeposits),
		SpendingControl:    scoreSpendingControl(budgetData, monthlyOut, now),
		StashDiscipline:    scoreStashDiscipline(stashRatio),
		ObligationCoverage: scoreObligationCoverage(spend, obligationRequired, o.obligations != nil),
		GoalAlignment:      scoreGoalAlignment(profile, monthlyNetFlow),
	}
	score.Total = clampScore(score.CashFlow+score.SpendingControl+score.StashDiscipline+score.ObligationCoverage+score.GoalAlignment, 0, 100)

	contradictions := auditContradictions(profile, flow, totalOut, netFlow, monthlyDeposits, monthlyNetFlow, topCategories, budgetData, obligationRequired, spend, recurringMonthly)
	riskFlags := auditRiskFlags(flow, totalOut, netFlow, spend, stash, obligationRequired, recurringMonthly, budgetData)
	nextActions := auditNextActions(spend, monthlyDeposits, netFlow, stashRatio, budgetData, obligationRequired)

	warnings := append(obligationWarnings, recurringWarnings...)

	result := map[string]interface{}{
		"audit_mode": true,
		"period": map[string]interface{}{
			"key":   period,
			"label": periodToLabel(period, start, end),
			"start": start.Format("2006-01-02"),
			"end":   end.Format("2006-01-02"),
		},
		"intensity":         intensity,
		"delivery_contract": auditDeliveryContract(intensity),
		"score": map[string]interface{}{
			"total":               score.Total,
			"status":              auditStatus(score.Total),
			"cash_flow":           score.CashFlow,
			"spending_control":    score.SpendingControl,
			"stash_discipline":    score.StashDiscipline,
			"obligation_coverage": score.ObligationCoverage,
			"goal_alignment":      score.GoalAlignment,
		},
		"snapshot": map[string]interface{}{
			"spend_balance":       spend.StringFixed(2),
			"stash_balance":       stash.StringFixed(2),
			"total_balance":       totalBalance.StringFixed(2),
			"money_in":            flow.TotalDeposits.StringFixed(2),
			"digital_money_out":   totalDigitalOut.StringFixed(2),
			"receipt_cash_out":    flow.TotalReceipts.StringFixed(2),
			"total_money_out":     totalOut.StringFixed(2),
			"net_flow":            netFlow.StringFixed(2),
			"average_monthly_in":  monthlyDeposits.StringFixed(2),
			"average_monthly_out": monthlyOut.StringFixed(2),
			"average_monthly_net": monthlyNetFlow.StringFixed(2),
			"savings_rate_pct":    savingsRate.StringFixed(1),
			"stash_ratio_pct":     stashRatio.StringFixed(1),
			"transaction_count":   summary.TxCount,
			"daily_average_spend": summary.DailyAvg.StringFixed(2),
		},
		"the_damage": map[string]interface{}{
			"money_in":      flow.TotalDeposits.StringFixed(2),
			"money_out":     totalOut.StringFixed(2),
			"net_flow":      netFlow.StringFixed(2),
			"biggest_leak":  biggestLeak,
			"primary_issue": auditPrimaryIssue(netFlow, totalOut, obligationRequired, spend, budgetData),
		},
		"do_this_today": nextActions,
		"data_used": []string{
			"current_balances",
			"multi_month_money_flow",
			"multi_month_spending_summary",
			"monthly_trend",
			"budget_if_present",
			"financial_profile_if_present",
			"manual_obligations_if_present",
			"recurring_expenses_if_present",
		},
	}

	// For "summary" depth, return only the core fields above
	if depth != "summary" {
		result["the_pattern"] = auditPatterns(flow, totalOut, netFlow, topCategories, recurringMonthly, stashRatio)
		result["budget"] = budgetData
		result["profile"] = profileData
		result["warnings"] = warnings
	}

	// "standard" and "deep" get additional detail
	if depth == "standard" || depth == "deep" {
		result["monthly_trend"] = monthlyTrend
		result["contradictions"] = contradictions
		result["top_spending_categories"] = topCategories
		result["top_merchants"] = topMerchants
		result["obligations"] = obligationData
		result["recurring"] = recurringData
		result["risk_flags"] = riskFlags
		result["data_coverage"] = dataCoverage
	}

	return result, nil
}

type auditScore struct {
	Total              int
	CashFlow           int
	SpendingControl    int
	StashDiscipline    int
	ObligationCoverage int
	GoalAlignment      int
}

func auditStringArg(args map[string]interface{}, key, fallback string) string {
	if args == nil {
		return fallback
	}
	if v, ok := args[key].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}

func normalizeAuditPeriod(value string) string {
	period := strings.ToLower(strings.TrimSpace(value))
	for _, allowed := range financialAuditPeriods {
		if period == allowed {
			return period
		}
	}
	return "last_90_days"
}

func normalizeAuditIntensity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "gentle", "hard":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "direct"
	}
}

func normalizeAuditDepth(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "summary", "deep":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "standard"
	}
}

func buildToneModeContext(mode string) string {
	switch normalizeAuditIntensity(mode) {
	case "gentle":
		return "PRODUCT TONE MODE: gentle. Be supportive, concrete, and brief. Keep accountability, but soften the edge."
	case "hard":
		return "PRODUCT TONE MODE: hard. The user opted into blunt accountability. Be direct and witty about financial patterns, but never humiliate, name-call, or imitate a specific creator. Tie every hard line to exact tool evidence."
	default:
		if strings.TrimSpace(mode) == "" {
			return ""
		}
		return "PRODUCT TONE MODE: direct. Be numbers-first, warm, and plainspoken."
	}
}

func auditDeliveryContract(intensity string) map[string]interface{} {
	contract := map[string]interface{}{
		"attack_patterns_not_person": true,
		"must_use_exact_numbers":     true,
		"no_humiliation":             true,
		"no_emojis":                  true,
	}
	switch intensity {
	case "gentle":
		contract["tone"] = "supportive, clear, and practical"
		contract["segment_labels"] = []string{"What happened", "The pattern", "The fix", "Do this today"}
	case "hard":
		contract["tone"] = "blunt accountability with dry humor; no cruelty, no name-calling"
		contract["segment_labels"] = []string{"The Damage", "The Pattern", "The Excuse I'm Not Buying", "The Fix", "Do This Today"}
	default:
		contract["tone"] = "direct, warm, numbers-first accountability"
		contract["segment_labels"] = []string{"The Damage", "The Pattern", "The Fix", "Do This Today"}
	}
	return contract
}

func percentOf(part, total decimal.Decimal) decimal.Decimal {
	if total.IsZero() {
		return decimal.Zero
	}
	return part.Div(total).Mul(decimal.NewFromInt(100))
}

func auditTopCategories(categories []entities.SpendingByCategory, limit int) []map[string]interface{} {
	copied := append([]entities.SpendingByCategory(nil), categories...)
	sort.SliceStable(copied, func(i, j int) bool {
		return copied[i].Total.GreaterThan(copied[j].Total)
	})
	if len(copied) > limit {
		copied = copied[:limit]
	}
	items := make([]map[string]interface{}, 0, len(copied))
	for _, c := range copied {
		items = append(items, map[string]interface{}{
			"category":     humanizeCategory(c.Category),
			"raw_category": c.Category,
			"total":        c.Total.StringFixed(2),
			"count":        c.Count,
		})
	}
	return items
}

func auditTopMerchants(merchants []entities.SpendingByMerchant, limit int) []map[string]interface{} {
	copied := append([]entities.SpendingByMerchant(nil), merchants...)
	sort.SliceStable(copied, func(i, j int) bool {
		return copied[i].Total.GreaterThan(copied[j].Total)
	})
	if len(copied) > limit {
		copied = copied[:limit]
	}
	items := make([]map[string]interface{}, 0, len(copied))
	for _, m := range copied {
		items = append(items, map[string]interface{}{
			"merchant": truncateMerchant(m.Merchant),
			"total":    m.Total.StringFixed(2),
			"count":    m.Count,
		})
	}
	return items
}

func auditBiggestLeak(topCategories []map[string]interface{}, totalOut decimal.Decimal) map[string]interface{} {
	if len(topCategories) == 0 {
		return map[string]interface{}{"found": false}
	}
	total, _ := decimal.NewFromString(fmt.Sprint(topCategories[0]["total"]))
	return map[string]interface{}{
		"found":        true,
		"category":     topCategories[0]["category"],
		"amount":       total.StringFixed(2),
		"share_of_out": percentOf(total, totalOut).StringFixed(1),
	}
}

func (o *AgentAdapter) auditMonthlyTrend(ctx context.Context, userID uuid.UUID, start, end time.Time) []map[string]interface{} {
	if o.spending == nil {
		return nil
	}
	now := time.Now().UTC()
	if end.After(now) {
		end = now
	}
	if !end.After(start) {
		return nil
	}

	monthStart := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
	items := make([]map[string]interface{}, 0, 12)
	for cursor := monthStart; cursor.Before(end) && len(items) < 12; cursor = cursor.AddDate(0, 1, 0) {
		chunkStart := cursor
		if chunkStart.Before(start) {
			chunkStart = start
		}
		chunkEnd := cursor.AddDate(0, 1, 0)
		if chunkEnd.After(end) {
			chunkEnd = end
		}
		if !chunkEnd.After(chunkStart) {
			continue
		}

		flow, err := o.spending.GetMoneyFlow(ctx, userID, chunkStart, chunkEnd)
		if err != nil || flow == nil {
			continue
		}
		moneyOut := auditTotalOut(flow)
		items = append(items, map[string]interface{}{
			"month":             cursor.Format("2006-01"),
			"label":             cursor.Format("Jan 2006"),
			"start":             chunkStart.Format("2006-01-02"),
			"end":               chunkEnd.Format("2006-01-02"),
			"money_in":          flow.TotalDeposits.StringFixed(2),
			"money_out":         moneyOut.StringFixed(2),
			"net_flow":          flow.TotalDeposits.Sub(moneyOut).StringFixed(2),
			"deposit_count":     flow.DepositCount,
			"outflow_count":     flow.WithdrawalCount + flow.CardSpendCount + flow.P2PCount + flow.ReceiptCount,
			"card_spend":        flow.TotalCardSpend.StringFixed(2),
			"withdrawals":       flow.TotalWithdrawals.StringFixed(2),
			"p2p":               flow.TotalP2P.StringFixed(2),
			"receipt_cash_out":  flow.TotalReceipts.StringFixed(2),
			"savings_rate_pct":  percentOf(flow.TotalDeposits.Sub(moneyOut), flow.TotalDeposits).StringFixed(1),
			"money_out_per_100": percentOf(moneyOut, flow.TotalDeposits).StringFixed(2),
		})
	}
	return items
}

func auditDataCoverage(summary *spendingsvc.Summary, monthlyTrend []map[string]interface{}, start, end time.Time) map[string]interface{} {
	periodDays := int(end.Sub(start).Hours()/24) + 1
	if periodDays < 1 {
		periodDays = 1
	}
	txCount := 0
	dailyAvg := decimal.Zero
	if summary != nil {
		txCount = summary.TxCount
		dailyAvg = summary.DailyAvg
		if summary.PeriodDays > 0 {
			periodDays = summary.PeriodDays
		}
	}

	totalIn := decimal.Zero
	totalOut := decimal.Zero
	activeMonths := 0
	peakMonth := map[string]interface{}{"found": false}
	peakOut := decimal.Zero
	for _, month := range monthlyTrend {
		in := decimalFromAuditMap(month, "money_in")
		out := decimalFromAuditMap(month, "money_out")
		if in.IsPositive() || out.IsPositive() {
			activeMonths++
		}
		totalIn = totalIn.Add(in)
		totalOut = totalOut.Add(out)
		if out.GreaterThan(peakOut) {
			peakOut = out
			peakMonth = map[string]interface{}{
				"found":     true,
				"label":     month["label"],
				"money_out": out.StringFixed(2),
			}
		}
	}

	monthCount := len(monthlyTrend)
	divisor := decimal.NewFromInt(int64(maxInt(1, monthCount)))
	return map[string]interface{}{
		"period_days":               periodDays,
		"months_analyzed":           monthCount,
		"active_months":             activeMonths,
		"transaction_count":         txCount,
		"daily_average_spend":       dailyAvg.StringFixed(2),
		"average_monthly_money_in":  totalIn.Div(divisor).StringFixed(2),
		"average_monthly_money_out": totalOut.Div(divisor).StringFixed(2),
		"peak_spend_month":          peakMonth,
		"has_3_month_window":        periodDays >= 89,
		"has_6_month_window":        periodDays >= 180,
		"has_12_month_window":       periodDays >= 365,
	}
}

func auditObservedMonths(start, end time.Time) decimal.Decimal {
	if !end.After(start) {
		return decimal.NewFromInt(1)
	}
	months := decimal.NewFromFloat(end.Sub(start).Hours() / 24 / 30.4375)
	if months.LessThan(decimal.NewFromInt(1)) {
		return decimal.NewFromInt(1)
	}
	return months
}

func auditTotalOut(flow *entities.MoneyFlowSummary) decimal.Decimal {
	if flow == nil {
		return decimal.Zero
	}
	return flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P).Add(flow.TotalReceipts)
}

func decimalFromAuditMap(data map[string]interface{}, key string) decimal.Decimal {
	if data == nil {
		return decimal.Zero
	}
	value, err := decimal.NewFromString(fmt.Sprint(data[key]))
	if err != nil {
		return decimal.Zero
	}
	return value
}

func (o *AgentAdapter) auditProfile(ctx context.Context, userID uuid.UUID) (map[string]interface{}, *entities.FinancialProfile) {
	if o.financialProfile == nil {
		return map[string]interface{}{"has_profile": false}, nil
	}
	profile, err := o.financialProfile.GetByUserID(ctx, userID)
	if err != nil || profile == nil {
		return map[string]interface{}{"has_profile": false}, nil
	}
	return map[string]interface{}{
		"has_profile":            true,
		"user_type":              coalesceString(profile.UserType, "individual"),
		"primary_currency":       profile.PrimaryCurrency,
		"income_frequency":       profile.IncomeFrequency,
		"monthly_income":         profile.MonthlyIncome.StringFixed(2),
		"monthly_fixed_costs":    profile.MonthlyFixedCosts.StringFixed(2),
		"monthly_savings_target": profile.MonthlySavingsTarget.StringFixed(2),
		"emergency_fund_target":  profile.EmergencyFundTarget.StringFixed(2),
		"financial_goal":         profile.FinancialGoal,
	}, profile
}

func (o *AgentAdapter) auditObligations(ctx context.Context, userID uuid.UUID, now time.Time) (map[string]interface{}, decimal.Decimal, []string) {
	if o.obligations == nil {
		return map[string]interface{}{"available": false, "message": "No manual obligations provider configured."}, decimal.Zero, []string{"Manual obligations were not included; rent, tax, family support, payroll, and subscriptions may be missing."}
	}
	obligations, err := o.obligations.ListActive(ctx, userID)
	if err != nil {
		return map[string]interface{}{"available": false, "error": "temporary_unavailable"}, decimal.Zero, []string{"Manual obligations could not be loaded for this audit."}
	}
	summary := summarizeObligations(obligations, now)
	return map[string]interface{}{
		"available":            true,
		"count":                len(obligations),
		"required_this_month":  summary.RequiredThisMonth.StringFixed(2),
		"critical_this_month":  summary.CriticalThisMonth.StringFixed(2),
		"by_type":              decimalMapStrings(summary.ByType),
		"upcoming":             summary.Upcoming,
		"invoice_aging":        summary.InvoiceAging,
		"missing_data_warning": len(obligations) == 0,
	}, summary.RequiredThisMonth, nil
}

func (o *AgentAdapter) auditBudget(ctx context.Context, userID uuid.UUID, monthlyOut decimal.Decimal, now time.Time) map[string]interface{} {
	if o.budgetProvider == nil {
		return map[string]interface{}{"has_budget": false}
	}
	budget, err := o.budgetProvider.GetByUserID(ctx, userID)
	if err != nil || budget == nil || !budget.MonthlyLimit.IsPositive() {
		return map[string]interface{}{"has_budget": false}
	}
	remaining := budget.MonthlyLimit.Sub(monthlyOut)
	usedPct := percentOf(monthlyOut, budget.MonthlyLimit)
	daysElapsed := decimal.NewFromInt(int64(maxInt(1, now.Day())))
	daysInMonth := decimal.NewFromInt(int64(time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()))
	expectedPct := daysElapsed.Div(daysInMonth).Mul(decimal.NewFromInt(100))
	status := "on_track"
	if usedPct.GreaterThan(expectedPct.Add(decimal.NewFromInt(20))) {
		status = "running_hot"
	}
	if usedPct.GreaterThan(decimal.NewFromInt(90)) {
		status = "near_limit"
	}
	if remaining.IsNegative() {
		status = "over_budget"
	}
	return map[string]interface{}{
		"has_budget":       true,
		"monthly_limit":    budget.MonthlyLimit.StringFixed(2),
		"spent_so_far":     monthlyOut.StringFixed(2),
		"basis":            "average_monthly_spend",
		"remaining":        remaining.StringFixed(2),
		"used_pct":         usedPct.StringFixed(1),
		"expected_pct_now": expectedPct.StringFixed(1),
		"status":           status,
	}
}

func (o *AgentAdapter) auditRecurring(ctx context.Context, userID uuid.UUID) (map[string]interface{}, decimal.Decimal, []string) {
	if o.recurringDetector == nil {
		return map[string]interface{}{"available": false}, decimal.Zero, nil
	}
	expenses, err := o.recurringDetector.DetectRecurring(ctx, userID)
	if err != nil {
		return map[string]interface{}{"available": false, "error": "temporary_unavailable"}, decimal.Zero, []string{"Recurring expenses could not be loaded for this audit."}
	}
	totalMonthly := decimal.Zero
	items := make([]map[string]interface{}, 0, len(expenses))
	for _, e := range expenses {
		monthly := e.AvgAmount
		if e.Frequency == "weekly" {
			monthly = e.AvgAmount.Mul(decimal.NewFromFloat(4.33))
		}
		totalMonthly = totalMonthly.Add(monthly)
		items = append(items, map[string]interface{}{
			"merchant":           e.Merchant,
			"frequency":          e.Frequency,
			"avg_amount":         e.AvgAmount.StringFixed(2),
			"monthly_equivalent": monthly.StringFixed(2),
			"observed_total":     e.Total.StringFixed(2),
			"transaction_count":  e.Count,
			"first_seen":         e.FirstSeen,
			"last_seen":          e.LastSeen,
		})
	}
	return map[string]interface{}{
		"available":               true,
		"count":                   len(items),
		"total_recurring_monthly": totalMonthly.StringFixed(2),
		"items":                   items,
	}, totalMonthly, nil
}

func scoreCashFlow(netFlow, income decimal.Decimal) int {
	if !income.IsPositive() {
		return 8
	}
	rate := percentOf(netFlow, income)
	switch {
	case rate.GreaterThanOrEqual(decimal.NewFromInt(25)):
		return 30
	case rate.GreaterThanOrEqual(decimal.NewFromInt(10)):
		return 22
	case rate.GreaterThanOrEqual(decimal.Zero):
		return 14
	default:
		return 4
	}
}

func scoreSpendingControl(budget map[string]interface{}, totalOut decimal.Decimal, now time.Time) int {
	if hasBudget, _ := budget["has_budget"].(bool); !hasBudget {
		if totalOut.IsZero() {
			return 18
		}
		return 12
	}
	status, _ := budget["status"].(string)
	switch status {
	case "on_track":
		return 20
	case "running_hot":
		return 12
	case "near_limit":
		return 7
	case "over_budget":
		return 2
	default:
		return 10
	}
}

func scoreStashDiscipline(stashRatio decimal.Decimal) int {
	switch {
	case stashRatio.GreaterThanOrEqual(decimal.NewFromInt(30)):
		return 20
	case stashRatio.GreaterThanOrEqual(decimal.NewFromInt(15)):
		return 14
	case stashRatio.GreaterThanOrEqual(decimal.NewFromInt(5)):
		return 8
	default:
		return 2
	}
}

func scoreObligationCoverage(spend, required decimal.Decimal, hasProvider bool) int {
	if !hasProvider {
		return 8
	}
	if required.IsZero() {
		return 10
	}
	if spend.GreaterThanOrEqual(required) {
		return 15
	}
	return 3
}

func scoreGoalAlignment(profile *entities.FinancialProfile, netFlow decimal.Decimal) int {
	if profile == nil || !profile.MonthlySavingsTarget.IsPositive() {
		return 8
	}
	switch {
	case netFlow.GreaterThanOrEqual(profile.MonthlySavingsTarget):
		return 15
	case netFlow.GreaterThanOrEqual(profile.MonthlySavingsTarget.Mul(decimal.NewFromFloat(0.5))):
		return 9
	case netFlow.IsPositive():
		return 5
	default:
		return 1
	}
}

func auditStatus(score int) string {
	switch {
	case score >= 80:
		return "strong"
	case score >= 65:
		return "stable"
	case score >= 45:
		return "messy_but_fixable"
	default:
		return "needs_intervention"
	}
}

func auditContradictions(profile *entities.FinancialProfile, flow *entities.MoneyFlowSummary, totalOut, netFlow, monthlyDeposits, monthlyNetFlow decimal.Decimal, topCategories []map[string]interface{}, budget map[string]interface{}, obligations, spend, recurringMonthly decimal.Decimal) []map[string]interface{} {
	items := make([]map[string]interface{}, 0)
	add := func(code, claim, reality, take string, evidence map[string]interface{}) {
		items = append(items, map[string]interface{}{
			"code":     code,
			"claim":    claim,
			"reality":  reality,
			"take":     take,
			"evidence": evidence,
		})
	}

	if flow.TotalDeposits.IsPositive() && totalOut.GreaterThan(flow.TotalDeposits) {
		add("outflow_beats_income", "Money is coming in.", fmt.Sprintf("Money out is $%s against $%s in deposits.", totalOut.StringFixed(2), flow.TotalDeposits.StringFixed(2)), "Income is not the whole problem. Containment is.", map[string]interface{}{
			"money_in":  flow.TotalDeposits.StringFixed(2),
			"money_out": totalOut.StringFixed(2),
			"net_flow":  netFlow.StringFixed(2),
		})
	}
	if profile != nil && profile.MonthlySavingsTarget.IsPositive() && monthlyNetFlow.LessThan(profile.MonthlySavingsTarget) {
		goal := "Monthly savings target"
		if strings.TrimSpace(profile.FinancialGoal) != "" {
			goal = profile.FinancialGoal
		}
		add("goal_gap", goal, fmt.Sprintf("Average monthly net flow is $%s against a $%s monthly savings target.", monthlyNetFlow.StringFixed(2), profile.MonthlySavingsTarget.StringFixed(2)), "The goal is not impossible. The current pace is just not funding it.", map[string]interface{}{
			"average_monthly_net":    monthlyNetFlow.StringFixed(2),
			"period_net_flow":        netFlow.StringFixed(2),
			"monthly_savings_target": profile.MonthlySavingsTarget.StringFixed(2),
		})
	}
	if len(topCategories) > 0 {
		total, _ := decimal.NewFromString(fmt.Sprint(topCategories[0]["total"]))
		if totalOut.IsPositive() && percentOf(total, totalOut).GreaterThan(decimal.NewFromInt(30)) {
			add("category_dominates", "This is just normal spending.", fmt.Sprintf("%s alone is $%s, which is %s%% of outflow.", topCategories[0]["category"], total.StringFixed(2), percentOf(total, totalOut).StringFixed(1)), "One category is driving the story. Fix that first.", map[string]interface{}{
				"category":     topCategories[0]["category"],
				"amount":       total.StringFixed(2),
				"share_of_out": percentOf(total, totalOut).StringFixed(1),
			})
		}
	}
	if hasBudget, _ := budget["has_budget"].(bool); hasBudget {
		status, _ := budget["status"].(string)
		if status == "running_hot" || status == "near_limit" || status == "over_budget" {
			add("budget_mismatch", "The budget is under control.", fmt.Sprintf("Budget status is %s with %s%% used.", status, budget["used_pct"]), "A budget that gets ignored is decoration.", map[string]interface{}{
				"budget_status": status,
				"used_pct":      budget["used_pct"],
				"remaining":     budget["remaining"],
			})
		}
	}
	if obligations.IsPositive() && spend.LessThan(obligations) {
		add("obligation_shortfall", "Bills are covered.", fmt.Sprintf("Spend balance is $%s against $%s due this month.", spend.StringFixed(2), obligations.StringFixed(2)), "Do not freestyle with money already spoken for.", map[string]interface{}{
			"spend_balance":        spend.StringFixed(2),
			"required_this_month":  obligations.StringFixed(2),
			"obligation_shortfall": obligations.Sub(spend).StringFixed(2),
		})
	}
	if recurringMonthly.IsPositive() && monthlyDeposits.IsPositive() && percentOf(recurringMonthly, monthlyDeposits).GreaterThan(decimal.NewFromInt(20)) {
		add("recurring_drag", "Subscriptions are small.", fmt.Sprintf("Recurring spend is about $%s/month, %s%% of average monthly deposits.", recurringMonthly.StringFixed(2), percentOf(recurringMonthly, monthlyDeposits).StringFixed(1)), "Small automatic charges become a salary leak when nobody audits them.", map[string]interface{}{
			"recurring_monthly": recurringMonthly.StringFixed(2),
			"income_share_pct":  percentOf(recurringMonthly, monthlyDeposits).StringFixed(1),
		})
	}
	return items
}

func auditRiskFlags(flow *entities.MoneyFlowSummary, totalOut, netFlow, spend, stash, obligations, recurringMonthly decimal.Decimal, budget map[string]interface{}) []map[string]interface{} {
	flags := make([]map[string]interface{}, 0)
	add := func(code, severity, title, evidence string) {
		flags = append(flags, map[string]interface{}{"code": code, "severity": severity, "title": title, "evidence": evidence})
	}
	if flow.TotalDeposits.IsZero() {
		add("no_income_seen", "high", "No completed deposits in period", "Audit cannot calculate a real savings rate without observed money in.")
	}
	if netFlow.IsNegative() {
		add("negative_cashflow", "critical", "Outflow exceeds income", fmt.Sprintf("Net flow is $%s.", netFlow.StringFixed(2)))
	}
	if spend.LessThan(decimal.NewFromInt(50)) {
		add("thin_spend_balance", "high", "Spend balance is thin", fmt.Sprintf("Spend balance is $%s.", spend.StringFixed(2)))
	}
	if obligations.IsPositive() && spend.LessThan(obligations) {
		add("obligation_shortfall", "critical", "Obligations exceed Spend", fmt.Sprintf("$%s due against $%s in Spend.", obligations.StringFixed(2), spend.StringFixed(2)))
	}
	if stash.LessThan(spend.Mul(decimal.NewFromFloat(0.10))) && spend.GreaterThan(decimal.NewFromInt(100)) {
		add("low_stash_ratio", "medium", "Stash is not carrying enough weight", fmt.Sprintf("Stash is $%s while Spend is $%s.", stash.StringFixed(2), spend.StringFixed(2)))
	}
	if recurringMonthly.IsPositive() && spend.IsPositive() && recurringMonthly.GreaterThan(spend) {
		add("recurring_over_spend", "high", "Recurring bills can drain Spend", fmt.Sprintf("Recurring monthly spend is $%s against $%s in Spend.", recurringMonthly.StringFixed(2), spend.StringFixed(2)))
	}
	if status, _ := budget["status"].(string); status == "over_budget" {
		add("over_budget", "high", "Budget is already broken", fmt.Sprintf("Budget remaining is %v.", budget["remaining"]))
	}
	if totalOut.IsZero() && flow.TotalDeposits.IsZero() {
		add("low_data", "medium", "Not enough activity to audit hard", "No completed deposits or outflows were found for this period.")
	}
	return flags
}

func auditPatterns(flow *entities.MoneyFlowSummary, totalOut, netFlow decimal.Decimal, topCategories []map[string]interface{}, recurringMonthly, stashRatio decimal.Decimal) []string {
	patterns := make([]string, 0)
	if flow.TotalDeposits.IsPositive() {
		patterns = append(patterns, fmt.Sprintf("For every $100 in, $%s left.", totalOut.Div(flow.TotalDeposits).Mul(decimal.NewFromInt(100)).StringFixed(2)))
	}
	if netFlow.IsNegative() {
		patterns = append(patterns, fmt.Sprintf("The period is negative by $%s.", netFlow.Abs().StringFixed(2)))
	}
	if len(topCategories) > 0 {
		patterns = append(patterns, fmt.Sprintf("The loudest category is %v at $%v.", topCategories[0]["category"], topCategories[0]["total"]))
	}
	if recurringMonthly.IsPositive() {
		patterns = append(patterns, fmt.Sprintf("Recurring expenses are about $%s/month before you make new choices.", recurringMonthly.StringFixed(2)))
	}
	if stashRatio.LessThan(decimal.NewFromInt(10)) {
		patterns = append(patterns, fmt.Sprintf("Only %s%% of current Rail balance is in Stash.", stashRatio.StringFixed(1)))
	}
	if len(patterns) == 0 {
		patterns = append(patterns, "No obvious leak found in this period. Keep the audit habit weekly.")
	}
	return patterns
}

func auditPrimaryIssue(netFlow, totalOut, obligations, spend decimal.Decimal, budget map[string]interface{}) string {
	if obligations.IsPositive() && spend.LessThan(obligations) {
		return "Obligations are not fully covered by Spend."
	}
	if netFlow.IsNegative() {
		return "You spent more than came in."
	}
	if status, _ := budget["status"].(string); status == "over_budget" || status == "running_hot" || status == "near_limit" {
		return "Budget pace is ahead of where it should be."
	}
	if totalOut.IsZero() {
		return "Not enough outflow data to find a leak."
	}
	return "No crisis, but the audit still found places to tighten."
}

func auditNextActions(spend, monthlyDeposits, netFlow, stashRatio decimal.Decimal, budget map[string]interface{}, obligations decimal.Decimal) []map[string]interface{} {
	actions := make([]map[string]interface{}, 0)
	add := func(title, rationale, tool string, params map[string]interface{}, requiresConfirmation bool) {
		actions = append(actions, map[string]interface{}{
			"title":                 title,
			"rationale":             rationale,
			"tool":                  tool,
			"params":                params,
			"requires_confirmation": requiresConfirmation,
		})
	}
	if hasBudget, _ := budget["has_budget"].(bool); !hasBudget && monthlyDeposits.IsPositive() {
		limit := monthlyDeposits.Mul(decimal.NewFromFloat(0.70)).Round(2)
		add("Set a monthly spending ceiling", fmt.Sprintf("Average monthly deposits are $%s; a $%s spending ceiling keeps the 70/30 Rail habit honest.", monthlyDeposits.StringFixed(2), limit.StringFixed(2)), ToolSetBudget, map[string]interface{}{"monthly_limit": limit.InexactFloat64()}, true)
	}
	if stashRatio.LessThan(decimal.NewFromInt(20)) && spend.GreaterThan(decimal.NewFromInt(30)) {
		amount := spend.Mul(decimal.NewFromFloat(0.10)).Round(2)
		if amount.GreaterThan(decimal.NewFromInt(100)) {
			amount = decimal.NewFromInt(100)
		}
		if amount.GreaterThan(decimal.NewFromInt(5)) {
			add("Move a small amount to Stash", fmt.Sprintf("Stash is only %s%% of Rail balance.", stashRatio.StringFixed(1)), ToolTransferFunds, map[string]interface{}{"from": "spend", "to": "stash", "amount": amount.InexactFloat64()}, true)
		}
	}
	if netFlow.IsNegative() {
		add("Freeze one discretionary category for 7 days", "The fastest fix is stopping one leak, not rewriting your whole life.", "", map[string]interface{}{"duration_days": 7}, false)
	}
	if obligations.IsZero() {
		add("Add rent, bills, tax, and family support obligations", "Miriam cannot protect money she does not know is already spoken for.", ToolCreateObligationReminder, map[string]interface{}{}, true)
	}
	if len(actions) == 0 {
		add("Repeat this audit weekly", "The numbers are stable enough; the habit is what keeps them that way.", "", map[string]interface{}{"cadence": "weekly"}, false)
	}
	return actions
}
