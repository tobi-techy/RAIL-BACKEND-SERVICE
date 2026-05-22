package transaction_monitor

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/security"
)

// Worker monitors all financial transactions in real-time, scoring them
// through the fraud rules engine and triggering alerts/freezes as needed.
type Worker struct {
	db            *sql.DB
	redis         *redis.Client
	rulesEngine   *security.FraudRulesEngine
	fraudService  *security.FraudDetectionService
	logger        *zap.Logger
	interval      time.Duration
	stopCh        chan struct{}
}

func NewWorker(
	db *sql.DB,
	redis *redis.Client,
	rulesEngine *security.FraudRulesEngine,
	fraudService *security.FraudDetectionService,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		db:           db,
		redis:        redis,
		rulesEngine:  rulesEngine,
		fraudService: fraudService,
		logger:       logger,
		interval:     30 * time.Second,
		stopCh:       make(chan struct{}),
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting transaction monitoring worker")

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Refresh rules every 5 minutes
	rulesTicker := time.NewTicker(5 * time.Minute)
	defer rulesTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-rulesTicker.C:
			w.rulesEngine.RefreshRules(ctx)
		case <-ticker.C:
			w.processUnscored(ctx)
		}
	}
}

func (w *Worker) Stop() { close(w.stopCh) }

// processUnscored finds recent transactions that haven't been scored yet and evaluates them.
func (w *Worker) processUnscored(ctx context.Context) {
	// Get the last processed timestamp from Redis
	lastProcessed := w.getLastProcessedTime(ctx)

	rows, err := w.db.QueryContext(ctx, `
		SELECT t.id, t.user_id, t.type, t.amount, t.currency, t.created_at
		FROM transactions t
		LEFT JOIN transaction_risk_assessments tra ON tra.user_id = t.user_id 
			AND tra.created_at >= t.created_at AND tra.created_at <= t.created_at + INTERVAL '1 minute'
		WHERE t.created_at > $1 AND t.status IN ('completed', 'pending')
		AND tra.id IS NULL
		ORDER BY t.created_at ASC LIMIT 100`, lastProcessed)
	if err != nil {
		w.logger.Error("Failed to query unscored transactions", zap.Error(err))
		return
	}
	defer rows.Close()

	var processed int
	for rows.Next() {
		var tx entities.MonitoredTransaction
		if err := rows.Scan(&tx.ID, &tx.UserID, &tx.Type, &tx.Amount, &tx.Currency, &tx.CreatedAt); err != nil {
			continue
		}

		w.scoreTransaction(ctx, &tx)
		processed++
	}

	if processed > 0 {
		w.setLastProcessedTime(ctx, time.Now())
		w.logger.Info("Transaction monitoring batch complete", zap.Int("processed", processed))
	}
}

func (w *Worker) scoreTransaction(ctx context.Context, tx *entities.MonitoredTransaction) {
	// 1. Run through configurable rules engine
	results, highestAction := w.rulesEngine.EvaluateTransaction(ctx, tx)

	// 2. If any rules triggered, create alerts
	if len(results) > 0 {
		w.handleTriggeredRules(ctx, tx, results, highestAction)
	}

	// 3. Check for fund-through pattern on withdrawals
	if tx.Type == "withdrawal" {
		w.checkFundThrough(ctx, tx)
	}

	// 4. Check cumulative fraud score for auto-freeze
	w.checkAutoFreeze(ctx, tx.UserID)
}

func (w *Worker) handleTriggeredRules(ctx context.Context, tx *entities.MonitoredTransaction, results []entities.RuleEvalResult, action entities.FraudRuleAction) {
	// Determine highest severity from triggered rules
	severity := "medium"
	for _, r := range results {
		if r.Action == entities.RuleActionFreeze || r.Action == entities.RuleActionBlock {
			severity = "critical"
			break
		}
		if r.Action == entities.RuleActionManualReview {
			severity = "high"
		}
	}

	// Create alert
	details := map[string]interface{}{"triggered_rules": results}
	alert := &entities.FraudRuleAlert{
		ID:              uuid.New(),
		UserID:          tx.UserID,
		RuleID:          &results[0].RuleID,
		AlertType:       "rule_trigger",
		Severity:        severity,
		Status:          entities.AlertStatusOpen,
		Details:         details,
		TransactionID:   &tx.ID,
		TransactionType: tx.Type,
		Amount:          tx.Amount,
		CreatedAt:       time.Now(),
	}

	if err := w.rulesEngine.CreateAlert(ctx, alert); err != nil {
		w.logger.Error("Failed to create fraud alert", zap.Error(err))
	}

	// Execute action
	switch action {
	case entities.RuleActionBlock:
		w.blockTransaction(ctx, tx)
	case entities.RuleActionFreeze:
		w.freezeAccount(ctx, tx.UserID, "rule_trigger", fmt.Sprintf("Rule triggered: %s", results[0].RuleName), &alert.ID)
	}

	w.logger.Warn("Fraud rules triggered",
		zap.String("user_id", tx.UserID.String()),
		zap.String("action", string(action)),
		zap.Int("rules_triggered", len(results)))
}

