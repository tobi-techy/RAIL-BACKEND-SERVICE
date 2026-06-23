package miriam

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/analytics"
	"github.com/shopspring/decimal"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

var miriamTracer = otel.Tracer("miriam")

var (
	miriamEvaluations = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "miriam_evaluations_total",
		Help: "Total Miriam user evaluations",
	}, []string{"status"})
	miriamEvalDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "miriam_evaluation_duration_seconds",
		Help:    "Duration of Miriam evaluation per user",
		Buckets: prometheus.ExponentialBuckets(0.01, 2, 12),
	})
	miriamPredictions = promauto.NewCounter(prometheus.CounterOpts{
		Name: "miriam_predictions_generated_total",
		Help: "Total predictions generated",
	})
	miriamMandatesExecuted = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "miriam_mandates_executed_total",
		Help: "Total mandates executed by action type",
	}, []string{"action_type", "status"})
	miriamNudgesDelivered = promauto.NewCounter(prometheus.CounterOpts{
		Name: "miriam_nudges_delivered_total",
		Help: "Total proactive nudges delivered",
	})
)

// New mandate action types for autonomous money intelligence.
const (
	MiriamMandateTransferToStash  = entities.MiriamMandateTransferToStash // existing
	MiriamMandateTransferToSpend  = "transfer_to_spend"                   // stash → spend for bills
	MiriamMandateBillReservation  = "bill_reservation"                    // set aside for upcoming bills
	MiriamMandateSpendCooldown    = "spend_cooldown"                      // suggest spending restriction
	MiriamMandateGoalContribution = "goal_contribution"                   // auto-save to goals
	MiriamMandateStashTopUp       = "stash_top_up"                        // surplus → stash
	MiriamMandateIdleSweep        = "idle_sweep"                          // sweep excess above threshold
)

// IntelligenceOrchestrator is Miriam's unified brain — a single evaluation pass
// that coordinates predictions, decisions, memory, learning, and actions.
type IntelligenceOrchestrator struct {
	service      *Service
	decisions    *DecisionEngine
	nudges       *ProactiveNudgeEngine
	predictions  *PredictiveEngine
	signals      *SignalDetector
	suggestions  *MandateSuggestionEngine
	obDetector   *ObligationAutoDetector
	enricher     *TransactionEnricher
	dispatcher   *NotificationDispatcher
	memory       MemoryReader
	notifier     Notifier
	healthScore  *HealthScoreTracker
	outcomeTrack *OutcomeTracker
	logger       *zap.Logger
}

// NewIntelligenceOrchestrator creates the unified brain.
func NewIntelligenceOrchestrator(
	service *Service,
	decisions *DecisionEngine,
	nudges *ProactiveNudgeEngine,
	predictions *PredictiveEngine,
	signals *SignalDetector,
	suggestions *MandateSuggestionEngine,
	obDetector *ObligationAutoDetector,
	dispatcher *NotificationDispatcher,
	memory MemoryReader,
	notifier Notifier,
	healthScore *HealthScoreTracker,
	outcomeTrack *OutcomeTracker,
	logger *zap.Logger,
) *IntelligenceOrchestrator {
	return &IntelligenceOrchestrator{
		service: service, decisions: decisions, nudges: nudges,
		predictions: predictions, signals: signals, suggestions: suggestions,
		obDetector: obDetector, dispatcher: dispatcher, memory: memory,
		notifier: notifier, healthScore: healthScore, outcomeTrack: outcomeTrack,
		logger: logger,
	}
}

// SetMemory injects a MemoryReader after construction (deferred wiring).
func (o *IntelligenceOrchestrator) SetMemory(m MemoryReader) {
	o.memory = m
}

// SetNotifier injects a Notifier after construction (deferred wiring).
func (o *IntelligenceOrchestrator) SetNotifier(n Notifier) {
	o.notifier = n
}

// SetEnricher injects a TransactionEnricher after construction (deferred wiring).
func (o *IntelligenceOrchestrator) SetEnricher(e *TransactionEnricher) {
	o.enricher = e
}

