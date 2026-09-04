package miriam

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// PredictionRepository persists predictions for a user.
type PredictionRepository interface {
	UpsertPrediction(ctx context.Context, p *entities.MiriamPrediction) error
	ListActivePredictions(ctx context.Context, userID uuid.UUID) ([]entities.MiriamPrediction, error)
	ExpireOldPredictions(ctx context.Context, before time.Time) (int64, error)
}

// PredictiveEngine generates forward-looking financial predictions.
type PredictiveEngine struct {
	repo        PredictionRepository
	spending    SpendingProvider
	obligations ObligationProvider
	balances    BalanceProvider
	profiles    FinancialProfileProvider
	logger      *zap.Logger
}

// NewPredictiveEngine creates a predictive engine.
func NewPredictiveEngine(
	repo PredictionRepository,
	spending SpendingProvider,
	obligations ObligationProvider,
	balances BalanceProvider,
	profiles FinancialProfileProvider,
	logger *zap.Logger,
) *PredictiveEngine {
	return &PredictiveEngine{
		repo: repo, spending: spending, obligations: obligations,
		balances: balances, profiles: profiles, logger: logger,
	}
}

// GeneratePredictions is the main entry point. It produces a summary of all
// active predictions for a user with a composite risk score.
func (e *PredictiveEngine) GeneratePredictions(ctx context.Context, userID uuid.UUID, state *entities.MiriamMoneyState) (*entities.PredictionSummary, error) {
	now := time.Now().UTC()

	predictions := []*entities.MiriamPrediction{
		e.predictCashShortfall(ctx, userID, state),
		e.predictBillPressure(ctx, userID, state),
		e.detectSpendingAnomaly(ctx, userID, state),
		e.predictIncomeGap(ctx, userID, state),
		e.detectIdleSurplus(ctx, userID, state),
		e.detectStashOpportunity(ctx, userID, state),
	}

	active := make([]entities.MiriamPrediction, 0, len(predictions))
	for _, p := range predictions {
		if p == nil || p.Probability.LessThan(decimal.NewFromFloat(0.3)) {
			continue
		}
		p.UserID = userID
		p.CreatedAt = now
		p.ExpiresAt = now.Add(predictionHorizonDuration(p.Horizon))

		if e.repo != nil {
			if err := e.repo.UpsertPrediction(ctx, p); err != nil && e.logger != nil {
				e.logger.Warn("prediction upsert failed", zap.String("user_id", userID.String()), zap.Error(err))
				continue
			}
		}
		active = append(active, *p)
	}

	summary := &entities.PredictionSummary{
		UserID:            userID,
		ActivePredictions: active,
		RiskScore:         computeRiskScore(active),
	}
	summary.TopRisk = topRisk(active)
	summary.RecommendedAction = recommendAction(summary)

	return summary, nil
}

