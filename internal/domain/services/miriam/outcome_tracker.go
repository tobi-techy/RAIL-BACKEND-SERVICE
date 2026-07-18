package miriam

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// OutcomeTracker records predictions, evaluates whether they materialised,
// and provides accuracy metrics for confidence calibration.
type OutcomeTracker struct {
	repo       Repository
	spending   SpendingProvider
	balances   BalanceProvider
	obligations ObligationProvider
	logger     *zap.Logger
}

func NewOutcomeTracker(
	repo Repository,
	spending SpendingProvider,
	balances BalanceProvider,
	obligations ObligationProvider,
	logger *zap.Logger,
) *OutcomeTracker {
	return &OutcomeTracker{
		repo: repo, spending: spending, balances: balances,
		obligations: obligations, logger: logger,
	}
}

// RecordPredictions persists pending outcomes for a batch of predictions.
// Called right after predictions are generated in the Evaluate pipeline.
func (t *OutcomeTracker) RecordPredictions(ctx context.Context, userID uuid.UUID, predictions []entities.MiriamPrediction) {
	if t.repo == nil || len(predictions) == 0 {
		return
	}

	outcomes := make([]entities.MiriamPredictionOutcome, 0, len(predictions))
	now := time.Now().UTC()

	for _, p := range predictions {
		thresholdData := map[string]interface{}{
			"predicted_amount": p.ProjectedAmount.StringFixed(2),
			"probability":      p.Probability.StringFixed(4),
			"reasoning":        p.Reasoning,
			"data_snapshot":    string(p.DataSnapshot),
		}
		raw, _ := json.Marshal(thresholdData)

		horizon := predictionHorizonDays(p.Horizon)

		outcomes = append(outcomes, entities.MiriamPredictionOutcome{
			ID:                   uuid.New(),
			UserID:               userID,
			PredictionID:         p.ID,
			PredictionType:       p.PredictionType,
			PredictedProbability: p.Probability,
			HorizonDays:          horizon,
			ThresholdData:        raw,
			ActualOutcome:        nil,
			OutcomeObservedAt:    nil,
			CreatedAt:            now,
		})
	}

	if err := t.repo.SavePredictionOutcomes(ctx, outcomes); err != nil && t.logger != nil {
		t.logger.Warn("failed to record prediction outcomes",
			zap.String("user_id", userID.String()), zap.Error(err))
	}
}

// EvaluateOutcomes checks all pending outcomes whose horizon has expired and
// records whether the predicted event actually occurred. It returns the outcomes
// resolved during this sweep so callers can close the loop with the user.
// Called periodically (e.g., once per hour or on each worker sweep).
func (t *OutcomeTracker) EvaluateOutcomes(ctx context.Context, userID uuid.UUID) []entities.MiriamPredictionOutcome {
	if t.repo == nil {
		return nil
	}

	pending, err := t.repo.GetPendingPredictionOutcomes(ctx, userID)
	if err != nil {
		if t.logger != nil {
			t.logger.Warn("failed to fetch pending outcomes",
				zap.String("user_id", userID.String()), zap.Error(err))
		}
		return nil
	}

	now := time.Now().UTC()
	spend := decimal.Zero
	if b, err := t.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance); err == nil {
		spend = b
	}

	var resolved []entities.MiriamPredictionOutcome
	for _, o := range pending {
		deadline := o.CreatedAt.Add(time.Duration(o.HorizonDays) * 24 * time.Hour)
		if now.Before(deadline) {
			continue
		}

		outcome := t.evaluatePrediction(ctx, userID, o, spend)
		if err := t.repo.MarkPredictionOutcome(ctx, o.ID, outcome, now); err != nil && t.logger != nil {
			t.logger.Warn("failed to mark outcome",
				zap.String("user_id", userID.String()),
				zap.String("prediction_id", o.PredictionID.String()),
				zap.Error(err))
			continue
		}
		settled := outcome
		o.ActualOutcome = &settled
		o.OutcomeObservedAt = &now
		resolved = append(resolved, o)
	}
	return resolved
}

