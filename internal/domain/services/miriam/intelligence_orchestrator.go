package miriam

import (
	"context"
	"encoding/json"
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
	// miriamMandatesGated counts autonomous mandate evaluations skipped because
	// the user's control level does not permit autonomous execution. The reason
	// label distinguishes guided/monitor/unknown so we can see the mix.
	miriamMandatesGated = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "miriam_mandates_gated_total",
		Help: "Autonomous mandate evaluations skipped due to control level",
	}, []string{"reason"})
)

// ControlLevelReader reads a user's autonomy/control level ("full", "guided",
// "monitor"). Only "full" permits autonomous money movement.
type ControlLevelReader interface {
	GetControlLevel(ctx context.Context, userID uuid.UUID) (string, error)
}

// Mandate action types (canonical definitions in entities package).
const (
	MiriamMandateTransferToStash  = entities.MiriamMandateTransferToStash
	MiriamMandateTransferToSpend  = entities.MiriamMandateTransferToSpend
	MiriamMandateBillReservation  = entities.MiriamMandateBillReservation
	MiriamMandateSpendCooldown    = entities.MiriamMandateSpendCooldown
	MiriamMandateGoalContribution = entities.MiriamMandateGoalContribution
	MiriamMandateStashTopUp       = entities.MiriamMandateStashTopUp
	MiriamMandateIdleSweep        = entities.MiriamMandateIdleSweep
)

