package miriam

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

var rng = rand.New(rand.NewSource(time.Now().UnixNano()))

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
	balances    BalanceProvider
	memory      MemoryReader
	notifier    Notifier
	logger      *zap.Logger
}

// NewProactiveNudgeEngine creates a proactive nudge engine.
func NewProactiveNudgeEngine(
	store ProactiveNudgeStore,
	predictions *PredictiveEngine,
	balances BalanceProvider,
	memory MemoryReader,
	notifier Notifier,
	logger *zap.Logger,
) *ProactiveNudgeEngine {
	return &ProactiveNudgeEngine{
		store: store, predictions: predictions, balances: balances, memory: memory, notifier: notifier, logger: logger,
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
		n := e.nudgeFromPrediction(ctx, userID, p, state)
		if n != nil {
			nudges = append(nudges, *n)
		}
	}

	// 2. Memory-triggered nudges
	if e.memory != nil {
		facts, err := e.memory.GetActiveFacts(ctx, userID)
		if err == nil {
			if n := e.nudgeFromMemory(ctx, userID, facts, state); n != nil {
				nudges = append(nudges, *n)
			}
		}
	}

	// 3. Bill warning nudges
	if n := e.nudgeFromBills(ctx, userID, state); n != nil {
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

func (e *ProactiveNudgeEngine) nudgeFromPrediction(ctx context.Context, userID uuid.UUID, p entities.MiriamPrediction, state *entities.MiriamMoneyState) *entities.ProactiveNudge {
	if p.Severity == entities.SeverityLow && p.Probability.LessThan(decimal.NewFromFloat(0.6)) {
		return nil
	}

	priority := predictionPriority(p)
	message := e.buildPredictionMessage(ctx, userID, p, state)
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

func (e *ProactiveNudgeEngine) nudgeFromMemory(ctx context.Context, userID uuid.UUID, facts []*entities.MiriamUserFact, state *entities.MiriamMoneyState) *entities.ProactiveNudge {
	for _, f := range facts {
		if f.Category == entities.FactCategoryGoal && f.Confidence.GreaterThanOrEqual(decimal.NewFromFloat(0.7)) {
			if !state.StashTarget.IsPositive() {
				continue
			}
			stash, err := e.balances.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
			if err != nil {
				if e.logger != nil {
					e.logger.Warn("nudgeFromMemory: stash balance fetch failed", zap.Error(err))
				}
				continue
			}
			spend, err := e.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
			if err != nil {
				if e.logger != nil {
					e.logger.Warn("nudgeFromMemory: spend balance fetch failed", zap.Error(err))
				}
				continue
			}

			remaining := state.StashTarget.Sub(stash)
			if remaining.LessThan(decimal.NewFromFloat(1)) {
				continue
			}

			pct := int(stash.Div(state.StashTarget).Mul(decimal.NewFromFloat(100)).InexactFloat64())
			target := state.StashTarget.StringFixed(0)
			s := spend.StringFixed(0)
			st := stash.StringFixed(0)
			rem := remaining.StringFixed(0)

			var msg string
			switch rng.Intn(3) {
			case 0:
				msg = fmt.Sprintf("You're %d%% of the way to your $%s goal with $%s in Spend. Add more?", pct, target, s)
			case 1:
				msg = fmt.Sprintf("Need $%s more for your $%s savings goal. Spend has $%s — tap to move it.", rem, target, s)
			default:
				msg = fmt.Sprintf("Stash goal: $%s total. Saved $%s, need $%s more. Boost from Spend?", target, st, rem)
			}

			return &entities.ProactiveNudge{
				ID:          uuid.New(),
				UserID:      userID,
				TriggerType: entities.NudgeTriggerMemory,
				Priority:    5,
				Message:     msg,
				ActionSuggestion: mustJSON(map[string]interface{}{
					"type":   "transfer_to_stash",
					"label":  "Add to Stash",
					"amount": remaining.StringFixed(2),
				}),
				ExpiresAt: time.Now().UTC().Add(7 * 24 * time.Hour),
				CreatedAt: time.Now().UTC(),
			}
		}
	}
	return nil
}

func (e *ProactiveNudgeEngine) nudgeFromBills(ctx context.Context, userID uuid.UUID, state *entities.MiriamMoneyState) *entities.ProactiveNudge {
	if !state.UpcomingObligations.IsPositive() {
		return nil
	}

	spend, err := e.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		if e.logger != nil {
			e.logger.Warn("nudgeFromBills: spend balance fetch failed", zap.Error(err))
		}
		return nil
	}

	if spend.LessThan(state.UpcomingObligations.Mul(decimal.NewFromFloat(0.8))) {
		gap := state.UpcomingObligations.Sub(spend)
		stash, err := e.balances.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
		if err != nil {
			if e.logger != nil {
				e.logger.Warn("nudgeFromBills: stash balance fetch failed", zap.Error(err))
			}
			stash = decimal.Zero
		}
		ob := state.UpcomingObligations.StringFixed(0)
		s := spend.StringFixed(0)
		st := stash.StringFixed(0)
		g := gap.StringFixed(0)

		var msg string
		switch rng.Intn(3) {
		case 0:
			msg = fmt.Sprintf("Bills ($%s) exceed Spend ($%s). Stash has $%s — tap to cover the $%s gap.", ob, s, st, g)
		case 1:
			msg = fmt.Sprintf("⚠️ $%s in bills due. Spend has $%s. I can pull $%s from Stash.", ob, s, g)
		default:
			msg = fmt.Sprintf("Upcoming bills of $%s will overdraw Spend ($%s). Stash ($%s) can cover — want me to?", ob, s, st)
		}

		return &entities.ProactiveNudge{
			ID:          uuid.New(),
			UserID:      userID,
			TriggerType: entities.NudgeTriggerBillWarning,
			Priority:    9,
			Message:     msg,
			ActionSuggestion: mustJSON(map[string]interface{}{
				"type":   "transfer_from_stash",
				"label":  "Cover from Stash",
				"amount": gap.StringFixed(2),
			}),
			ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
			CreatedAt: time.Now().UTC(),
		}
	}

	return nil
}

func (e *ProactiveNudgeEngine) buildPredictionMessage(ctx context.Context, userID uuid.UUID, p entities.MiriamPrediction, state *entities.MiriamMoneyState) string {
	spend, err := e.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		if e.logger != nil {
			e.logger.Warn("buildPredictionMessage: spend balance fetch failed", zap.Error(err))
		}
		spend = decimal.Zero
	}
	stash, err := e.balances.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		if e.logger != nil {
			e.logger.Warn("buildPredictionMessage: stash balance fetch failed", zap.Error(err))
		}
		stash = decimal.Zero
	}

	s := spend.StringFixed(0)
	st := stash.StringFixed(0)
	pa := p.ProjectedAmount.StringFixed(0)

	switch p.PredictionType {
	case entities.PredictionCashShortfall:
		ob := state.UpcomingObligations.StringFixed(0)
		switch rng.Intn(4) {
		case 0:
			return fmt.Sprintf("Spend is at $%s with $%s in bills due. You're $%s short — tap to cover from Stash.", s, ob, pa)
		case 1:
			return fmt.Sprintf("Heads up: $%s in Spend, $%s in bills. I can move $%s from Stash.", s, ob, pa)
		case 2:
			return fmt.Sprintf("$%s in bills coming, Spend at $%s. Want me to pull $%s from Stash?", ob, s, pa)
		default:
			return fmt.Sprintf("Paycheck's not here yet. Spend ($%s) won't cover $%s in bills. I can fill the $%s gap.", s, ob, pa)
		}

	case entities.PredictionBillPressure:
		ob := state.UpcomingObligations.StringFixed(0)
		switch rng.Intn(4) {
		case 0:
			return fmt.Sprintf("$%s in bills this week vs $%s in Spend. Auto-cover from Stash?", ob, s)
		case 1:
			return fmt.Sprintf("Your $%s in obligations are crowding Spend ($%s). Stash has $%s — let me top up.", ob, s, st)
		case 2:
			return fmt.Sprintf("$%s due soon, Spend at $%s. Stash has $%s — move enough to cover?", ob, s, st)
		default:
			return fmt.Sprintf("Bill cluster: $%s in obligations, $%s in Spend. Stash ($%s) can smooth it out.", ob, s, st)
		}

	case entities.PredictionIncomeGap:
		inc := state.AvgMonthlyIncome.StringFixed(0)
		switch rng.Intn(3) {
		case 0:
			return fmt.Sprintf("Income this month ($%s) is $%s short of expenses. Pull from Stash?", inc, pa)
		case 1:
			return fmt.Sprintf("Your $%s/mo income won't cover this month. Stash can bridge the $%s gap.", inc, pa)
		default:
			return fmt.Sprintf("Spending $%s more than you earn this month. Move from Stash to cover?", pa)
		}

	case entities.PredictionSpendingAnomaly:
		safe := state.SafeToSpendDaily.StringFixed(0)
		runway := state.LiquidityRunwayDays
		switch rng.Intn(3) {
		case 0:
			return fmt.Sprintf("You've spent $%s more this month. Daily safe-to-spend is $%s — try easing up.", pa, safe)
		case 1:
			return fmt.Sprintf("Spending is up $%s vs normal. At this pace your runway is %d days.", pa, runway)
		default:
			return fmt.Sprintf("Unusual spending this month: $%s above normal. I'm watching it.", pa)
		}

	case entities.PredictionIdleSurplus:
		switch rng.Intn(3) {
		case 0:
			return fmt.Sprintf("$%s sitting idle in Spend. Move it to Stash and put it to work.", pa)
		case 1:
			return fmt.Sprintf("$%s in Spend isn't earning anything. Tap to stash it.", pa)
		default:
			return fmt.Sprintf("Your Spend has $%s extra. Want to move it to Stash?", pa)
		}

	case entities.PredictionStashOpportunity:
		target := state.StashTarget.StringFixed(0)
		switch rng.Intn(3) {
		case 0:
			return fmt.Sprintf("Stash ($%s) is $%s short of $%s target. Add from Spend?", st, pa, target)
		case 1:
			if state.StashTarget.IsPositive() {
				pct := int(stash.Div(state.StashTarget).Mul(decimal.NewFromFloat(100)).InexactFloat64())
				return fmt.Sprintf("You're at %d%% of your Stash goal. Add $%s from Spend to stay on track.", pct, pa)
			}
			return fmt.Sprintf("Add $%s to your Stash from Spend to stay on target.", pa)
		default:
			return fmt.Sprintf("Stash opportunity: move $%s from Spend to keep your savings growing.", pa)
		}

	default:
		if p.Reasoning != "" {
			return p.Reasoning
		}
		return "Miriam noticed something about your money — tap to see."
	}
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