func (e *PredictiveEngine) predictCashShortfall(ctx context.Context, userID uuid.UUID, state *entities.MiriamMoneyState) *entities.MiriamPrediction {
	spend, err := e.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return nil
	}

	dailyOutflow := decimal.Zero
	if state.LiquidityRunwayDays > 0 {
		dailyOutflow = spend.Div(decimal.NewFromInt(int64(state.LiquidityRunwayDays)))
	}
	if !dailyOutflow.IsPositive() && state.RecurringSpendMonthly.IsPositive() {
		dailyOutflow = state.RecurringSpendMonthly.Div(decimal.NewFromInt(30))
	}
	if !dailyOutflow.IsPositive() {
		return nil
	}

	now := time.Now().UTC()
	daysUntilPayday := daysUntilNextPayday(state.IncomeCadence, now)

	// Projected balance at next payday
	projectedSpend := spend.Sub(dailyOutflow.Mul(decimal.NewFromInt(int64(daysUntilPayday))))
	shortfall := decimal.Zero
	willShortfall := false
	if projectedSpend.IsNegative() {
		shortfall = projectedSpend.Abs()
		willShortfall = true
	}

	// Also check obligation coverage
	obligationGap := decimal.Zero
	if state.UpcomingObligations.GreaterThan(spend) {
		obligationGap = state.UpcomingObligations.Sub(spend)
		willShortfall = true
		shortfall = maxDecimal(shortfall, obligationGap)
	}

	if !willShortfall {
		return nil
	}

	probability := shortfallProbability(spend, dailyOutflow, daysUntilPayday, state.UpcomingObligations)
	severity := shortfallSeverity(probability, shortfall, spend)

	horizon := horizonForDays(daysUntilPayday)
	reasoning := cashShortfallReasoning(daysUntilPayday, spend, dailyOutflow, state.UpcomingObligations, shortfall)

	snapshot := map[string]interface{}{
		"current_spend":        spend.StringFixed(2),
		"daily_outflow":        dailyOutflow.StringFixed(2),
		"days_until_payday":    daysUntilPayday,
		"projected_spend":      projectedSpend.StringFixed(2),
		"upcoming_obligations": state.UpcomingObligations.StringFixed(2),
		"shortfall_amount":     shortfall.StringFixed(2),
	}

	return &entities.MiriamPrediction{
		ID:              uuid.New(),
		PredictionType:  entities.PredictionCashShortfall,
		Horizon:         horizon,
		Probability:     probability,
		Severity:        severity,
		ProjectedAmount: shortfall,
		Reasoning:       reasoning,
		DataSnapshot:    mustJSON(snapshot),
	}
}

func (e *PredictiveEngine) predictBillPressure(ctx context.Context, userID uuid.UUID, state *entities.MiriamMoneyState) *entities.MiriamPrediction {
	if !state.UpcomingObligations.IsPositive() {
		return nil
	}

	spend, err := e.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return nil
	}

	// Find obligations due in next 7 days
	now := time.Now().UTC()
	weekEnd := now.AddDate(0, 0, 7)
	imminentObligations := decimal.Zero
	imminentCount := 0

	if e.obligations != nil {
		obligations, err := e.obligations.ListActive(ctx, userID)
		if err == nil {
			for _, o := range obligations {
				if !usdLike(o.Currency) {
					continue
				}
				dueDate := nextDueDate(o, now)
				if dueDate != nil && !dueDate.After(weekEnd) && !dueDate.Before(now) {
					imminentObligations = imminentObligations.Add(o.Amount)
					imminentCount++
				}
			}
		}
	}

	if !imminentObligations.IsPositive() {
		return nil
	}

	coverageRatio := decimal.Zero
	if imminentObligations.IsPositive() {
		coverageRatio = spend.Div(imminentObligations)
	}

	probability := decimal.NewFromFloat(0.5)
	if coverageRatio.LessThan(decimal.NewFromFloat(0.5)) {
		probability = decimal.NewFromFloat(0.9)
	} else if coverageRatio.LessThan(decimal.NewFromFloat(0.8)) {
		probability = decimal.NewFromFloat(0.75)
	} else if coverageRatio.LessThan(decimal.NewFromInt(1)) {
		probability = decimal.NewFromFloat(0.6)
	}

	severity := entities.SeverityLow
	if probability.GreaterThan(decimal.NewFromFloat(0.8)) {
		severity = entities.SeverityCritical
	} else if probability.GreaterThan(decimal.NewFromFloat(0.6)) {
		severity = entities.SeverityHigh
	} else if probability.GreaterThan(decimal.NewFromFloat(0.4)) {
		severity = entities.SeverityMedium
	}

	gap := decimal.Zero
	if imminentObligations.GreaterThan(spend) {
		gap = imminentObligations.Sub(spend)
	}

	reasoning := billPressureReasoning(imminentCount, imminentObligations, spend, coverageRatio, gap)

	snapshot := map[string]interface{}{
		"imminent_obligations": imminentObligations.StringFixed(2),
		"imminent_count":       imminentCount,
		"current_spend":        spend.StringFixed(2),
		"coverage_ratio":       coverageRatio.StringFixed(2),
		"gap_amount":           gap.StringFixed(2),
		"days_to_first_due":    daysUntilNextObligation(e.obligations, ctx, userID, now),
	}

	return &entities.MiriamPrediction{
		ID:              uuid.New(),
		PredictionType:  entities.PredictionBillPressure,
		Horizon:         entities.Horizon7Day,
		Probability:     probability,
		Severity:        severity,
		ProjectedAmount: gap,
		Reasoning:       reasoning,
		DataSnapshot:    mustJSON(snapshot),
	}
}