// HealthScoreTracker returns the health score tracker for maintenance operations.
func (o *IntelligenceOrchestrator) HealthScoreTracker() *HealthScoreTracker {
	return o.healthScore
}

// IntelligenceResult is the output of a single evaluation pass.
type IntelligenceResult struct {
	UserID          uuid.UUID                        `json:"user_id"`
	MoneyState      *entities.MiriamMoneyState       `json:"money_state"`
	Predictions     *entities.PredictionSummary      `json:"predictions"`
	DecisionsMade   int                              `json:"decisions_made"`
	ActionsExecuted int                              `json:"actions_executed"`
	NudgesGenerated int                              `json:"nudges_generated"`
	SuggestionsMade int                              `json:"suggestions_made"`
	Receipts        []entities.MiriamDecisionReceipt `json:"receipts"`
	EvaluatedAt     time.Time                        `json:"evaluated_at"`
	Duration        time.Duration                    `json:"duration_ms"`
}

// Evaluate runs the full intelligence pipeline for one user.
func (o *IntelligenceOrchestrator) Evaluate(ctx context.Context, userID uuid.UUID, eventType string) (*IntelligenceResult, error) {
	ctx, span := miriamTracer.Start(ctx, "miriam.Evaluate")
	span.SetAttributes(attribute.String("user_id", userID.String()), attribute.String("event_type", eventType))
	defer span.End()

	start := time.Now().UTC()
	result := &IntelligenceResult{UserID: userID}

	// 1. Refresh money state (existing logic)
	state, err := o.service.RefreshMoneyState(ctx, userID)
	if err != nil {
		miriamEvaluations.WithLabelValues("error").Inc()
		return nil, fmt.Errorf("refresh money state: %w", err)
	}
	result.MoneyState = state

	// 1b. Compute and record financial health score
	if o.healthScore != nil {
		runway := state.LiquidityRunwayDays
		o.healthScore.RecordScore(ctx, userID,
			computeOverall(state),
			computeBudgetScore(state),
			computeSavingsScore(state),
			computeDebtScore(state),
			computeRunwayScore(runway),
			computeStabilityScore(state),
			generateHealthReasoning(state),
			map[string]interface{}{
				"income_cadence":  state.IncomeCadence,
				"confidence":      state.ConfidenceLevel,
				"anomaly_count":   state.AnomalyCount,
				"runway_days":     state.LiquidityRunwayDays,
				"monthly_income":  state.AvgMonthlyIncome.StringFixed(2),
				"upcoming_bills":  state.UpcomingObligations.StringFixed(2),
				"recurring_spend": state.RecurringSpendMonthly.StringFixed(2),
			})
	}

	// 2. Generate predictions
	predictions, err := o.predictions.GeneratePredictions(ctx, userID, state)
	if err != nil && o.logger != nil {
		o.logger.Warn("prediction generation failed", zap.String("user_id", userID.String()), zap.Error(err))
	}
	result.Predictions = predictions

	// 2b. Record prediction outcomes (pending) and evaluate expired ones
	if o.outcomeTrack != nil {
		if predictions != nil {
			o.outcomeTrack.RecordPredictions(ctx, userID, predictions.ActivePredictions)
		}
		o.outcomeTrack.EvaluateOutcomes(ctx, userID)
	}

	// 3. Detect and upsert context signals
	if o.signals != nil {
		if err := o.signals.DetectAndUpsert(ctx, userID); err != nil && o.logger != nil {
			o.logger.Debug("signal detection failed", zap.String("user_id", userID.String()), zap.Error(err))
		}
	}

	// 4. Load memory context
	var memoryFacts []entities.MiriamUserFact
	if o.memory != nil {
		facts, err := o.memory.GetActiveFacts(ctx, userID)
		if err == nil {
			memoryFacts = filterActionRelevantFacts(facts)
		}
	}

	// 4. Get learning bias
	learningBias := decimal.Zero
	if o.service.repo != nil {
		bias, err := o.service.repo.RecentLearningBias(ctx, userID, time.Now().UTC().AddDate(0, -1, 0))
		if err == nil {
			learningBias = bias
		}
	}

	// 5. Evaluate mandates with decision engine
	decisionsMade := 0
	actionsExecuted := 0
	if eventType == EventWorkerSweep || eventType == EventIncomeLowerThanUsual {
		mandates, err := o.service.repo.ListActiveMandates(ctx, userID)
		if err == nil {
			for _, mandate := range mandates {
				dc := &entities.DecisionContext{
					MoneyState:   state,
					Predictions:  predictions,
					MemoryFacts:  memoryFacts,
					LearningBias: learningBias,
					Mandate:      mandate,
					EventType:    eventType,
				}

				decision, err := o.decisions.MakeDecision(ctx, dc)
				if err != nil && o.logger != nil {
					o.logger.Warn("decision failed", zap.Error(err))
					continue
				}
				decisionsMade++

				if ShouldExecute(decision) {
					amount := ExecuteAmount(decision)
					if err := o.executeMandateAction(ctx, userID, mandate, amount); err != nil && o.logger != nil {
						o.logger.Warn("mandate execution failed", zap.Error(err))
					} else {
						actionsExecuted++
					}
				}
			}
		}
	}
	result.DecisionsMade = decisionsMade
	result.ActionsExecuted = actionsExecuted

	// 6. Generate proactive nudges
	if predictions != nil && predictions.RiskScore > 20 {
		nudges, err := o.nudges.GenerateProactiveNudgesWithPredictions(ctx, userID, state, predictions)
		if err == nil {
			result.NudgesGenerated = len(nudges)
		}
	}

	// 7. Generate mandate suggestions (autonomous intelligence)
	suggestions, err := o.suggestions.GenerateSuggestions(ctx, userID, state, memoryFacts)
	if err == nil {
		result.SuggestionsMade = len(suggestions)
	}

	// 8. Enrich transactions for improved categorization
	if o.enricher != nil && o.enricher.transactions != nil && eventType == EventWorkerSweep {
		if txns, err := o.enricher.transactions.GetUserTransactions(ctx, userID, 50, 0); err == nil && len(txns) > 0 {
			if _, err := o.enricher.EnrichBatch(ctx, txns); err != nil && o.logger != nil {
				o.logger.Debug("transaction enrichment batch failed", zap.String("user_id", userID.String()), zap.Error(err))
			}
		}
	}

	// 9. Detect recurring obligations from transactions (weekly check)
	if o.obDetector != nil && eventType == EventWorkerSweep {
		detected, err := o.obDetector.DetectRecurringPayments(ctx, userID)
		if err == nil && len(detected) > 0 && o.logger != nil {
			o.logger.Info("obligation auto-detection found candidates",
				zap.String("user_id", userID.String()),
				zap.Int("count", len(detected)))
		}
	}

	// 10. Flush notification batches (dispatcher handles batching/digest)
	if o.dispatcher != nil {
		_ = o.dispatcher.FlushBatches(ctx)
	}

	result.EvaluatedAt = time.Now().UTC()
	result.Duration = time.Since(start)

	// Record metrics
	miriamEvaluations.WithLabelValues("success").Inc()
	miriamEvalDuration.Observe(result.Duration.Seconds())
	if result.Predictions != nil {
		miriamPredictions.Add(float64(len(result.Predictions.ActivePredictions)))
	}
	miriamNudgesDelivered.Add(float64(result.NudgesGenerated))

	// Mixpanel: track the evaluation pass
	healthScore := computeOverall(state)
	analyticsProps := map[string]any{
		"event_type":       eventType,
		"decisions_made":   result.DecisionsMade,
		"actions_executed": result.ActionsExecuted,
		"nudges_generated": result.NudgesGenerated,
		"suggestions_made": result.SuggestionsMade,
		"duration_ms":      result.Duration.Milliseconds(),
		"health_score":     healthScore,
		"runway_days":      state.LiquidityRunwayDays,
		"confidence":       state.ConfidenceLevel,
	}
	if result.Predictions != nil {
		analyticsProps["risk_score"] = result.Predictions.RiskScore
		analyticsProps["active_predictions"] = len(result.Predictions.ActivePredictions)
	}
	analytics.TrackEvent(ctx, userID.String(), analytics.EventMiriamEvaluationRun, analyticsProps)

	// Update user profile with financial health snapshot
	analytics.IdentifyUser(ctx, userID.String(), map[string]any{
		analytics.PropFinancialHealthScore: healthScore,
		analytics.PropLiquidityRunwayDays:  state.LiquidityRunwayDays,
		analytics.PropIncomeCadence:        state.IncomeCadence,
		analytics.PropAvgMonthlyIncome:     state.AvgMonthlyIncome.InexactFloat64(),
		analytics.PropConfidenceLevel:      state.ConfidenceLevel,
	})

	if result.NudgesGenerated > 0 {
		analytics.TrackEvent(ctx, userID.String(), analytics.EventMiriamNudgeSent, map[string]any{
			"nudge_count": result.NudgesGenerated,
			"event_type":  eventType,
			"risk_score":  func() int { if result.Predictions != nil { return result.Predictions.RiskScore }; return 0 }(),
		})
	}
	if result.SuggestionsMade > 0 {
		analytics.TrackEvent(ctx, userID.String(), analytics.EventMiriamMandateSuggested, map[string]any{
			"suggestion_count": result.SuggestionsMade,
			"health_score":     healthScore,
		})
	}

	return result, nil
}

