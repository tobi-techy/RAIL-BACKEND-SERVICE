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
	repo        Repository
	spending    SpendingProvider
	balances    BalanceProvider
	obligations ObligationProvider
	logger      *zap.Logger
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

	// One pending outcome per prediction type. The sweep regenerates similar
	// predictions every cycle with fresh IDs, so without this gate each sweep
	// piles on another pending row for the same claim; when horizons expire
	// they resolve one after another and the user receives the identical
	// loop-closing message again and again.
	pending, err := t.repo.GetPendingPredictionOutcomes(ctx, userID)
	if err != nil {
		if t.logger != nil {
			t.logger.Warn("failed to check pending outcomes before recording, skipping predictions",
				zap.String("user_id", userID.String()), zap.Error(err))
		}
		return
	}
	alreadyPending := make(map[string]bool, len(pending))
	for _, p := range pending {
		alreadyPending[p.PredictionType] = true
	}

	outcomes := make([]entities.MiriamPredictionOutcome, 0, len(predictions))
	now := time.Now().UTC()

	for _, p := range predictions {
		if alreadyPending[p.PredictionType] {
			continue
		}
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

	if len(outcomes) == 0 {
		return
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

	cache := &sweepCache{tracker: t, userID: userID, now: now}

	var resolved []entities.MiriamPredictionOutcome
	var toMark []entities.MiriamPredictionOutcome
	for _, o := range pending {
		deadline := o.CreatedAt.Add(time.Duration(o.HorizonDays) * 24 * time.Hour)
		if now.Before(deadline) {
			continue
		}

		outcome := t.evaluatePrediction(ctx, o, spend, cache)
		settled := outcome
		o.ActualOutcome = &settled
		o.OutcomeObservedAt = &now
		toMark = append(toMark, o)
		resolved = append(resolved, o)
	}
	if len(toMark) > 0 {
		// The parent context carries the worker's per-user budget, which the
		// evaluation above may have exhausted. Detaching the write keeps settled
		// outcomes from being re-evaluated on every subsequent sweep.
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()

		if err := t.repo.BatchMarkPredictionOutcomes(writeCtx, toMark); err != nil {
			if t.logger != nil {
				t.logger.Warn("failed to batch mark outcomes",
					zap.String("user_id", userID.String()),
					zap.Int("count", len(toMark)),
					zap.Error(err))
			}
			return nil
		}
	}
	return resolved
}

// sweepCache memoizes the reads shared by every pending outcome within one
// EvaluateOutcomes sweep. All of them are scoped to a single user at a single
// instant, so a user with 100 pending outcomes would otherwise issue hundreds of
// identical queries and exhaust the worker's per-user deadline.
type sweepCache struct {
	tracker *OutcomeTracker
	userID  uuid.UUID
	now     time.Time

	upcomingObligations  decimal.Decimal
	upcomingLoaded       bool
	imminentObligations  decimal.Decimal
	imminentLoaded       bool
	thisMonthFlow        *entities.MoneyFlowSummary
	thisMonthFlowLoaded  bool
	priorMonthFlow       *entities.MoneyFlowSummary
	priorMonthFlowLoaded bool
}

func (c *sweepCache) monthStart() time.Time {
	return time.Date(c.now.Year(), c.now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func (c *sweepCache) upcoming(ctx context.Context) decimal.Decimal {
	if !c.upcomingLoaded {
		c.upcomingObligations = c.tracker.fetchUpcomingObligations(ctx, c.userID)
		c.upcomingLoaded = true
	}
	return c.upcomingObligations
}

func (c *sweepCache) imminent(ctx context.Context, days int) decimal.Decimal {
	if !c.imminentLoaded {
		c.imminentObligations = c.tracker.fetchImminentObligations(ctx, c.userID, days)
		c.imminentLoaded = true
	}
	return c.imminentObligations
}

func (c *sweepCache) thisMonth(ctx context.Context) *entities.MoneyFlowSummary {
	if !c.thisMonthFlowLoaded {
		if c.tracker.spending != nil {
			flow, err := c.tracker.spending.GetMoneyFlow(ctx, c.userID, c.monthStart(), c.now)
			if err != nil {
				if c.tracker.logger != nil {
					c.tracker.logger.Warn("failed to fetch this month flow for prediction evaluation",
						zap.String("user_id", c.userID.String()), zap.Error(err))
				}
			} else {
				c.thisMonthFlow = flow
			}
		}
		c.thisMonthFlowLoaded = true
	}
	return c.thisMonthFlow
}

func (c *sweepCache) priorMonth(ctx context.Context) *entities.MoneyFlowSummary {
	if !c.priorMonthFlowLoaded {
		if c.tracker.spending != nil {
			monthStart := c.monthStart()
			flow, err := c.tracker.spending.GetMoneyFlow(ctx, c.userID, monthStart.AddDate(0, -1, 0), monthStart)
			if err != nil {
				if c.tracker.logger != nil {
					c.tracker.logger.Warn("failed to fetch prior month flow for prediction evaluation",
						zap.String("user_id", c.userID.String()), zap.Error(err))
				}
			} else {
				c.priorMonthFlow = flow
			}
		}
		c.priorMonthFlowLoaded = true
	}
	return c.priorMonthFlow
}

func (t *OutcomeTracker) evaluatePrediction(ctx context.Context, o entities.MiriamPredictionOutcome, currentSpend decimal.Decimal, cache *sweepCache) bool {
	switch o.PredictionType {
	case entities.PredictionCashShortfall:
		return currentSpend.LessThan(cache.upcoming(ctx))

	case entities.PredictionBillPressure:
		return currentSpend.LessThan(cache.imminent(ctx, 7))

	case entities.PredictionIncomeGap:
		return resolveIncomeGap(ctx, o.ThresholdData, cache)

	case entities.PredictionSpendingAnomaly:
		return resolveSpendingAnomaly(ctx, cache)

	case entities.PredictionIdleSurplus, entities.PredictionStashOpportunity:
		return false // both outcomes are informational, never negative

	default:
		return false
	}
}

// resolveIncomeGap reports whether current month deposits trailed the projected
// income captured in the prediction snapshot by more than 30%.
func resolveIncomeGap(ctx context.Context, thresholdData []byte, cache *sweepCache) bool {
	flow := cache.thisMonth(ctx)
	if flow == nil {
		return false
	}
	// Parse projected_income from the prediction snapshot stored in threshold_data.
	var td struct {
		DataSnapshot string `json:"data_snapshot"`
	}
	if err := json.Unmarshal(thresholdData, &td); err != nil || td.DataSnapshot == "" {
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
	return flow.TotalDeposits.LessThan(projected.Mul(decimal.NewFromFloat(0.7)))
}

// resolveSpendingAnomaly reports whether this month's outflow exceeded last
// month's by more than 35% (the growth threshold used when the prediction was made).
func resolveSpendingAnomaly(ctx context.Context, cache *sweepCache) bool {
	thisMonth := cache.thisMonth(ctx)
	lastMonth := cache.priorMonth(ctx)
	if thisMonth == nil || lastMonth == nil {
		return false
	}
	thisOut := totalOut(thisMonth)
	lastOut := totalOut(lastMonth)
	return lastOut.IsPositive() && thisOut.GreaterThan(lastOut.Mul(decimal.NewFromFloat(1.35)))
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

// CleanupOldPredictions removes predictions older than the retention period
// (default 30 days). Once a prediction's outcome has been evaluated, the
// prediction row itself is historical and can be safely removed.
func (t *OutcomeTracker) CleanupOldPredictions(ctx context.Context, retentionDays int) (int64, error) {
	if t.repo == nil {
		return 0, nil
	}
	if retentionDays <= 0 {
		retentionDays = 30
	}
	before := time.Now().UTC().AddDate(0, 0, -retentionDays)
	return t.repo.DeletePredictionsOlderThan(ctx, before)
}

// CleanupEvaluatedOutcomes removes prediction outcomes that have been evaluated
// (actual_outcome IS NOT NULL) and are older than the retention period (default
// 30 days). Pending outcomes are preserved so they can still be evaluated when
// their horizon expires.
func (t *OutcomeTracker) CleanupEvaluatedOutcomes(ctx context.Context, retentionDays int) (int64, error) {
	if t.repo == nil {
		return 0, nil
	}
	if retentionDays <= 0 {
		retentionDays = 30
	}
	before := time.Now().UTC().AddDate(0, 0, -retentionDays)
	return t.repo.DeleteEvaluatedOutcomesOlderThan(ctx, before)
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
			messages := []string{
				"i flagged that spend might run short. it did tighten up. keeping an eye on the next few days.",
				"i had a feeling spend might get tight. it did, so let's keep the next few days light.",
				"i called out a possible spend squeeze. it showed up, so we'll watch the next few days.",
			}
			return messages[randIntn(len(messages))]
		}
		messages := []string{
			"i flagged that spend might run short. it held up fine, though. nice work.",
			"i thought spend might get tight, but you kept it steady. good stuff.",
			"i had spend down as a risk. you got through it without a squeeze.",
		}
		return messages[randIntn(len(messages))]
	case entities.PredictionBillPressure:
		if materialized {
			messages := []string{
				"told you bills might squeeze. they did, but you handled it.",
				"i flagged the bills could press on your balance. they landed heavy, and you got through them.",
				"i had bills down as a pinch point. they showed up, but you kept things moving.",
			}
			return messages[randIntn(len(messages))]
		}
		messages := []string{
			"told you bills might squeeze, but they came in lighter than expected. nice.",
			"i flagged bills as a risk this week. they didn't hit as hard as i thought.",
			"i thought bills might press you. turns out you had them covered.",
		}
		return messages[randIntn(len(messages))]
	case entities.PredictionIncomeGap:
		if materialized {
			messages := []string{
				"i flagged income might come in light. it did trail a bit, so i'll factor that into what's next.",
				"i thought this month might be thinner on income. it was, so let's plan around that.",
				"i had income down as a watchout. it came in light, and i'll keep that in mind.",
			}
			return messages[randIntn(len(messages))]
		}
		messages := []string{
			"i flagged income might come in light, but it landed where it needed to. good.",
			"i thought income could dip. it held up, so that's one less thing to worry about.",
			"i had income down as a risk. you got what you needed this month.",
		}
		return messages[randIntn(len(messages))]
	case entities.PredictionSpendingAnomaly:
		if materialized {
			messages := []string{
				"i noticed spending picking up. it kept climbing, so i'll watch those categories with you.",
				"i flagged that spending was speeding up. it did, so let's keep an eye on where it's going.",
				"i saw spending start to edge up. it carried on, and we'll keep tabs on it.",
			}
			return messages[randIntn(len(messages))]
		}
		messages := []string{
			"i noticed spending edging up. it settled back down, which is the good kind of false alarm.",
			"i had spending marked as a watchout. it cooled off, so we're good.",
			"i thought spending might keep climbing, but it eased back. nice.",
		}
		return messages[randIntn(len(messages))]
	default:
		return ""
	}
}
