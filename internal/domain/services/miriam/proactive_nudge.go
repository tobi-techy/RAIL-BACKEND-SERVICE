package miriam

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// ProactiveNudgeStore persists proactive nudges.
type ProactiveNudgeStore interface {
	CreateNudge(ctx context.Context, n *entities.ProactiveNudge) error
	ListPendingNudges(ctx context.Context, userID uuid.UUID) ([]entities.ProactiveNudge, error)
	MarkDelivered(ctx context.Context, nudgeID uuid.UUID) error
	MarkDismissed(ctx context.Context, nudgeID uuid.UUID) error
	ExpireOldNudges(ctx context.Context, before time.Time) (int64, error)
	HasRecentNudgeByType(ctx context.Context, userID uuid.UUID, triggerType string, since time.Time) (bool, error)
}

// ProactiveNudgeEngine generates context-aware push notifications.
type ProactiveNudgeEngine struct {
	store       ProactiveNudgeStore
	predictions *PredictiveEngine
	memory      MemoryReader
	notifier    Notifier
	logger      *zap.Logger
}

// NewProactiveNudgeEngine creates a proactive nudge engine.
func NewProactiveNudgeEngine(
	store ProactiveNudgeStore,
	predictions *PredictiveEngine,
	memory MemoryReader,
	notifier Notifier,
	logger *zap.Logger,
) *ProactiveNudgeEngine {
	return &ProactiveNudgeEngine{
		store: store, predictions: predictions, memory: memory, notifier: notifier, logger: logger,
	}
}

// SetMemory injects a MemoryReader after construction (deferred wiring).
func (e *ProactiveNudgeEngine) SetMemory(m MemoryReader) {
	e.memory = m
}

// SetNotifier injects a Notifier after construction (deferred wiring).
func (e *ProactiveNudgeEngine) SetNotifier(n Notifier) {
	e.notifier = n
}

// GenerateProactiveNudges evaluates a user's state and produces 0–3 nudges.
func (e *ProactiveNudgeEngine) GenerateProactiveNudges(ctx context.Context, userID uuid.UUID, state *entities.MiriamMoneyState) ([]entities.ProactiveNudge, error) {
	summary, err := e.predictions.GeneratePredictions(ctx, userID, state)
	if err != nil {
		return nil, err
	}
	return e.generateFromSummary(ctx, userID, state, summary)
}

// GenerateProactiveNudgesWithPredictions generates nudges using pre-computed predictions,
// avoiding duplicate prediction generation.
func (e *ProactiveNudgeEngine) GenerateProactiveNudgesWithPredictions(ctx context.Context, userID uuid.UUID, state *entities.MiriamMoneyState, predictions *entities.PredictionSummary) ([]entities.ProactiveNudge, error) {
	return e.generateFromSummary(ctx, userID, state, predictions)
}

func (e *ProactiveNudgeEngine) generateFromSummary(ctx context.Context, userID uuid.UUID, state *entities.MiriamMoneyState, summary *entities.PredictionSummary) ([]entities.ProactiveNudge, error) {

	var nudges []entities.ProactiveNudge

	// 1. Prediction-triggered nudges (highest priority)
	for _, p := range summary.ActivePredictions {
		if p.Probability.LessThan(decimal.NewFromFloat(0.5)) {
			continue
		}
		n := e.nudgeFromPrediction(userID, p)
		if n != nil {
			nudges = append(nudges, *n)
		}
	}

	// 2. Memory-triggered nudges
	if e.memory != nil {
		facts, err := e.memory.GetActiveFacts(ctx, userID)
		if err == nil {
			if n := e.nudgeFromMemory(userID, facts, state); n != nil {
				nudges = append(nudges, *n)
			}
		}
	}

	// 3. Bill warning nudges
	if n := e.nudgeFromBills(userID, state); n != nil {
		nudges = append(nudges, *n)
	}

	// Select top 3 by priority, deduplicate by trigger type
	nudges = selectTopNudges(nudges, 3)

	dedupWindow := 12 * time.Hour

	for i := range nudges {
		if e.store != nil {
			same, err := e.store.HasRecentNudgeByType(ctx, userID, nudges[i].TriggerType, time.Now().UTC().Add(-dedupWindow))
			if err == nil && same {
				continue
			}
			if err := e.store.CreateNudge(ctx, &nudges[i]); err != nil && e.logger != nil {
				e.logger.Warn("nudge creation failed", zap.Error(err))
				continue
			}
		}
		if e.notifier != nil {
			_ = e.notifier.SendGenericNotification(ctx, userID, "Miriam", nudges[i].Message)
			if e.store != nil {
				_ = e.store.MarkDelivered(ctx, nudges[i].ID)
			}
		}
	}

	return nudges, nil
}

// GetPendingNudges returns undelivered nudges for a user.
func (e *ProactiveNudgeEngine) GetPendingNudges(ctx context.Context, userID uuid.UUID) (*entities.ProactiveNudgeSummary, error) {
	if e.store == nil {
		return &entities.ProactiveNudgeSummary{}, nil
	}
	nudges, err := e.store.ListPendingNudges(ctx, userID)
	if err != nil {
		return nil, err
	}

	summary := &entities.ProactiveNudgeSummary{
		Nudges: nudges,
		Count:  len(nudges),
	}
	for _, n := range nudges {
		if n.Priority > summary.Highest {
			summary.Highest = n.Priority
		}
	}
	return summary, nil
}

