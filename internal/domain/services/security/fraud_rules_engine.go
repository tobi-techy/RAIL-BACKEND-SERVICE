package security

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

type FraudRulesEngine struct {
	db     *sql.DB
	redis  *redis.Client
	logger *zap.Logger

	mu    sync.RWMutex
	rules []entities.FraudRule
}

func NewFraudRulesEngine(db *sql.DB, redis *redis.Client, logger *zap.Logger) *FraudRulesEngine {
	engine := &FraudRulesEngine{db: db, redis: redis, logger: logger}
	if err := engine.RefreshRules(context.Background()); err != nil {
		logger.Error("Failed to load fraud rules on init", zap.Error(err))
	}
	return engine
}

func (e *FraudRulesEngine) RefreshRules(ctx context.Context) error {
	rows, err := e.db.QueryContext(ctx, `
		SELECT id, name, description, rule_type, conditions, action, severity, score_weight, is_active, applies_to
		FROM fraud_rules WHERE is_active = true ORDER BY score_weight DESC`)
	if err != nil {
		return fmt.Errorf("failed to load fraud rules: %w", err)
	}
	defer rows.Close()

	var rules []entities.FraudRule
	for rows.Next() {
		var r entities.FraudRule
		var condJSON []byte
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &r.RuleType, &condJSON,
			&r.Action, &r.Severity, &r.ScoreWeight, &r.IsActive, &r.AppliesTo); err != nil {
			return fmt.Errorf("failed to scan fraud rule: %w", err)
		}
		if err := json.Unmarshal(condJSON, &r.Conditions); err != nil {
			e.logger.Error("Skipping rule with invalid conditions JSON",
				zap.String("rule_id", r.ID.String()), zap.String("rule_name", r.Name), zap.Error(err))
			continue
		}
		rules = append(rules, r)
	}

	e.mu.Lock()
	e.rules = rules
	e.mu.Unlock()

	e.logger.Info("Fraud rules refreshed", zap.Int("count", len(rules)))
	return nil
}

func (e *FraudRulesEngine) EvaluateTransaction(ctx context.Context, tx *entities.MonitoredTransaction) ([]entities.RuleEvalResult, entities.FraudRuleAction) {
	e.mu.RLock()
	rules := e.rules
	e.mu.RUnlock()

	var results []entities.RuleEvalResult
	highestAction := entities.RuleActionAllow

	for _, rule := range rules {
		if rule.AppliesTo != "all" && rule.AppliesTo != tx.Type {
			continue
		}
		result := e.evaluateRule(ctx, rule, tx)
		if result.Triggered {
			results = append(results, result)
			if actionSeverity(result.Action) > actionSeverity(highestAction) {
				highestAction = result.Action
			}
		}
	}
	return results, highestAction
}

func (e *FraudRulesEngine) evaluateRule(ctx context.Context, rule entities.FraudRule, tx *entities.MonitoredTransaction) entities.RuleEvalResult {
	result := entities.RuleEvalResult{RuleID: rule.ID, RuleName: rule.Name, Action: rule.Action}

	switch rule.RuleType {
	case entities.FraudRuleVelocity:
		result.Triggered, result.Details = e.evalVelocity(ctx, rule, tx)
	case entities.FraudRuleAmount:
		result.Triggered, result.Details = e.evalAmount(ctx, rule, tx)
	case entities.FraudRulePattern:
		result.Triggered, result.Details = e.evalPattern(ctx, rule, tx)
	case entities.FraudRuleDevice:
		result.Triggered, result.Details = e.evalDevice(ctx, rule, tx)
	case entities.FraudRuleCustom:
		result.Triggered, result.Details = e.evalCustom(ctx, rule, tx)
	}

	if result.Triggered {
		result.Score = rule.ScoreWeight
	}
	return result
}

