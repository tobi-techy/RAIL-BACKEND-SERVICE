package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

type MiriamIntelligenceRepository struct {
	db *sqlx.DB
}

func NewMiriamIntelligenceRepository(db *sqlx.DB) *MiriamIntelligenceRepository {
	return &MiriamIntelligenceRepository{db: db}
}

func (r *MiriamIntelligenceRepository) UpsertMoneyState(ctx context.Context, state *entities.MiriamMoneyState) error {
	if state.Snapshot == nil {
		state.Snapshot = json.RawMessage(`{}`)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO miriam_money_states (
			user_id, income_cadence, avg_monthly_income, upcoming_obligations,
			safe_to_spend_daily, liquidity_runway_days, stash_target,
			recurring_spend_monthly, anomaly_count, confidence_level,
			confidence_score, last_evaluated_at, snapshot,
			monthly_spend, monthly_savings, spend_balance, stash_balance, calibration_score, active_months,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, NOW(), NOW()
		)
		ON CONFLICT (user_id) DO UPDATE SET
			income_cadence = EXCLUDED.income_cadence,
			avg_monthly_income = EXCLUDED.avg_monthly_income,
			upcoming_obligations = EXCLUDED.upcoming_obligations,
			safe_to_spend_daily = EXCLUDED.safe_to_spend_daily,
			liquidity_runway_days = EXCLUDED.liquidity_runway_days,
			stash_target = EXCLUDED.stash_target,
			recurring_spend_monthly = EXCLUDED.recurring_spend_monthly,
			anomaly_count = EXCLUDED.anomaly_count,
			confidence_level = EXCLUDED.confidence_level,
			confidence_score = EXCLUDED.confidence_score,
			last_evaluated_at = EXCLUDED.last_evaluated_at,
			snapshot = EXCLUDED.snapshot,
			monthly_spend = EXCLUDED.monthly_spend,
			monthly_savings = EXCLUDED.monthly_savings,
			spend_balance = EXCLUDED.spend_balance,
			stash_balance = EXCLUDED.stash_balance,
			calibration_score = EXCLUDED.calibration_score,
			active_months = EXCLUDED.active_months,
			updated_at = NOW()`,
		state.UserID, state.IncomeCadence, state.AvgMonthlyIncome, state.UpcomingObligations,
		state.SafeToSpendDaily, state.LiquidityRunwayDays, state.StashTarget,
		state.RecurringSpendMonthly, state.AnomalyCount, state.ConfidenceLevel,
		state.ConfidenceScore, state.LastEvaluatedAt, state.Snapshot,
		state.MonthlySpend, state.MonthlySavings, state.SpendBalance, state.StashBalance, state.CalibrationScore, state.ActiveMonths)
	if err != nil {
		return fmt.Errorf("upsert miriam money state: %w", err)
	}
	return nil
}

func (r *MiriamIntelligenceRepository) GetMoneyState(ctx context.Context, userID uuid.UUID) (*entities.MiriamMoneyState, error) {
	var state entities.MiriamMoneyState
	err := r.db.GetContext(ctx, &state, `SELECT * FROM miriam_money_states WHERE user_id = $1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get miriam money state: %w", err)
	}
	return &state, nil
}

func (r *MiriamIntelligenceRepository) CreateMandate(ctx context.Context, mandate *entities.MiriamAutopilotMandate) error {
	if mandate.ID == uuid.Nil {
		mandate.ID = uuid.New()
	}
	if mandate.Status == "" {
		mandate.Status = entities.MiriamMandateStatusActive
	}
	if mandate.Metadata == nil {
		mandate.Metadata = json.RawMessage(`{}`)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO miriam_autopilot_mandates (
			id, user_id, name, action_type, status, max_amount_per_action,
			max_amount_per_day, min_spend_balance, min_safe_to_spend,
			cooldown_minutes, last_executed_at, expires_at, metadata, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NOW(), NOW()
		)`,
		mandate.ID, mandate.UserID, mandate.Name, mandate.ActionType, mandate.Status,
		mandate.MaxAmountPerAction, mandate.MaxAmountPerDay, mandate.MinSpendBalance,
		mandate.MinSafeToSpend, mandate.CooldownMinutes, mandate.LastExecutedAt,
		mandate.ExpiresAt, mandate.Metadata)
	if err != nil {
		return fmt.Errorf("create miriam mandate: %w", err)
	}
	return nil
}

func (r *MiriamIntelligenceRepository) ListMandates(ctx context.Context, userID uuid.UUID) ([]entities.MiriamAutopilotMandate, error) {
	var mandates []entities.MiriamAutopilotMandate
	err := r.db.SelectContext(ctx, &mandates, `
		SELECT id, user_id, name, action_type, status, max_amount_per_action,
		       max_amount_per_day, min_spend_balance, min_safe_to_spend,
		       cooldown_minutes, last_executed_at, expires_at, metadata,
		       created_at, updated_at
		FROM miriam_autopilot_mandates
		WHERE user_id = $1
		ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list miriam mandates: %w", err)
	}
	return mandates, nil
}