func (e *ProactiveNudgeEngine) nudgeFromPrediction(userID uuid.UUID, p entities.MiriamPrediction) *entities.ProactiveNudge {
	if p.Severity == entities.SeverityLow && p.Probability.LessThan(decimal.NewFromFloat(0.6)) {
		return nil
	}

	priority := predictionPriority(p)
	message := predictionMessage(p)
	action := predictionAction(p)

	return &entities.ProactiveNudge{
		ID:               uuid.New(),
		UserID:           userID,
		TriggerType:      entities.NudgeTriggerPrediction,
		Priority:         priority,
		Message:          message,
		ActionSuggestion: mustJSON(action),
		ExpiresAt:        time.Now().UTC().Add(predictionHorizonDuration(p.Horizon)),
		CreatedAt:        time.Now().UTC(),
	}
}

func (e *ProactiveNudgeEngine) nudgeFromMemory(userID uuid.UUID, facts []*entities.MiriamUserFact, state *entities.MiriamMoneyState) *entities.ProactiveNudge {
	for _, f := range facts {
		if f.Category == entities.FactCategoryGoal && f.Confidence.GreaterThanOrEqual(decimal.NewFromFloat(0.7)) {
			// User has a goal — check if stash progress aligns
			if state.StashTarget.IsPositive() {
				stash, _ := e.predictions.balances.GetAccountBalance(context.Background(), userID, entities.AccountTypeStashBalance)
				if stash.LessThan(state.StashTarget.Mul(decimal.NewFromFloat(0.5))) {
					return &entities.ProactiveNudge{
						ID:          uuid.New(),
						UserID:      userID,
						TriggerType: entities.NudgeTriggerMemory,
						Priority:    5,
						Message:     fmt.Sprintf("Your savings goal is halfway there. Want to boost your Stash this week?"),
						ExpiresAt:   time.Now().UTC().Add(7 * 24 * time.Hour),
						CreatedAt:   time.Now().UTC(),
					}
				}
			}
		}
	}
	return nil
}

func (e *ProactiveNudgeEngine) nudgeFromBills(userID uuid.UUID, state *entities.MiriamMoneyState) *entities.ProactiveNudge {
	if !state.UpcomingObligations.IsPositive() {
		return nil
	}

	spend, err := e.predictions.balances.GetAccountBalance(context.Background(), userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return nil
	}

	if spend.LessThan(state.UpcomingObligations.Mul(decimal.NewFromFloat(0.8))) {
		gap := state.UpcomingObligations.Sub(spend)
		return &entities.ProactiveNudge{
			ID:          uuid.New(),
			UserID:      userID,
			TriggerType: entities.NudgeTriggerBillWarning,
			Priority:    9,
			Message: fmt.Sprintf("Upcoming bills ($%s) may exceed your Spend balance ($%s). $%s gap — want me to cover from Stash?",
				state.UpcomingObligations.StringFixed(0), spend.StringFixed(0), gap.StringFixed(0)),
			ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
			CreatedAt: time.Now().UTC(),
		}
	}

	return nil
}

func predictionPriority(p entities.MiriamPrediction) int {
	base := 3
	switch p.Severity {
	case entities.SeverityCritical:
		base = 9
	case entities.SeverityHigh:
		base = 7
	case entities.SeverityMedium:
		base = 5
	case entities.SeverityLow:
		base = 3
	}

	prob := p.Probability.InexactFloat64()
	if prob > 0.8 {
		base++
	}
	if prob < 0.4 {
		base--
	}

	if base > 10 {
		return 10
	}
	return base
}

func predictionMessage(p entities.MiriamPrediction) string {
	var b strings.Builder
	switch p.PredictionType {
	case entities.PredictionCashShortfall:
		b.WriteString("Heads up — you might run low before payday. ")
	case entities.PredictionBillPressure:
		b.WriteString("Bills coming up that need attention. ")
	case entities.PredictionIncomeGap:
		b.WriteString("Income this month may not cover your needs. ")
	case entities.PredictionSpendingAnomaly:
		b.WriteString("Unusual spending detected this month. ")
	case entities.PredictionIdleSurplus:
		b.WriteString("You've got idle money that could be earning yield. ")
	case entities.PredictionStashOpportunity:
		b.WriteString("Your Stash could use a top-up to stay on target. ")
	default:
		b.WriteString("Something to know about your money. ")
	}

	if p.Reasoning != "" {
		b.WriteString(p.Reasoning)
	}

	return b.String()
}

func predictionAction(p entities.MiriamPrediction) map[string]interface{} {
	switch p.PredictionType {
	case entities.PredictionCashShortfall, entities.PredictionBillPressure:
		return map[string]interface{}{
			"type":   "transfer_from_stash",
			"label":  "Cover from Stash",
			"amount": p.ProjectedAmount.StringFixed(2),
		}
	case entities.PredictionIdleSurplus, entities.PredictionStashOpportunity:
		return map[string]interface{}{
			"type":   "transfer_to_stash",
			"label":  "Move to Stash",
			"amount": p.ProjectedAmount.StringFixed(2),
		}
	default:
		return nil
	}
}

func selectTopNudges(nudges []entities.ProactiveNudge, max int) []entities.ProactiveNudge {
	if len(nudges) <= max {
		return nudges
	}

	sort.Slice(nudges, func(i, j int) bool {
		return nudges[i].Priority > nudges[j].Priority
	})

	// Deduplicate by trigger type
	seen := make(map[string]bool)
	result := make([]entities.ProactiveNudge, 0, max)
	for _, n := range nudges {
		if !seen[n.TriggerType] {
			seen[n.TriggerType] = true
			result = append(result, n)
		}
		if len(result) >= max {
			break
		}
	}

	return result
}