func (e *PredictiveEngine) detectSpendingAnomaly(ctx context.Context, userID uuid.UUID, state *entities.MiriamMoneyState) *entities.MiriamPrediction {
	if state.AnomalyCount <= 0 {
		return nil
	}

	spend, err := e.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return nil
	}

	// Compute actual anomaly amount from money flows
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	lastMonthStart := monthStart.AddDate(0, -1, 0)
	thisMonth, _ := e.spending.GetMoneyFlow(ctx, userID, monthStart, now)
	lastMonth, _ := e.spending.GetMoneyFlow(ctx, userID, lastMonthStart, monthStart)

	anomalyAmount := decimal.Zero
	if thisMonth != nil && lastMonth != nil {
		thisOut := totalOut(thisMonth)
		lastOut := totalOut(lastMonth)
		if lastOut.IsPositive() && thisOut.GreaterThan(lastOut) {
			anomalyAmount = thisOut.Sub(lastOut)
		}
	}

	probability := decimal.NewFromFloat(0.7)
	if state.ConfidenceLevel == "high" {
		probability = decimal.NewFromFloat(0.85)
	}

	severity := entities.SeverityMedium
	if state.AnomalyCount >= 2 {
		severity = entities.SeverityHigh
	}

	reasoning := "Spending anomaly detected. "
	if state.ConfidenceLevel == "high" {
		reasoning += "High confidence based on multiple data points."
	} else {
		reasoning += "Moderate confidence, more data would help."
	}

	snapshot := map[string]interface{}{
		"anomaly_count":    state.AnomalyCount,
		"confidence_level": state.ConfidenceLevel,
		"spend_balance":    spend.StringFixed(2),
		"anomaly_amount":   anomalyAmount.StringFixed(2),
	}

	return &entities.MiriamPrediction{
		ID:              uuid.New(),
		PredictionType:  entities.PredictionSpendingAnomaly,
		Horizon:         entities.Horizon7Day,
		Probability:     probability,
		Severity:        severity,
		ProjectedAmount: anomalyAmount,
		Reasoning:       reasoning,
		DataSnapshot:    mustJSON(snapshot),
	}
}

func (e *PredictiveEngine) predictIncomeGap(ctx context.Context, userID uuid.UUID, state *entities.MiriamMoneyState) *entities.MiriamPrediction {
	if state.AvgMonthlyIncome.IsZero() || state.IncomeCadence == "unknown" {
		return nil
	}

	now := time.Now().UTC()

	// Projected income for rest of month based on cadence
	projectedIncome := projectedMonthlyIncome(state, now)

	// Monthly obligations — use the larger of recurring or upcoming to avoid
	// double-counting (obligations with both a DueDay/DueDate and a Cadence
	// appear in both fields).
	totalNeeds := state.RecurringSpendMonthly
	if state.UpcomingObligations.GreaterThan(totalNeeds) {
		totalNeeds = state.UpcomingObligations
	}

	gap := decimal.Zero
	willGap := false
	if totalNeeds.GreaterThan(state.AvgMonthlyIncome.Add(projectedIncome)) {
		gap = totalNeeds.Sub(state.AvgMonthlyIncome.Add(projectedIncome))
		willGap = true
	}

	// Also check if projected income is significantly below average
	if state.AvgMonthlyIncome.IsPositive() && projectedIncome.LessThan(state.AvgMonthlyIncome.Mul(decimal.NewFromFloat(0.7))) {
		willGap = true
		gap = maxDecimal(gap, state.AvgMonthlyIncome.Sub(projectedIncome))
	}

	if !willGap {
		return nil
	}

	probability := incomeGapProbability(state, projectedIncome)
	severity := entities.SeverityMedium
	if gap.GreaterThan(state.AvgMonthlyIncome.Mul(decimal.NewFromFloat(0.2))) {
		severity = entities.SeverityHigh
	}

	reasoning := incomeGapReasoning(state.IncomeCadence, state.AvgMonthlyIncome, projectedIncome, totalNeeds, gap)

	snapshot := map[string]interface{}{
		"avg_monthly_income":  state.AvgMonthlyIncome.StringFixed(2),
		"projected_income":    projectedIncome.StringFixed(2),
		"total_monthly_needs": totalNeeds.StringFixed(2),
		"gap_amount":          gap.StringFixed(2),
		"income_cadence":      state.IncomeCadence,
	}

	return &entities.MiriamPrediction{
		ID:              uuid.New(),
		PredictionType:  entities.PredictionIncomeGap,
		Horizon:         entities.Horizon30Day,
		Probability:     probability,
		Severity:        severity,
		ProjectedAmount: gap,
		Reasoning:       reasoning,
		DataSnapshot:    mustJSON(snapshot),
	}
}