func (r *MiriamIntelligenceRepository) ListActiveMandates(ctx context.Context, userID uuid.UUID) ([]entities.MiriamAutopilotMandate, error) {
	var mandates []entities.MiriamAutopilotMandate
	err := r.db.SelectContext(ctx, &mandates, `
		SELECT id, user_id, name, action_type, status, max_amount_per_action,
		       max_amount_per_day, min_spend_balance, min_safe_to_spend,
		       cooldown_minutes, last_executed_at, expires_at, metadata,
		       created_at, updated_at
		FROM miriam_autopilot_mandates
		WHERE user_id = $1
		  AND status = 'active'
		  AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY created_at ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list active miriam mandates: %w", err)
	}
	return mandates, nil
}

func (r *MiriamIntelligenceRepository) UpdateMandateStatus(ctx context.Context, userID, id uuid.UUID, status string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE miriam_autopilot_mandates
		SET status = $3, updated_at = NOW()
		WHERE user_id = $1 AND id = $2`, userID, id, status)
	if err != nil {
		return fmt.Errorf("update miriam mandate status: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check miriam mandate rows: %w", err)
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *MiriamIntelligenceRepository) MarkMandateExecuted(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE miriam_autopilot_mandates
		SET last_executed_at = $2, updated_at = NOW()
		WHERE id = $1`, id, at)
	if err != nil {
		return fmt.Errorf("mark miriam mandate executed: %w", err)
	}
	return nil
}

func (r *MiriamIntelligenceRepository) MandateExecutedAmountSince(ctx context.Context, mandateID uuid.UUID, since time.Time) (decimal.Decimal, error) {
	var total decimal.Decimal
	err := r.db.GetContext(ctx, &total, `
		SELECT COALESCE(SUM(amount), 0)
		FROM miriam_decision_receipts
		WHERE mandate_id = $1
		  AND status = 'executed'
		  AND created_at >= $2`, mandateID, since)
	if err != nil {
		return decimal.Zero, fmt.Errorf("sum mandate receipts: %w", err)
	}
	return total, nil
}

func (r *MiriamIntelligenceRepository) CreateReceipt(ctx context.Context, receipt *entities.MiriamDecisionReceipt) error {
	if receipt.ID == uuid.Nil {
		receipt.ID = uuid.New()
	}
	if receipt.Currency == "" {
		receipt.Currency = "USD"
	}
	if receipt.Evidence == nil {
		receipt.Evidence = json.RawMessage(`{}`)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO miriam_decision_receipts (
			id, user_id, mandate_id, event_type, action_type, amount, currency,
			status, reason, evidence, error_message, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
		)`,
		receipt.ID, receipt.UserID, receipt.MandateID, receipt.EventType, receipt.ActionType,
		receipt.Amount, receipt.Currency, receipt.Status, receipt.Reason, receipt.Evidence,
		receipt.ErrorMessage, receipt.CreatedAt)
	if err != nil {
		return fmt.Errorf("create miriam decision receipt: %w", err)
	}
	return nil
}