func (w *Worker) checkFundThrough(ctx context.Context, tx *entities.MonitoredTransaction) {
	// Look for a recent deposit that this withdrawal is draining
	var depositID uuid.UUID
	var depositAmount decimal.Decimal
	var depositTime time.Time

	err := w.db.QueryRowContext(ctx, `
		SELECT id, amount, created_at FROM transactions 
		WHERE user_id = $1 AND type = 'deposit' AND status = 'completed'
		AND created_at > NOW() - INTERVAL '2 hours'
		ORDER BY amount DESC LIMIT 1`,
		tx.UserID).Scan(&depositID, &depositAmount, &depositTime)

	if err != nil || depositAmount.IsZero() {
		return
	}

	ratio := tx.Amount.Div(depositAmount).InexactFloat64()
	timeBetween := int(time.Since(depositTime).Seconds())

	// Flag if withdrawing >70% of a recent deposit within 2 hours
	if ratio >= 0.7 && timeBetween < 7200 {
		riskScore := ratio * (1.0 - float64(timeBetween)/7200.0)

		detection := &entities.FundThroughDetection{
			ID:                 uuid.New(),
			UserID:             tx.UserID,
			DepositID:          &depositID,
			WithdrawalID:       &tx.ID,
			DepositAmount:      depositAmount,
			WithdrawalAmount:   tx.Amount,
			TimeBetweenSeconds: timeBetween,
			WithdrawalRatio:    ratio,
			RiskScore:          riskScore,
			ActionTaken:        "flagged",
			CreatedAt:          time.Now(),
		}

		// Critical: >90% withdrawal within 30 minutes → block and freeze
		if ratio >= 0.9 && timeBetween < 1800 {
			detection.ActionTaken = "frozen"
			w.freezeAccount(ctx, tx.UserID, "fund_through",
				fmt.Sprintf("Fund-through: %.0f%% withdrawal within %ds of deposit", ratio*100, timeBetween), nil)
		}

		w.saveFundThroughDetection(ctx, detection)

		w.logger.Warn("Fund-through pattern detected",
			zap.String("user_id", tx.UserID.String()),
			zap.Float64("ratio", ratio),
			zap.Int("time_between_s", timeBetween))
	}
}

func (w *Worker) checkAutoFreeze(ctx context.Context, userID uuid.UUID) {
	// Get cumulative fraud score from recent signals
	var avgScore float64
	var signalCount int
	w.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(signal_value), 0), COUNT(*) FROM fraud_signals 
		WHERE user_id = $1 AND created_at > NOW() - INTERVAL '24 hours'`,
		userID).Scan(&avgScore, &signalCount)

	// Auto-freeze if: high average score AND multiple signals in 24h
	if avgScore >= 0.6 && signalCount >= 5 {
		// Check if already frozen
		var alreadyFrozen bool
		w.db.QueryRowContext(ctx, `
			SELECT COALESCE(withdrawals_frozen, false) FROM users WHERE id = $1`,
			userID).Scan(&alreadyFrozen)

		if !alreadyFrozen {
			w.freezeAccount(ctx, userID, "fraud_score",
				fmt.Sprintf("Auto-freeze: avg fraud score %.2f with %d signals in 24h", avgScore, signalCount), nil)
		}
	}
}

func (w *Worker) freezeAccount(ctx context.Context, userID uuid.UUID, freezeType, reason string, alertID *uuid.UUID) {
	// Freeze withdrawals
	w.db.ExecContext(ctx, `
		UPDATE users SET withdrawals_frozen = true, account_frozen_at = NOW() WHERE id = $1`, userID)

	// Record the freeze
	w.db.ExecContext(ctx, `
		INSERT INTO account_freezes (id, user_id, freeze_type, reason, triggered_by, alert_id, is_active, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, true, NOW())`,
		uuid.New(), userID, freezeType, reason, "system", alertID)

	w.logger.Warn("Account frozen",
		zap.String("user_id", userID.String()),
		zap.String("type", freezeType),
		zap.String("reason", reason))
}

func (w *Worker) blockTransaction(ctx context.Context, tx *entities.MonitoredTransaction) {
	w.db.ExecContext(ctx, `
		UPDATE transactions SET status = 'blocked', updated_at = NOW() WHERE id = $1`, tx.ID)
}

func (w *Worker) saveFundThroughDetection(ctx context.Context, d *entities.FundThroughDetection) {
	w.db.ExecContext(ctx, `
		INSERT INTO fund_through_detections (id, user_id, deposit_id, withdrawal_id, deposit_amount, withdrawal_amount, time_between_seconds, withdrawal_ratio, risk_score, action_taken, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		d.ID, d.UserID, d.DepositID, d.WithdrawalID, d.DepositAmount, d.WithdrawalAmount,
		d.TimeBetweenSeconds, d.WithdrawalRatio, d.RiskScore, d.ActionTaken, d.CreatedAt)
}

func (w *Worker) getLastProcessedTime(ctx context.Context) time.Time {
	val, err := w.redis.Get(ctx, "txmon:last_processed").Result()
	if err != nil {
		return time.Now().Add(-5 * time.Minute)
	}
	t, _ := time.Parse(time.RFC3339, val)
	return t
}

func (w *Worker) setLastProcessedTime(ctx context.Context, t time.Time) {
	w.redis.Set(ctx, "txmon:last_processed", t.Format(time.RFC3339), 24*time.Hour)
}