func (e *PredictiveEngine) detectIdleSurplus(ctx context.Context, userID uuid.UUID, state *entities.MiriamMoneyState) *entities.MiriamPrediction {
	spend, err := e.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return nil
	}

	// Surplus = spend - (upcoming obligations + projected 14-day spend)
	projectedSpend := decimal.Zero
	if state.LiquidityRunwayDays > 0 {
		dailyOutflow := spend.Div(decimal.NewFromInt(int64(state.LiquidityRunwayDays)))
		projectedSpend = dailyOutflow.Mul(decimal.NewFromInt(14))
	}

	totalNeeds := state.UpcomingObligations.Add(projectedSpend)
	surplus := spend.Sub(totalNeeds)

	// Only flag as idle if surplus is meaningful (> $50 and > 20% of spend)
	if !surplus.GreaterThan(decimal.NewFromInt(50)) {
		return nil
	}
	if spend.IsPositive() && surplus.LessThan(spend.Mul(decimal.NewFromFloat(0.20))) {
		return nil
	}

	probability := decimal.NewFromFloat(0.8)

	snapshot := map[string]interface{}{
		"spend_balance":         spend.StringFixed(2),
		"upcoming_obligations":  state.UpcomingObligations.StringFixed(2),
		"projected_14day_spend": projectedSpend.StringFixed(2),
		"idle_surplus":          surplus.StringFixed(2),
	}

	return &entities.MiriamPrediction{
		ID:              uuid.New(),
		PredictionType:  entities.PredictionIdleSurplus,
		Horizon:         entities.Horizon14Day,
		Probability:     probability,
		Severity:        entities.SeverityLow,
		ProjectedAmount: surplus,
		Reasoning:       "You have idle money sitting in Spend that could be working for you in Stash.",
		DataSnapshot:    mustJSON(snapshot),
	}
}

func (e *PredictiveEngine) detectStashOpportunity(ctx context.Context, userID uuid.UUID, state *entities.MiriamMoneyState) *entities.MiriamPrediction {
	stash, err := e.balances.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return nil
	}

	// Stash opportunity: stash is below target and spend has surplus
	if state.StashTarget.IsZero() || stash.GreaterThanOrEqual(state.StashTarget) {
		return nil
	}

	gap := state.StashTarget.Sub(stash)
	probability := decimal.NewFromFloat(0.6)
	if state.LiquidityRunwayDays > 30 {
		probability = decimal.NewFromFloat(0.8)
	}

	snapshot := map[string]interface{}{
		"stash_balance": stash.StringFixed(2),
		"stash_target":  state.StashTarget.StringFixed(2),
		"stash_gap":     gap.StringFixed(2),
		"runway_days":   state.LiquidityRunwayDays,
	}

	return &entities.MiriamPrediction{
		ID:              uuid.New(),
		PredictionType:  entities.PredictionStashOpportunity,
		Horizon:         entities.Horizon30Day,
		Probability:     probability,
		Severity:        entities.SeverityLow,
		ProjectedAmount: gap,
		Reasoning:       "Your Stash is below target. A quiet top-up would keep you on track.",
		DataSnapshot:    mustJSON(snapshot),
	}
}