// EvaluateBatch runs intelligence for multiple users.
func (o *IntelligenceOrchestrator) EvaluateBatch(ctx context.Context, userIDs []uuid.UUID, eventType string) (evaluated, failed int) {
	for _, userID := range userIDs {
		if _, err := o.Evaluate(ctx, userID, eventType); err != nil {
			failed++
			if o.logger != nil {
				o.logger.Error("intelligence evaluation failed for user",
					zap.String("user_id", userID.String()), zap.Error(err))
			}
		} else {
			evaluated++
		}
	}
	return
}

func (o *IntelligenceOrchestrator) executeMandateAction(ctx context.Context, userID uuid.UUID, mandate entities.MiriamAutopilotMandate, amount decimal.Decimal) error {
	var err error
	switch mandate.ActionType {
	case entities.MiriamMandateTransferToStash:
		err = o.executeTransferToStash(ctx, userID, amount, mandate.ID)
	case MiriamMandateTransferToSpend:
		err = o.executeTransferToSpend(ctx, userID, amount, mandate.ID)
	case MiriamMandateStashTopUp, MiriamMandateIdleSweep:
		err = o.executeTransferToStash(ctx, userID, amount, mandate.ID)
	case MiriamMandateBillReservation:
		err = o.recordBillReservation(ctx, userID, amount, mandate)
	case MiriamMandateSpendCooldown:
		err = o.recordReceipt(ctx, userID, amount, mandate, "spend_cooldown", "Spending cooldown activated per mandate.")
	case MiriamMandateGoalContribution:
		err = o.executeTransferToStash(ctx, userID, amount, mandate.ID)
	default:
		return fmt.Errorf("unknown mandate action type: %s", mandate.ActionType)
	}

	status := "success"
	if err != nil {
		status = "failed"
	}
	miriamMandatesExecuted.WithLabelValues(mandate.ActionType, status).Inc()
	analytics.TrackEvent(ctx, userID.String(), analytics.EventMiriamActionExecuted, map[string]any{
		"action_type": mandate.ActionType,
		"amount":      amount.InexactFloat64(),
		"mandate_id":  mandate.ID.String(),
		"status":      status,
	})
	if err == nil {
		analytics.G().Increment(ctx, userID.String(), map[string]int{
			analytics.PropMiriamActionsTotal: 1,
		})
	}
	return err
}

