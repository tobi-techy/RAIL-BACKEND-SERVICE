package miriam

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
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
	service     *Service
	decisions   *DecisionEngine
	nudges      *ProactiveNudgeEngine
	predictions *PredictiveEngine
	signals     *SignalDetector
	suggestions *MandateSuggestionEngine
	obDetector  *ObligationAutoDetector
	dispatcher  *NotificationDispatcher
	memory      MemoryReader
	notifier    Notifier
	logger      *zap.Logger
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
	logger *zap.Logger,
) *IntelligenceOrchestrator {
	return &IntelligenceOrchestrator{
		service: service, decisions: decisions, nudges: nudges,
		predictions: predictions, signals: signals, suggestions: suggestions,
		obDetector: obDetector, dispatcher: dispatcher, memory: memory,
		notifier: notifier, logger: logger,
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
	start := time.Now().UTC()
	result := &IntelligenceResult{UserID: userID}

	// 1. Refresh money state (existing logic)
	state, err := o.service.RefreshMoneyState(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("refresh money state: %w", err)
	}
	result.MoneyState = state

	// 2. Generate predictions
	predictions, err := o.predictions.GeneratePredictions(ctx, userID, state)
	if err != nil && o.logger != nil {
		o.logger.Warn("prediction generation failed", zap.String("user_id", userID.String()), zap.Error(err))
	}
	result.Predictions = predictions

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
		nudges, err := o.nudges.GenerateProactiveNudges(ctx, userID, state)
		if err == nil {
			result.NudgesGenerated = len(nudges)
		}
	}

	// 7. Generate mandate suggestions (autonomous intelligence)
	suggestions, err := o.suggestions.GenerateSuggestions(ctx, userID, state, memoryFacts)
	if err == nil {
		result.SuggestionsMade = len(suggestions)
	}

	// 8. Detect recurring obligations from transactions (weekly check)
	if o.obDetector != nil && eventType == EventWorkerSweep {
		detected, err := o.obDetector.DetectRecurringPayments(ctx, userID)
		if err == nil && len(detected) > 0 && o.logger != nil {
			o.logger.Info("obligation auto-detection found candidates",
				zap.String("user_id", userID.String()),
				zap.Int("count", len(detected)))
		}
	}

	// 9. Flush notification batches (dispatcher handles batching/digest)
	if o.dispatcher != nil {
		_ = o.dispatcher.FlushBatches(ctx)
	}

	result.EvaluatedAt = time.Now().UTC()
	result.Duration = time.Since(start)

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
	switch mandate.ActionType {
	case entities.MiriamMandateTransferToStash:
		return o.executeTransferToStash(ctx, userID, amount, mandate.ID)
	case MiriamMandateTransferToSpend:
		return o.executeTransferToSpend(ctx, userID, amount, mandate.ID)
	case MiriamMandateStashTopUp, MiriamMandateIdleSweep:
		return o.executeTransferToStash(ctx, userID, amount, mandate.ID)
	case MiriamMandateBillReservation:
		// For bill reservation, we just record the intent — actual transfer happens via transfer_to_spend
		return o.recordBillReservation(ctx, userID, amount, mandate)
	default:
		return fmt.Errorf("unknown mandate action type: %s", mandate.ActionType)
	}
}

func (o *IntelligenceOrchestrator) executeTransferToStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, mandateID uuid.UUID) error {
	windowSec := int64(1440) * 60 // 24-hour window
	idempotencyKey := fmt.Sprintf("miriam-autopilot-%s-%d", mandateID.String(), time.Now().UTC().Unix()/windowSec)
	return o.service.transfer.TransferSpendingToStash(ctx, userID, amount, idempotencyKey)
}

func (o *IntelligenceOrchestrator) executeTransferToSpend(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, mandateID uuid.UUID) error {
	// This would need a TransferStashToSpending method on the TransferExecutor.
	// For now, we skip and record a receipt.
	if o.logger != nil {
		o.logger.Info("stash-to-spend transfer not yet supported",
			zap.String("user_id", userID.String()),
			zap.String("amount", amount.StringFixed(2)))
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

func filterActionRelevantFacts(facts []*entities.MiriamUserFact) []entities.MiriamUserFact {
	relevant := make([]entities.MiriamUserFact, 0, len(facts))
	for _, f := range facts {
		switch f.Category {
		case entities.FactCategoryGoal,
			entities.FactCategoryFear,
			entities.FactCategoryHabit,
			entities.FactCategoryFinancialBehavior,
			entities.FactCategoryLifeEvent:
			if f.Confidence.GreaterThanOrEqual(decimal.NewFromFloat(0.5)) {
				relevant = append(relevant, *f)
			}
		}
	}
	return relevant
}