func (t *OutcomeTracker) evaluatePrediction(ctx context.Context, userID uuid.UUID, o entities.MiriamPredictionOutcome, currentSpend decimal.Decimal) bool {
	switch o.PredictionType {
	case entities.PredictionCashShortfall:
		upcoming := t.fetchUpcomingObligations(ctx, userID)
		return currentSpend.LessThan(upcoming)

	case entities.PredictionBillPressure:
		imminent := t.fetchImminentObligations(ctx, userID, 7)
		return currentSpend.LessThan(imminent)

	case entities.PredictionIncomeGap:
		now := time.Now().UTC()
		monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		flow, _ := t.spending.GetMoneyFlow(ctx, userID, monthStart, now)
		if flow == nil {
			return false
		}
		// Parse projected_income from the prediction snapshot stored in threshold_data.
		var td struct {
			DataSnapshot string `json:"data_snapshot"`
		}
		if err := json.Unmarshal(o.ThresholdData, &td); err != nil || td.DataSnapshot == "" {
			return false
		}
		var snap struct {
			ProjectedIncome string `json:"projected_income"`
		}
		if err := json.Unmarshal([]byte(td.DataSnapshot), &snap); err != nil || snap.ProjectedIncome == "" {
			return false
		}
		projected, err := decimal.NewFromString(snap.ProjectedIncome)
		if err != nil || !projected.IsPositive() {
			return false
		}
		// Income gap is "true" if current month actual deposits trailed
		// the projected income by more than 30%.
		return flow.TotalDeposits.LessThan(projected.Mul(decimal.NewFromFloat(0.7)))

	case entities.PredictionSpendingAnomaly:
		now := time.Now().UTC()
		monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		lastMonthStart := monthStart.AddDate(0, -1, 0)
		thisMonth, _ := t.spending.GetMoneyFlow(ctx, userID, monthStart, now)
		lastMonth, _ := t.spending.GetMoneyFlow(ctx, userID, lastMonthStart, monthStart)
		if thisMonth == nil || lastMonth == nil {
			return false
		}
		thisOut := totalOut(thisMonth)
		lastOut := totalOut(lastMonth)
		return lastOut.IsPositive() && thisOut.GreaterThan(lastOut.Mul(decimal.NewFromFloat(1.35)))

	case entities.PredictionIdleSurplus:
		return false // idle surplus is not a negative outcome — just informational

	case entities.PredictionStashOpportunity:
		return false // stash opportunity is informational

	default:
		return false
	}
}

// GetHitRate returns the fraction of evaluated predictions that were correct
// for the given user and optional prediction type over the lookback period.
func (t *OutcomeTracker) GetHitRate(ctx context.Context, userID uuid.UUID, predictionType string, since time.Time) float64 {
	if t.repo == nil {
		return 0
	}
	rate, err := t.repo.GetPredictionHitRate(ctx, userID, predictionType, since)
	if err != nil {
		return 0
	}
	return rate
}

func (t *OutcomeTracker) fetchUpcomingObligations(ctx context.Context, userID uuid.UUID) decimal.Decimal {
	if t.obligations == nil {
		return decimal.Zero
	}
	now := time.Now().UTC()
	obligations, err := t.obligations.ListActive(ctx, userID)
	if err != nil {
		return decimal.Zero
	}
	total := decimal.Zero
	for _, o := range obligations {
		if !usdLike(o.Currency) {
			continue
		}
		due := nextDueDate(o, now)
		if due != nil && !due.After(now.AddDate(0, 0, 30)) {
			total = total.Add(o.Amount)
		}
	}
	return total
}

func (t *OutcomeTracker) fetchImminentObligations(ctx context.Context, userID uuid.UUID, days int) decimal.Decimal {
	if t.obligations == nil {
		return decimal.Zero
	}
	now := time.Now().UTC()
	deadline := now.AddDate(0, 0, days)
	obligations, err := t.obligations.ListActive(ctx, userID)
	if err != nil {
		return decimal.Zero
	}
	total := decimal.Zero
	for _, o := range obligations {
		if !usdLike(o.Currency) {
			continue
		}
		due := nextDueDate(o, now)
		if due != nil && !due.After(deadline) && !due.Before(now) {
			total = total.Add(o.Amount)
		}
	}
	return total
}

func predictionHorizonDays(horizon string) int {
	switch horizon {
	case entities.Horizon3Day:
		return 3
	case entities.Horizon7Day:
		return 7
	case entities.Horizon14Day:
		return 14
	case entities.Horizon30Day:
		return 30
	default:
		return 7
	}
}

// LoopClosingMessage turns a freshly resolved prediction into a short, human
// "I remember what I said, here's how it turned out" line. Returns "" for
// informational prediction types that were never a warning worth revisiting.
// This is what makes Miriam feel accountable rather than noisy.
func LoopClosingMessage(o entities.MiriamPredictionOutcome) string {
	if o.ActualOutcome == nil {
		return ""
	}
	materialized := *o.ActualOutcome

	switch o.PredictionType {
	case entities.PredictionCashShortfall:
		if materialized {
			return "Earlier I flagged that Spend might run short this stretch. It did tighten, so worth keeping an eye on the next few days."
		}
		return "Earlier I flagged Spend might run short. It held up fine, you handled it."
	case entities.PredictionBillPressure:
		if materialized {
			return "I'd warned bills would press on your balance. They did land heavy, but you got through them."
		}
		return "I'd warned bills might squeeze you this week. Turned out smoother than I expected. Nicely managed."
	case entities.PredictionIncomeGap:
		if materialized {
			return "I'd flagged income might come in light this month. It did trail a bit, so I'm factoring that into what's next."
		}
		return "I'd flagged income might come in light. It landed where it needed to. Good."
	case entities.PredictionSpendingAnomaly:
		if materialized {
			return "I'd noticed spending picking up pace. It did keep climbing, so I'll watch the categories with you."
		}
		return "I'd noticed spending edging up. It settled back down. False alarm, which is the good kind."
	default:
		return ""
	}
}