func (o *IntelligenceOrchestrator) executeTransferToStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, mandateID uuid.UUID) error {
	windowSec := int64(1440) * 60 // 24-hour window
	idempotencyKey := fmt.Sprintf("miriam-autopilot-%s-%d", mandateID.String(), time.Now().UTC().Unix()/windowSec)
	return o.service.transfer.TransferSpendingToStash(ctx, userID, amount, idempotencyKey)
}

func (o *IntelligenceOrchestrator) executeTransferToSpend(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, mandateID uuid.UUID) error {
	windowSec := int64(1440) * 60 // 24-hour window
	idempotencyKey := fmt.Sprintf("miriam-autopilot-spend-%s-%d", mandateID.String(), time.Now().UTC().Unix()/windowSec)
	if err := o.service.transfer.TransferStashToSpending(ctx, userID, amount, idempotencyKey); err != nil {
		return fmt.Errorf("execute stash-to-spend transfer: %w", err)
	}

	if o.logger != nil {
		o.logger.Info("transferred stash to spend for bills",
			zap.String("user_id", userID.String()),
			zap.String("amount", amount.StringFixed(2)),
			zap.String("mandate_id", mandateID.String()))
	}

	if o.notifier != nil {
		_ = o.notifier.SendGenericNotification(ctx, userID, "Miriam moved money from Stash",
			fmt.Sprintf("Moved $%s from Stash to Spending for upcoming bills.", amount.StringFixed(2)))
	}
	return nil
}