func (r *MiriamIntelligenceRepository) ListReceipts(ctx context.Context, userID uuid.UUID, limit int) ([]entities.MiriamDecisionReceipt, error) {
	if limit <= 0 {
		limit = 20
	} else if limit > 50 {
		limit = 50
	}
	var receipts []entities.MiriamDecisionReceipt
	err := r.db.SelectContext(ctx, &receipts, `
		SELECT * FROM miriam_decision_receipts
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list miriam receipts: %w", err)
	}
	return receipts, nil
}

// ListReceiptsSince returns receipts created at or after the given time, oldest
// first, so self-review can grade actions in chronological order.
func (r *MiriamIntelligenceRepository) ListReceiptsSince(ctx context.Context, userID uuid.UUID, since time.Time) ([]entities.MiriamDecisionReceipt, error) {
	var receipts []entities.MiriamDecisionReceipt
	err := r.db.SelectContext(ctx, &receipts, `
		SELECT * FROM miriam_decision_receipts
		WHERE user_id = $1 AND created_at >= $2
		ORDER BY created_at ASC`, userID, since)
	if err != nil {
		return nil, fmt.Errorf("list miriam receipts since: %w", err)
	}
	return receipts, nil
}

// CreateSelfReview persists a self-review audit row.
func (r *MiriamIntelligenceRepository) CreateSelfReview(ctx context.Context, review *entities.MiriamSelfReview) error {
	if review.ID == uuid.Nil {
		review.ID = uuid.New()
	}
	if review.CadenceHint == "" {
		review.CadenceHint = entities.NudgeCadenceNormal
	}
	if review.Adjustments == nil {
		review.Adjustments = json.RawMessage(`{}`)
	}
	if review.CreatedAt.IsZero() {
		review.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO miriam_self_reviews (
			id, user_id, period_start, period_end, actions_reviewed, actions_helped,
			actions_neutral, actions_harmed, nudges_sent, nudges_dismissed,
			avg_health_before, avg_health_after, cadence_hint, verdict, adjustments,
			note_sent, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
		)`,
		review.ID, review.UserID, review.PeriodStart, review.PeriodEnd, review.ActionsReviewed,
		review.ActionsHelped, review.ActionsNeutral, review.ActionsHarmed, review.NudgesSent,
		review.NudgesDismissed, review.AvgHealthBefore, review.AvgHealthAfter, review.CadenceHint,
		review.Verdict, review.Adjustments, review.NoteSent, review.CreatedAt)
	if err != nil {
		return fmt.Errorf("create miriam self review: %w", err)
	}
	return nil
}

// LastSelfReviewAt returns the timestamp of the user's most recent self-review,
// or nil if none exists. Used to gate the once-per-day cadence.
func (r *MiriamIntelligenceRepository) LastSelfReviewAt(ctx context.Context, userID uuid.UUID) (*time.Time, error) {
	var at time.Time
	err := r.db.GetContext(ctx, &at, `
		SELECT created_at FROM miriam_self_reviews
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("last miriam self review at: %w", err)
	}
	return &at, nil
}

// LastSelfReviewNoteAt returns the timestamp of the user's most recent
// self-review that surfaced a user-facing note, or nil if none. Used to gate the
// weekly "how I'm doing" note.
func (r *MiriamIntelligenceRepository) LastSelfReviewNoteAt(ctx context.Context, userID uuid.UUID) (*time.Time, error) {
	var at time.Time
	err := r.db.GetContext(ctx, &at, `
		SELECT created_at FROM miriam_self_reviews
		WHERE user_id = $1 AND note_sent = TRUE
		ORDER BY created_at DESC
		LIMIT 1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("last miriam self review note at: %w", err)
	}
	return &at, nil
}

// LatestCadenceHint returns the nudge cadence hint from the user's most recent
// self-review, defaulting to "normal" when none exists.
func (r *MiriamIntelligenceRepository) LatestCadenceHint(ctx context.Context, userID uuid.UUID) (string, error) {
	var hint string
	err := r.db.GetContext(ctx, &hint, `
		SELECT cadence_hint FROM miriam_self_reviews
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 1`, userID)
	if err == sql.ErrNoRows {
		return entities.NudgeCadenceNormal, nil
	}
	if err != nil {
		return entities.NudgeCadenceNormal, fmt.Errorf("latest miriam cadence hint: %w", err)
	}
	return hint, nil
}