func (e *FraudRulesEngine) evalVelocity(ctx context.Context, rule entities.FraudRule, tx *entities.MonitoredTransaction) (bool, string) {
	cond := rule.Conditions
	if cond.Event == "" || cond.WindowSeconds == 0 {
		return false, ""
	}

	window := time.Duration(cond.WindowSeconds) * time.Second
	since := time.Now().Add(-window)

	if cond.CountThreshold > 0 {
		var count int
		if err := e.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM transactions 
			WHERE user_id = $1 AND type = $2 AND created_at > $3`,
			tx.UserID, cond.Event, since).Scan(&count); err != nil {
			e.logger.Error("Velocity count query failed, flagging for review",
				zap.String("user_id", tx.UserID.String()), zap.Error(err))
			return true, "velocity check failed: database error, flagged for manual review"
		}
		if float64(count) >= cond.CountThreshold {
			return true, fmt.Sprintf("%d %ss in %v (threshold: %.0f)", count, cond.Event, window, cond.CountThreshold)
		}
	}

	if cond.SumThreshold > 0 {
		var sum decimal.Decimal
		if err := e.db.QueryRowContext(ctx, `
			SELECT COALESCE(SUM(amount), 0) FROM transactions 
			WHERE user_id = $1 AND type = $2 AND created_at > $3`,
			tx.UserID, cond.Event, since).Scan(&sum); err != nil {
			e.logger.Error("Velocity sum query failed, flagging for review",
				zap.String("user_id", tx.UserID.String()), zap.Error(err))
			return true, "velocity sum check failed: database error, flagged for manual review"
		}
		if sum.GreaterThan(decimal.NewFromFloat(cond.SumThreshold)) {
			return true, fmt.Sprintf("cumulative %s amount $%s in %v (threshold: $%.0f)", cond.Event, sum.StringFixed(2), window, cond.SumThreshold)
		}
	}

	return false, ""
}

func (e *FraudRulesEngine) evalAmount(ctx context.Context, rule entities.FraudRule, tx *entities.MonitoredTransaction) (bool, string) {
	cond := rule.Conditions
	amount := tx.Amount.InexactFloat64()
	if cond.MinAmount > 0 && amount < cond.MinAmount {
		return false, ""
	}

	if cond.MaxAccountAgeHours > 0 {
		var createdAt time.Time
		if err := e.db.QueryRowContext(ctx, "SELECT created_at FROM users WHERE id = $1", tx.UserID).Scan(&createdAt); err != nil {
			e.logger.Error("Account age query failed, flagging for review",
				zap.String("user_id", tx.UserID.String()), zap.Error(err))
			return true, "account age check failed: database error, flagged for manual review"
		}
		if time.Since(createdAt).Hours() > cond.MaxAccountAgeHours {
			return false, ""
		}
	}

	if cond.FirstTransaction {
		var txCount int
		if err := e.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM transactions WHERE user_id = $1 AND type = $2`,
			tx.UserID, tx.Type).Scan(&txCount); err != nil {
			e.logger.Error("First transaction query failed, flagging for review",
				zap.String("user_id", tx.UserID.String()), zap.Error(err))
			return true, "first transaction check failed: database error, flagged for manual review"
		}
		if txCount > 1 {
			return false, ""
		}
	}

	return true, fmt.Sprintf("amount $%.2f exceeds threshold $%.0f on qualifying account", amount, cond.MinAmount)
}

func (e *FraudRulesEngine) evalPattern(ctx context.Context, rule entities.FraudRule, tx *entities.MonitoredTransaction) (bool, string) {
	switch rule.Conditions.Pattern {
	case "fund_through":
		return e.evalFundThrough(ctx, rule.Conditions, tx)
	case "structuring":
		return e.evalStructuring(ctx, rule.Conditions, tx)
	}
	return false, ""
}

func (e *FraudRulesEngine) evalFundThrough(ctx context.Context, cond entities.RuleConditions, tx *entities.MonitoredTransaction) (bool, string) {
	if tx.Type != "withdrawal" {
		return false, ""
	}

	ratio := cond.WithdrawalRatio
	maxDelay := cond.MaxDelaySeconds
	if ratio == 0 {
		ratio = 0.8
	}
	if maxDelay == 0 {
		maxDelay = 3600
	}

	var depositAmount decimal.Decimal
	var depositTime time.Time
	err := e.db.QueryRowContext(ctx, `
		SELECT amount, created_at FROM transactions 
		WHERE user_id = $1 AND type = 'deposit' AND status = 'completed'
		AND created_at > NOW() - INTERVAL '1 second' * $2
		ORDER BY created_at DESC LIMIT 1`,
		tx.UserID, maxDelay).Scan(&depositAmount, &depositTime)

	if err == sql.ErrNoRows {
		return false, ""
	}
	if err != nil {
		e.logger.Error("Fund-through deposit query failed, flagging for review",
			zap.String("user_id", tx.UserID.String()), zap.Error(err))
		return true, "fund-through check failed: database error, flagged for manual review"
	}
	if depositAmount.IsZero() {
		return false, ""
	}

	withdrawalRatio := tx.Amount.Div(depositAmount).InexactFloat64()
	if withdrawalRatio >= ratio {
		timeBetween := time.Since(depositTime).Seconds()
		return true, fmt.Sprintf("withdrawal of %.0f%% of deposit ($%s) within %.0fs",
			withdrawalRatio*100, depositAmount.StringFixed(2), timeBetween)
	}
	return false, ""
}