func (o *IntelligenceOrchestrator) recordBillReservation(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, mandate entities.MiriamAutopilotMandate) error {
	reason := fmt.Sprintf("Reserved $%s for upcoming bills per mandate.", amount.StringFixed(2))
	receipt := &entities.MiriamDecisionReceipt{
		ID:         uuid.New(),
		UserID:     userID,
		MandateID:  &mandate.ID,
		EventType:  "bill_reservation",
		ActionType: mandate.ActionType,
		Amount:     amount,
		Currency:   "USD",
		Status:     entities.MiriamReceiptStatusExecuted,
		Reason:     reason,
		Evidence:   mustJSON(map[string]interface{}{"generated_by": "intelligence_orchestrator"}),
		CreatedAt:  time.Now().UTC(),
	}
	return o.service.repo.CreateReceipt(ctx, receipt)
}

func (o *IntelligenceOrchestrator) recordReceipt(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, mandate entities.MiriamAutopilotMandate, eventType, reason string) error {
	receipt := &entities.MiriamDecisionReceipt{
		ID:         uuid.New(),
		UserID:     userID,
		MandateID:  &mandate.ID,
		EventType:  eventType,
		ActionType: mandate.ActionType,
		Amount:     amount,
		Currency:   "USD",
		Status:     entities.MiriamReceiptStatusExecuted,
		Reason:     reason,
		Evidence:   mustJSON(map[string]interface{}{"generated_by": "intelligence_orchestrator"}),
		CreatedAt:  time.Now().UTC(),
	}
	return o.service.repo.CreateReceipt(ctx, receipt)
}

func filterActionRelevantFacts(facts []*entities.MiriamUserFact) []entities.MiriamUserFact {
	relevant := make([]entities.MiriamUserFact, 0, len(facts))
	for _, f := range facts {
		switch f.Category {
		case entities.FactCategoryGoal,
			entities.FactCategoryFear,
			entities.FactCategoryHabit,
			entities.FactCategoryFinancialBehavior,
			entities.FactCategoryLifeEvent,
			entities.FactCategoryIncomePattern,
			entities.FactCategoryDepositCadence,
			entities.FactCategorySalaryDay,
			entities.FactCategoryFreelancePattern,
			entities.FactCategoryFamilySupport,
			entities.FactCategoryCurrencyContext,
			entities.FactCategoryRiskPreference,
			entities.FactCategoryStashBehavior:
			if f.Confidence.GreaterThanOrEqual(decimal.NewFromFloat(0.5)) {
				relevant = append(relevant, *f)
			}
		}
	}
	return relevant
}