func (r *MiriamIntelligenceRepository) CreateEvent(ctx context.Context, event *entities.MiriamEvent) error {
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	if event.Currency == "" {
		event.Currency = "USD"
	}
	if event.Severity == "" {
		event.Severity = "info"
	}
	if event.Metadata == nil {
		event.Metadata = json.RawMessage(`{}`)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO miriam_events (id, user_id, event_type, severity, amount, currency, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		event.ID, event.UserID, event.EventType, event.Severity, event.Amount, event.Currency, event.Metadata, event.CreatedAt)
	if err != nil {
		return fmt.Errorf("create miriam event: %w", err)
	}
	return nil
}

func (r *MiriamIntelligenceRepository) CreateLearningSignal(ctx context.Context, signal *entities.MiriamLearningSignal) error {
	if signal.ID == uuid.Nil {
		signal.ID = uuid.New()
	}
	if signal.Weight.IsZero() {
		signal.Weight = decimal.NewFromInt(1)
	}
	if signal.Metadata == nil {
		signal.Metadata = json.RawMessage(`{}`)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO miriam_learning_signals (id, user_id, receipt_id, signal, weight, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		signal.ID, signal.UserID, signal.ReceiptID, signal.Signal, signal.Weight, signal.Metadata, signal.CreatedAt)
	if err != nil {
		return fmt.Errorf("create miriam learning signal: %w", err)
	}
	return nil
}

func (r *MiriamIntelligenceRepository) RecentLearningBias(ctx context.Context, userID uuid.UUID, since time.Time) (decimal.Decimal, error) {
	var score decimal.Decimal
	err := r.db.GetContext(ctx, &score, `
		SELECT COALESCE(SUM(
			CASE signal
				WHEN 'accepted' THEN weight
				WHEN 'reversed' THEN -2 * weight
				WHEN 'dismissed' THEN -1 * weight
				WHEN 'ignored' THEN -0.5 * weight
				ELSE 0
			END
		), 0)
		FROM miriam_learning_signals
		WHERE user_id = $1 AND created_at >= $2`, userID, since)
	if err != nil {
		return decimal.Zero, fmt.Errorf("recent miriam learning bias: %w", err)
	}
	return score, nil
}

func (r *MiriamIntelligenceRepository) SavePredictionOutcomes(ctx context.Context, outcomes []entities.MiriamPredictionOutcome) error {
	if len(outcomes) == 0 {
		return nil
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, o := range outcomes {
		if o.ThresholdData == nil {
			o.ThresholdData = json.RawMessage(`{}`)
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO miriam_prediction_outcomes
				(id, user_id, prediction_id, prediction_type, predicted_probability,
				 horizon_days, threshold_data, actual_outcome, outcome_observed_at, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (id) DO NOTHING`,
			o.ID, o.UserID, o.PredictionID, o.PredictionType, o.PredictedProbability,
			o.HorizonDays, o.ThresholdData, o.ActualOutcome, o.OutcomeObservedAt, o.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert prediction outcome: %w", err)
		}
	}
	return tx.Commit()
}

func (r *MiriamIntelligenceRepository) GetPendingPredictionOutcomes(ctx context.Context, userID uuid.UUID) ([]entities.MiriamPredictionOutcome, error) {
	var outcomes []entities.MiriamPredictionOutcome
	err := r.db.SelectContext(ctx, &outcomes, `
		SELECT id, user_id, prediction_id, prediction_type, predicted_probability,
		       horizon_days, threshold_data, actual_outcome, outcome_observed_at, created_at
		FROM miriam_prediction_outcomes
		WHERE user_id = $1 AND actual_outcome IS NULL
		ORDER BY created_at ASC
		LIMIT 100`, userID)
	if err != nil {
		return nil, fmt.Errorf("get pending prediction outcomes: %w", err)
	}
	return outcomes, nil
}

func (r *MiriamIntelligenceRepository) MarkPredictionOutcome(ctx context.Context, id uuid.UUID, outcome bool, observedAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE miriam_prediction_outcomes
		SET actual_outcome = $2, outcome_observed_at = $3
		WHERE id = $1`, id, outcome, observedAt)
	if err != nil {
		return fmt.Errorf("mark prediction outcome: %w", err)
	}
	return nil
}

func (r *MiriamIntelligenceRepository) BatchMarkPredictionOutcomes(ctx context.Context, outcomes []entities.MiriamPredictionOutcome) error {
	if len(outcomes) == 0 {
		return nil
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, o := range outcomes {
		if o.ActualOutcome == nil {
			continue
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE miriam_prediction_outcomes
			SET actual_outcome = $2, outcome_observed_at = $3
			WHERE id = $1`, o.ID, *o.ActualOutcome, o.OutcomeObservedAt)
		if err != nil {
			return fmt.Errorf("batch mark prediction outcome: %w", err)
		}
	}
	return tx.Commit()
}

func (r *MiriamIntelligenceRepository) GetPredictionHitRate(ctx context.Context, userID uuid.UUID, predictionType string, since time.Time) (float64, error) {
	var result struct {
		Total   int     `db:"total"`
		Correct int     `db:"correct"`
		Rate    float64 `db:"rate"`
	}
	query := `
		SELECT
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE actual_outcome = TRUE) AS correct,
			CASE WHEN COUNT(*) > 0
				THEN COUNT(*) FILTER (WHERE actual_outcome = TRUE)::FLOAT / COUNT(*)::FLOAT
				ELSE 0
			END AS rate
		FROM miriam_prediction_outcomes
		WHERE user_id = $1
		  AND actual_outcome IS NOT NULL
		  AND created_at >= $2`
	args := []interface{}{userID, since}
	if predictionType != "" {
		query += ` AND prediction_type = $3`
		args = append(args, predictionType)
	}
	err := r.db.GetContext(ctx, &result, query, args...)
	if err != nil {
		return 0, fmt.Errorf("get prediction hit rate: %w", err)
	}
	return result.Rate, nil
}