func (e *FraudRulesEngine) evalStructuring(ctx context.Context, cond entities.RuleConditions, tx *entities.MonitoredTransaction) (bool, string) {
	if tx.Type != "deposit" {
		return false, ""
	}
	if cond.Threshold == 0 || cond.Margin == 0 || cond.Count == 0 {
		return false, ""
	}

	amount := tx.Amount.InexactFloat64()
	if amount < (cond.Threshold-cond.Margin) || amount >= cond.Threshold {
		return false, ""
	}

	window := time.Duration(cond.WindowHours) * time.Hour
	var count int
	if err := e.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM transactions 
		WHERE user_id = $1 AND type = 'deposit' 
		AND amount >= $2 AND amount < $3
		AND created_at > $4`,
		tx.UserID, cond.Threshold-cond.Margin, cond.Threshold, time.Now().Add(-window)).Scan(&count); err != nil {
		e.logger.Error("Structuring query failed, flagging for review",
			zap.String("user_id", tx.UserID.String()), zap.Error(err))
		return true, "structuring check failed: database error, flagged for manual review"
	}

	if float64(count) >= cond.Count {
		return true, fmt.Sprintf("%d deposits between $%.0f-$%.0f in %v (structuring pattern)",
			count, cond.Threshold-cond.Margin, cond.Threshold, window)
	}
	return false, ""
}

func (e *FraudRulesEngine) evalDevice(ctx context.Context, rule entities.FraudRule, tx *entities.MonitoredTransaction) (bool, string) {
	cond := rule.Conditions

	if cond.MaxAccountsPerDevice > 0 && tx.DeviceID != "" {
		var accountCount int
		if err := e.db.QueryRowContext(ctx, `
			SELECT COUNT(DISTINCT user_id) FROM device_account_links 
			WHERE device_fingerprint = $1 AND created_at > NOW() - INTERVAL '30 days'`,
			tx.DeviceID).Scan(&accountCount); err != nil {
			e.logger.Error("Device accounts query failed, flagging for review",
				zap.String("device_id", tx.DeviceID), zap.Error(err))
			return true, "device check failed: database error, flagged for manual review"
		}
		if float64(accountCount) > cond.MaxAccountsPerDevice {
			return true, fmt.Sprintf("device linked to %d accounts (max: %.0f)", accountCount, cond.MaxAccountsPerDevice)
		}
	}

	if cond.MaxDeviceAgeHours > 0 && tx.DeviceID != "" {
		amount := tx.Amount.InexactFloat64()
		if cond.MinAmount > 0 && amount < cond.MinAmount {
			return false, ""
		}
		var deviceCreatedAt time.Time
		err := e.db.QueryRowContext(ctx, `
			SELECT created_at FROM known_devices 
			WHERE user_id = $1 AND fingerprint = $2`,
			tx.UserID, tx.DeviceID).Scan(&deviceCreatedAt)
		if err == nil {
			ageHours := time.Since(deviceCreatedAt).Hours()
			if ageHours < cond.MaxDeviceAgeHours {
				return true, fmt.Sprintf("device age %.1fh (max: %.0fh) with amount $%.2f", ageHours, cond.MaxDeviceAgeHours, amount)
			}
		}
	}
	return false, ""
}

func (e *FraudRulesEngine) evalCustom(ctx context.Context, rule entities.FraudRule, tx *entities.MonitoredTransaction) (bool, string) {
	cond := rule.Conditions
	amount := tx.Amount.InexactFloat64()
	if cond.MinAmount > 0 && amount < cond.MinAmount {
		return false, ""
	}

	hour := time.Now().Hour()
	if cond.HourStart > 0 && cond.HourEnd > 0 {
		if hour >= cond.HourStart && hour <= cond.HourEnd {
			return true, fmt.Sprintf("transaction at %d:00 (unusual hours %d-%d) amount $%.2f", hour, cond.HourStart, cond.HourEnd, amount)
		}
	}
	return false, ""
}

// CreateAlert persists a fraud alert to the database.
func (e *FraudRulesEngine) CreateAlert(ctx context.Context, alert *entities.FraudRuleAlert) error {
	details, err := json.Marshal(alert.Details)
	if err != nil {
		e.logger.Error("Failed to marshal alert details",
			zap.String("alert_id", alert.ID.String()), zap.Error(err))
		return fmt.Errorf("marshal alert details: %w", err)
	}
	_, err = e.db.ExecContext(ctx, `
		INSERT INTO fraud_alerts (id, user_id, rule_id, alert_type, severity, status, details, transaction_id, transaction_type, amount, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		alert.ID, alert.UserID, alert.RuleID, alert.AlertType, alert.Severity,
		alert.Status, details, alert.TransactionID, alert.TransactionType, alert.Amount, alert.CreatedAt)
	return err
}

// GetOpenAlerts returns unresolved alerts for ops dashboard.
func (e *FraudRulesEngine) GetOpenAlerts(ctx context.Context, limit int) ([]entities.FraudRuleAlert, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT id, user_id, rule_id, alert_type, severity, status, details, transaction_id, transaction_type, amount, created_at
		FROM fraud_alerts WHERE status IN ('open', 'investigating')
		ORDER BY CASE severity WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END, created_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []entities.FraudRuleAlert
	for rows.Next() {
		var a entities.FraudRuleAlert
		var detailsJSON []byte
		if err := rows.Scan(&a.ID, &a.UserID, &a.RuleID, &a.AlertType, &a.Severity,
			&a.Status, &detailsJSON, &a.TransactionID, &a.TransactionType, &a.Amount, &a.CreatedAt); err != nil {
			e.logger.Error("Failed to scan alert row", zap.Error(err))
			continue
		}
		json.Unmarshal(detailsJSON, &a.Details)
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

func actionSeverity(action entities.FraudRuleAction) int {
	switch action {
	case entities.RuleActionFreeze:
		return 4
	case entities.RuleActionBlock:
		return 3
	case entities.RuleActionManualReview:
		return 2
	case entities.RuleActionFlag:
		return 1
	default:
		return 0
	}
}