// --- Active prediction queries ---

func (e *PredictiveEngine) GetActivePredictions(ctx context.Context, userID uuid.UUID) (*entities.PredictionSummary, error) {
	if e.repo == nil {
		return &entities.PredictionSummary{UserID: userID}, nil
	}
	predictions, err := e.repo.ListActivePredictions(ctx, userID)
	if err != nil {
		return nil, err
	}

	summary := &entities.PredictionSummary{
		UserID:            userID,
		ActivePredictions: predictions,
		RiskScore:         computeRiskScore(predictions),
	}
	summary.TopRisk = topRisk(predictions)
	summary.RecommendedAction = recommendAction(summary)

	return summary, nil
}

func (e *PredictiveEngine) ExpireOldPredictions(ctx context.Context) (int64, error) {
	if e.repo == nil {
		return 0, nil
	}
	return e.repo.ExpireOldPredictions(ctx, time.Now().UTC())
}

// --- Probability and severity helpers ---

func shortfallProbability(spend, dailyOutflow decimal.Decimal, daysUntilPayday int, obligations decimal.Decimal) decimal.Decimal {
	if !dailyOutflow.IsPositive() {
		return decimal.NewFromFloat(0.1)
	}

	projectedSpend := spend.Sub(dailyOutflow.Mul(decimal.NewFromInt(int64(daysUntilPayday))))

	if projectedSpend.LessThan(decimal.Zero) {
		// Certain shortfall
		return decimal.NewFromFloat(0.95)
	}

	buffer := projectedSpend
	if obligations.IsPositive() && spend.LessThan(obligations) {
		return decimal.NewFromFloat(0.85)
	}

	// Probability increases as buffer shrinks
	if spend.IsPositive() {
		bufferRatio := buffer.Div(spend).InexactFloat64()
		if bufferRatio < 0.1 {
			return decimal.NewFromFloat(0.8)
		}
		if bufferRatio < 0.25 {
			return decimal.NewFromFloat(0.6)
		}
		if bufferRatio < 0.5 {
			return decimal.NewFromFloat(0.4)
		}
	}
	return decimal.NewFromFloat(0.2)
}

func shortfallSeverity(probability, shortfall, spend decimal.Decimal) string {
	if probability.GreaterThan(decimal.NewFromFloat(0.8)) && shortfall.GreaterThan(spend.Mul(decimal.NewFromFloat(0.3))) {
		return entities.SeverityCritical
	}
	if probability.GreaterThan(decimal.NewFromFloat(0.7)) || shortfall.GreaterThan(spend.Mul(decimal.NewFromFloat(0.2))) {
		return entities.SeverityHigh
	}
	if probability.GreaterThan(decimal.NewFromFloat(0.4)) {
		return entities.SeverityMedium
	}
	return entities.SeverityLow
}

func incomeGapProbability(state *entities.MiriamMoneyState, projectedIncome decimal.Decimal) decimal.Decimal {
	if state.AvgMonthlyIncome.IsZero() {
		return decimal.NewFromFloat(0.3)
	}

	ratio := projectedIncome.Div(state.AvgMonthlyIncome).InexactFloat64()
	if ratio < 0.5 {
		return decimal.NewFromFloat(0.9)
	}
	if ratio < 0.7 {
		return decimal.NewFromFloat(0.75)
	}
	if ratio < 0.9 {
		return decimal.NewFromFloat(0.5)
	}
	return decimal.NewFromFloat(0.3)
}

// --- Reasoning generators ---

func cashShortfallReasoning(daysUntilPayday int, spend, dailyOutflow, obligations, shortfall decimal.Decimal) string {
	var b strings.Builder
	b.WriteString("At current spend rate, you'll run out before payday")
	if daysUntilPayday > 0 {
		b.WriteString(fmt.Sprintf(" (%d days).", daysUntilPayday))
	} else {
		b.WriteString(".")
	}

	if shortfall.IsPositive() {
		b.WriteString(" Projected shortfall: $")
		b.WriteString(shortfall.StringFixed(2))
		b.WriteString(".")
	}
	if obligations.GreaterThan(spend) {
		b.WriteString(" Upcoming obligations exceed current balance.")
	}
	return b.String()
}