// IntelligenceOrchestrator is Miriam's unified brain — a single evaluation pass
// that coordinates predictions, decisions, memory, learning, and actions.
type IntelligenceOrchestrator struct {
	service       *Service
	decisions     *DecisionEngine
	nudges        *ProactiveNudgeEngine
	predictions   *PredictiveEngine
	signals       *SignalDetector
	suggestions   *MandateSuggestionEngine
	obDetector    *ObligationAutoDetector
	enricher      *TransactionEnricher
	patternAnalyzer *TransactionPatternAnalyzer
	dispatcher    *NotificationDispatcher
	memory        MemoryReader
	notifier      Notifier
	healthScore   *HealthScoreTracker
	outcomeTrack  *OutcomeTracker
	selfReview    *SelfReviewEngine
	controlLevel  ControlLevelReader
	logger        *zap.Logger
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

// SetPatternAnalyzer injects a TransactionPatternAnalyzer after construction (deferred wiring).
func (o *IntelligenceOrchestrator) SetPatternAnalyzer(a *TransactionPatternAnalyzer) {
	o.patternAnalyzer = a
}

// SetSelfReview injects the self-review engine after construction (deferred wiring).
func (o *IntelligenceOrchestrator) SetSelfReview(s *SelfReviewEngine) {
	o.selfReview = s
}

// SetControlLevel injects the control-level reader after construction (deferred
// wiring). When set, autonomous mandate execution only runs for users in Full
// Autopilot; guided/monitor users still receive predictions, nudges, and
// suggestions but no money moves without their explicit approval.
func (o *IntelligenceOrchestrator) SetControlLevel(r ControlLevelReader) {
	o.controlLevel = r
}

// RunSelfReview runs Miriam's meta-review of her own recent work for a user. It
// self-gates to once per day, so it is safe to call on every worker tick.
func (o *IntelligenceOrchestrator) RunSelfReview(ctx context.Context, userID uuid.UUID) error {
	if o.selfReview == nil {
		return nil
	}
	_, err := o.selfReview.Run(ctx, userID)
	return err
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

	// 2. Enrich transactions early so predictions, decisions, and nudges
	// have access to fresh categorization data this cycle.
	if o.enricher != nil && o.enricher.transactions != nil && eventType == EventWorkerSweep {
		txns, txnErr := o.enricher.transactions.GetUserTransactions(ctx, userID, 50, 0)
		if txnErr == nil && len(txns) > 0 {
			enriched, err := o.enricher.EnrichBatch(ctx, txns)
			if err != nil && o.logger != nil {
				o.logger.Debug("transaction enrichment batch failed", zap.String("user_id", userID.String()), zap.Error(err))
			}

			// Bridge enrichment facts directly from results into miriam_user_facts
			// so Miriam's memory ranker can surface them without tool calls.
			if o.enricher.store != nil && len(enriched) > 0 {
				var allFacts []entities.TransactionFact
				for _, et := range enriched {
					if len(et.Facts) == 0 {
						continue
					}
					var facts []entities.TransactionFact
					if err := json.Unmarshal(et.Facts, &facts); err == nil {
						allFacts = append(allFacts, facts...)
					}
				}
				if len(allFacts) > 0 {
					if n, bridgeErr := o.enricher.store.BridgeFactsToMemory(ctx, userID, allFacts); bridgeErr == nil && n > 0 && o.logger != nil {
						o.logger.Debug("enrichment facts bridged to memory",
							zap.String("user_id", userID.String()), zap.Int("count", n))
					}
				}
			}
		}
	}

	// 2b. Analyze transfer patterns — family detection, recurring recipients,
	// behavioral clusters. Runs after enrichment so counterparty names are resolved.
	if o.patternAnalyzer != nil && eventType == EventWorkerSweep {
		patterns, patErr := o.patternAnalyzer.AnalyzePatterns(ctx, userID)
		if patErr != nil && o.logger != nil {
			o.logger.Debug("pattern analysis failed", zap.String("user_id", userID.String()), zap.Error(patErr))
		} else if patterns != nil && patterns.TotalRecipients > 0 {
			// Store pattern summary as a fact so Miriam can reference it
			if o.enricher != nil && o.enricher.store != nil && patterns.Summary != "" {
				fact := entities.TransactionFact{
					Type:       "transfer_pattern",
					Value:      patterns.Summary,
					Confidence: 0.9,
					Category:   "financial_behavior",
				}
				if _, err := o.enricher.store.BridgeFactsToMemory(ctx, userID, []entities.TransactionFact{fact}); err == nil && o.logger != nil {
					o.logger.Debug("transfer patterns stored as memory fact",
						zap.String("user_id", userID.String()),
						zap.Int("recipients", patterns.TotalRecipients))
				}
			}
		}
	}

	// 3. Generate predictions
	predictions, err := o.predictions.GeneratePredictions(ctx, userID, state)
	if err != nil && o.logger != nil {
		o.logger.Warn("prediction generation failed", zap.String("user_id", userID.String()), zap.Error(err))
	}
	result.Predictions = predictions

	// 3b. Record prediction outcomes (pending) and evaluate expired ones. When a
	// prediction resolves, close the loop with the user so Miriam is accountable
	// for what she said rather than only ever raising new alarms.
	if o.outcomeTrack != nil {
		if predictions != nil {
			o.outcomeTrack.RecordPredictions(ctx, userID, predictions.ActivePredictions)
		}
		resolved := o.outcomeTrack.EvaluateOutcomes(ctx, userID)
		if o.notifier != nil {
			for _, out := range resolved {
				if msg := LoopClosingMessage(out); msg != "" {
					_ = o.notifier.SendGenericNotification(ctx, userID, "Miriam", msg)
					break // one loop-closing line per sweep — she's discreet, not chatty
				}
			}
		}
	}

	// 4. Detect and upsert context signals
	if o.signals != nil {
		if err := o.signals.DetectAndUpsert(ctx, userID); err != nil && o.logger != nil {
			o.logger.Debug("signal detection failed", zap.String("user_id", userID.String()), zap.Error(err))
		}
	}

	// 5. Load memory context
	var memoryFacts []entities.MiriamUserFact
	if o.memory != nil {
		facts, err := o.memory.GetActiveFacts(ctx, userID)
		if err == nil {
			memoryFacts = filterActionRelevantFacts(facts)
		}
	}

	// 5b. Get learning bias
	learningBias := decimal.Zero
	if o.service.repo != nil {
		bias, err := o.service.repo.RecentLearningBias(ctx, userID, time.Now().UTC().AddDate(0, -1, 0))
		if err == nil {
			learningBias = bias
		}
	}

	// 5. Evaluate mandates with decision engine.
	//
	// Autonomous money movement is gated on the user's control level: only Full
	// Autopilot ("full") permits Miriam to execute mandates on her own. In guided
	// or monitor mode the user has explicitly asked to approve actions first (or to
	// be advised only), so we skip execution entirely while still running every
	// advisory output above and below (predictions, nudges, suggestions, health,
	// loop-closing). This mirrors AutopilotService.loadFullAutopilotUsers, so both
	// autonomous paths honour the same contract. Fail closed: on any error reading
	// the level, we do NOT execute.
	decisionsMade := 0
	actionsExecuted := 0
	mandateEvent := eventType == EventWorkerSweep || eventType == EventIncomeLowerThanUsual
	gateReason := ""
	if mandateEvent {
		gateReason = o.autonomyGateReason(ctx, userID)
	}
	if mandateEvent && gateReason != "" {
		miriamMandatesGated.WithLabelValues(gateReason).Inc()
		if o.logger != nil {
			o.logger.Debug("miriam: autonomous mandate execution gated by control level",
				zap.String("user_id", userID.String()), zap.String("reason", gateReason))
		}
	}
	if mandateEvent && gateReason == "" {
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
			"risk_score": func() int {
				if result.Predictions != nil {
					return result.Predictions.RiskScore
				}
				return 0
			}(),
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

// autonomyGateReason decides whether autonomous mandate execution may run for a
// user. It returns "" when execution is permitted (Full Autopilot) and a short
// reason label otherwise, used for metrics and logs. It fails closed: if no
// control-level reader is wired or the lookup errors, execution is blocked so a
// transient read failure can never move a user's money without their consent.
func (o *IntelligenceOrchestrator) autonomyGateReason(ctx context.Context, userID uuid.UUID) string {
	if o.controlLevel == nil {
		return "unwired"
	}
	lvlCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	level, err := o.controlLevel.GetControlLevel(lvlCtx, userID)
	if err != nil {
		return "lookup_error"
	}
	switch level {
	case entities.ControlLevelFull:
		return ""
	case entities.ControlLevelGuided:
		return "guided"
	case entities.ControlLevelMonitor:
		return "monitor"
	default:
		// Unknown/blank value — fail closed rather than assume full autonomy.
		return "unknown"
	}
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
		if err = o.recordBillReservation(ctx, userID, amount, mandate); err == nil {
			if o.notifier != nil {
				_ = o.notifier.SendGenericNotification(ctx, userID, "Miriam", fmt.Sprintf("$%s noted for upcoming bills.", amount.StringFixed(2)))
			}
		}
	case MiriamMandateSpendCooldown:
		o.recordReceipt(ctx, userID, amount, mandate, "spend_cooldown", "Spending cooldown activated per mandate.")
		if o.notifier != nil {
			_ = o.notifier.SendGenericNotification(ctx, userID, "Miriam", "Spending cooldown is on. Paused non-essential spending for now.")
		}
	case MiriamMandateGoalContribution:
		if err = o.executeTransferToStash(ctx, userID, amount, mandate.ID); err == nil {
			o.recordReceipt(ctx, userID, amount, mandate, "goal_contribution", fmt.Sprintf("Auto-contributed $%s to savings goals.", amount.StringFixed(2)))
			if o.notifier != nil {
				_ = o.notifier.SendGenericNotification(ctx, userID, "Miriam", fmt.Sprintf("$%s moved to Stash for your savings goals.", amount.StringFixed(2)))
			}
		}
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