// computeOverall derives an overall health score from money state (0–100).
func computeOverall(state *entities.MiriamMoneyState) int {
	runway := state.LiquidityRunwayDays
	budgetScore := computeBudgetScore(state)
	savingsScore := computeSavingsScore(state)
	debtScore := computeDebtScore(state)
	runwayScore := computeRunwayScore(runway)
	stabilityScore := computeStabilityScore(state)
	return int(
		float64(savingsScore)*0.25 +
			float64(budgetScore)*0.20 +
			float64(runwayScore)*0.25 +
			float64(debtScore)*0.15 +
			float64(stabilityScore)*0.15,
	)
}

func computeBudgetScore(state *entities.MiriamMoneyState) int {
	if state.RecurringSpendMonthly.IsZero() || state.AvgMonthlyIncome.IsZero() {
		return 60
	}
	ratio := state.RecurringSpendMonthly.Div(state.AvgMonthlyIncome).InexactFloat64()
	switch {
	case ratio <= 0.5:
		return 100
	case ratio <= 0.7:
		return 80
	case ratio <= 0.9:
		return 60
	default:
		return 30
	}
}

func computeSavingsScore(state *entities.MiriamMoneyState) int {
	if state.AvgMonthlyIncome.IsZero() {
		return 40
	}
	// Use actual trailing savings (avg deposits − avg outflow) as the savings rate.
	savingsRate := state.MonthlySavings.Div(state.AvgMonthlyIncome).InexactFloat64()
	switch {
	case savingsRate >= 0.30:
		return 100
	case savingsRate >= 0.20:
		return 80
	case savingsRate >= 0.10:
		return 60
	case savingsRate >= 0.05:
		return 40
	default:
		return 20
	}
}

func computeDebtScore(state *entities.MiriamMoneyState) int {
	if state.UpcomingObligations.IsZero() {
		return 100
	}
	if state.SpendBalance.IsZero() {
		return 40
	}
	ratio := state.UpcomingObligations.Div(state.SpendBalance).InexactFloat64()
	switch {
	case ratio <= 0.3:
		return 100
	case ratio <= 0.5:
		return 80
	case ratio <= 0.75:
		return 60
	case ratio <= 1.0:
		return 40
	default:
		return 20
	}
}

func computeRunwayScore(runwayDays int) int {
	switch {
	case runwayDays >= 90:
		return 100
	case runwayDays >= 60:
		return 80
	case runwayDays >= 30:
		return 60
	case runwayDays >= 14:
		return 40
	default:
		return 20
	}
}

func computeStabilityScore(state *entities.MiriamMoneyState) int {
	monthlySpend := state.MonthlySpend
	if monthlySpend.IsZero() {
		monthlySpend = state.RecurringSpendMonthly
	}
	if monthlySpend.IsZero() {
		return 60
	}
	// Stash as percentage of actual monthly spend — how many months of expenses
	// are covered by savings.
	stabilityRatio := state.StashBalance.Div(monthlySpend).Mul(decimal.NewFromInt(100)).InexactFloat64()
	switch {
	case stabilityRatio >= 600:
		return 100
	case stabilityRatio >= 300:
		return 80
	case stabilityRatio >= 100:
		return 60
	case stabilityRatio >= 50:
		return 40
	default:
		return 20
	}
}

func generateHealthReasoning(state *entities.MiriamMoneyState) string {
	runway := state.LiquidityRunwayDays
	budgetScore := computeBudgetScore(state)
	savingScore := computeSavingsScore(state)
	debtScore := computeDebtScore(state)
	return fmt.Sprintf(
		"Runway: %d days. Savings score: %d/100. Budget score: %d/100. Debt score: %d/100. Confidence: %s.",
		runway, savingScore, budgetScore, debtScore, state.ConfidenceLevel,
	)
}