func billPressureReasoning(count int, obligations, spend, coverageRatio, gap decimal.Decimal) string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(count))
	if count == 1 {
		b.WriteString(" bill due within 7 days")
	} else {
		b.WriteString(" bills due within 7 days")
	}
	b.WriteString(" totaling $")
	b.WriteString(obligations.StringFixed(2))
	b.WriteString(". ")

	if coverageRatio.LessThan(decimal.NewFromInt(1)) {
		b.WriteString("Spend balance ($")
		b.WriteString(spend.StringFixed(2))
		b.WriteString(") doesn't cover it")
		if gap.IsPositive() {
			b.WriteString(", $")
			b.WriteString(gap.StringFixed(2))
			b.WriteString(" short")
		}
		b.WriteString(".")
	} else {
		b.WriteString("Spend balance covers it with $")
		remaining := spend.Sub(obligations)
		b.WriteString(remaining.StringFixed(2))
		b.WriteString(" left.")
	}

	return b.String()
}

func incomeGapReasoning(cadence string, avgIncome, projectedIncome, totalNeeds, gap decimal.Decimal) string {
	var b strings.Builder
	b.WriteString("Projected income ($")
	b.WriteString(projectedIncome.StringFixed(2))
	b.WriteString(") may not cover monthly needs ($")
	b.WriteString(totalNeeds.StringFixed(2))
	b.WriteString(")")

	if gap.IsPositive() {
		b.WriteString(", gap of $")
		b.WriteString(gap.StringFixed(2))
	}
	b.WriteString(".")

	if cadence != "unknown" {
		b.WriteString(" Income pattern: ")
		b.WriteString(cadence)
		b.WriteString(".")
	}

	return b.String()
}

// --- Utility helpers ---

func computeRiskScore(predictions []entities.MiriamPrediction) int {
	if len(predictions) == 0 {
		return 0
	}

	score := 0
	for _, p := range predictions {
		weight := severityWeight(p.Severity)
		prob := p.Probability.InexactFloat64()
		score += int(math.Round(prob * float64(weight)))
	}

	if score > 100 {
		return 100
	}
	return score
}

func severityWeight(severity string) int {
	switch severity {
	case entities.SeverityCritical:
		return 40
	case entities.SeverityHigh:
		return 25
	case entities.SeverityMedium:
		return 15
	case entities.SeverityLow:
		return 8
	default:
		return 10
	}
}

func topRisk(predictions []entities.MiriamPrediction) string {
	if len(predictions) == 0 {
		return ""
	}

	sort.Slice(predictions, func(i, j int) bool {
		scoreI := severityWeight(predictions[i].Severity) * int(predictions[i].Probability.Mul(decimal.NewFromInt(100)).IntPart())
		scoreJ := severityWeight(predictions[j].Severity) * int(predictions[j].Probability.Mul(decimal.NewFromInt(100)).IntPart())
		return scoreI > scoreJ
	})

	return predictions[0].PredictionType
}

func recommendAction(summary *entities.PredictionSummary) string {
	if summary.RiskScore == 0 {
		return ""
	}

	switch summary.TopRisk {
	case entities.PredictionCashShortfall:
		return "Review spending and consider moving funds from Stash to cover upcoming obligations."
	case entities.PredictionBillPressure:
		return "Check upcoming bills, some may not be fully covered by current Spend balance."
	case entities.PredictionIncomeGap:
		return "Income projection shows a potential gap. Consider adjusting spending or tapping Stash."
	case entities.PredictionSpendingAnomaly:
		return "Unusual spending detected. Review recent transactions."
	case entities.PredictionIdleSurplus:
		return "Idle money detected in Spend. Consider moving to Stash for yield."
	case entities.PredictionStashOpportunity:
		return "Stash is below target. A small top-up would keep you on track."
	default:
		return "Review your financial position."
	}
}

func predictionHorizonDuration(horizon string) time.Duration {
	switch horizon {
	case entities.Horizon3Day:
		return 3 * 24 * time.Hour
	case entities.Horizon7Day:
		return 7 * 24 * time.Hour
	case entities.Horizon14Day:
		return 14 * 24 * time.Hour
	case entities.Horizon30Day:
		return 30 * 24 * time.Hour
	default:
		return 7 * 24 * time.Hour
	}
}

func horizonForDays(days int) string {
	switch {
	case days <= 3:
		return entities.Horizon3Day
	case days <= 7:
		return entities.Horizon7Day
	case days <= 14:
		return entities.Horizon14Day
	default:
		return entities.Horizon30Day
	}
}

func daysInCurrentMonth(now time.Time) int {
	return time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func daysUntilNextPayday(cadence string, now time.Time) int {
	daysInMonth := daysInCurrentMonth(now)
	dayOfMonth := now.Day()

	switch cadence {
	case "weekly":
		daysUntil := 7 - int(now.Weekday())
		if daysUntil == 0 {
			daysUntil = 7
		}
		return daysUntil
	case "biweekly":
		// Approximate: assume paid on 1st and 15th
		if dayOfMonth < 15 {
			return 15 - dayOfMonth
		}
		return daysInMonth - dayOfMonth + 1
	case "monthly", "unknown":
		return daysInMonth - dayOfMonth
	default:
		return daysInMonth - dayOfMonth
	}
}

func projectedMonthlyIncome(state *entities.MiriamMoneyState, now time.Time) decimal.Decimal {
	if state.AvgMonthlyIncome.IsZero() {
		return decimal.Zero
	}

	dayOfMonth := now.Day()
	daysInMonth := daysInCurrentMonth(now)
	daysElapsed := dayOfMonth

	if daysElapsed <= 0 {
		return state.AvgMonthlyIncome
	}

	// Scale based on how far into the month we are and cadence
	switch state.IncomeCadence {
	case "weekly":
		weeksRemaining := (daysInMonth - dayOfMonth) / 7
		weeklyIncome := state.AvgMonthlyIncome.Div(decimal.NewFromInt(4))
		return weeklyIncome.Mul(decimal.NewFromInt(int64(weeksRemaining)))
	case "biweekly":
		// Approximate 2 payments per month
		if dayOfMonth <= 15 {
			// One more payment coming
			return state.AvgMonthlyIncome.Div(decimal.NewFromInt(2))
		}
		return decimal.Zero
	case "monthly":
		if dayOfMonth > 25 {
			// Likely already received this month's income
			return decimal.Zero
		}
		return state.AvgMonthlyIncome
	default:
		// Unknown — prorate remaining days
		remainingDays := daysInMonth - dayOfMonth
		dailyIncome := state.AvgMonthlyIncome.Div(decimal.NewFromInt(int64(daysInMonth)))
		return dailyIncome.Mul(decimal.NewFromInt(int64(remainingDays)))
	}
}

func nextDueDate(o entities.FinancialObligation, from time.Time) *time.Time {
	if o.DueDate != nil && !o.DueDate.Before(from) {
		return o.DueDate
	}
	if o.DueDay != nil {
		due := time.Date(from.Year(), from.Month(), *o.DueDay, 0, 0, 0, 0, time.UTC)
		if due.Before(from) {
			due = due.AddDate(0, 1, 0)
		}
		return &due
	}
	return nil
}

func daysUntilNextObligation(obs ObligationProvider, ctx context.Context, userID uuid.UUID, now time.Time) int {
	if obs == nil {
		return 0
	}
	obligations, err := obs.ListActive(ctx, userID)
	if err != nil {
		return 0
	}

	minDays := 999
	for _, o := range obligations {
		due := nextDueDate(o, now)
		if due != nil {
			days := int(due.Sub(now).Hours() / 24)
			if days < minDays {
				minDays = days
			}
		}
	}
	if minDays == 999 {
		return 0
	}
	return minDays
}
