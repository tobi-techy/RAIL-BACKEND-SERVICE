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

// FraudRulesEngine evaluates configurable fraud rules stored in the database.
// Rules are cached in memory and refreshed periodically so admin changes
// take effect without redeploying code.
type FraudRulesEngine struct {
	db     *sql.DB
	redis  *redis.Client
	logger *zap.Logger

	mu    sync.RWMutex
	rules []entities.FraudRule
}

func NewFraudRulesEngine(db *sql.DB, redis *redis.Client, logger *zap.Logger) *FraudRulesEngine {
	engine := &FraudRulesEngine{db: db, redis: redis, logger: logger}
	// Load rules on init
	if err := engine.RefreshRules(context.Background()); err != nil {
		logger.Error("Failed to load fraud rules on init", zap.Error(err))
	}
	return engine
}

// RefreshRules reloads active rules from the database.
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
		json.Unmarshal(condJSON, &r.Conditions)
		rules = append(rules, r)
	}

	e.mu.Lock()
	e.rules = rules
	e.mu.Unlock()

	e.logger.Info("Fraud rules refreshed", zap.Int("count", len(rules)))
	return nil
}

// EvaluateTransaction runs all applicable rules against a transaction.
// Returns the list of triggered rules and the highest-severity action.
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
	result := entities.RuleEvalResult{
		RuleID:   rule.ID,
		RuleName: rule.Name,
		Action:   rule.Action,
	}

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
	event, _ := cond["event"].(string)
	windowSec := getFloat(cond, "window_seconds")
	countThreshold := getFloat(cond, "count_threshold")
	sumThreshold := getFloat(cond, "sum_threshold")

	if event == "" || windowSec == 0 {
		return false, ""
	}

	window := time.Duration(windowSec) * time.Second
	since := time.Now().Add(-window)

	if countThreshold > 0 {
		var count int
		e.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM transactions 
			WHERE user_id = $1 AND type = $2 AND created_at > $3`,
			tx.UserID, event, since).Scan(&count)

		if float64(count) >= countThreshold {
			return true, fmt.Sprintf("%d %ss in %v (threshold: %.0f)", count, event, window, countThreshold)
		}
	}

	if sumThreshold > 0 {
		var sum decimal.Decimal
		e.db.QueryRowContext(ctx, `
			SELECT COALESCE(SUM(amount), 0) FROM transactions 
			WHERE user_id = $1 AND type = $2 AND created_at > $3`,
			tx.UserID, event, since).Scan(&sum)

		if sum.GreaterThan(decimal.NewFromFloat(sumThreshold)) {
			return true, fmt.Sprintf("cumulative %s amount $%s in %v (threshold: $%.0f)", event, sum.StringFixed(2), window, sumThreshold)
		}
	}

	return false, ""
}

func (e *FraudRulesEngine) evalAmount(ctx context.Context, rule entities.FraudRule, tx *entities.MonitoredTransaction) (bool, string) {
	cond := rule.Conditions
	minAmount := getFloat(cond, "min_amount")
	maxAccountAgeHours := getFloat(cond, "max_account_age_hours")
	firstTx, _ := cond["first_transaction"].(bool)

	amount := tx.Amount.InexactFloat64()
	if minAmount > 0 && amount < minAmount {
		return false, ""
	}

	if maxAccountAgeHours > 0 {
		var createdAt time.Time
		e.db.QueryRowContext(ctx, "SELECT created_at FROM users WHERE id = $1", tx.UserID).Scan(&createdAt)
		ageHours := time.Since(createdAt).Hours()
		if ageHours > maxAccountAgeHours {
			return false, ""
		}
	}

	if firstTx {
		var txCount int
		e.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM transactions WHERE user_id = $1 AND type = $2`,
			tx.UserID, tx.Type).Scan(&txCount)
		if txCount > 1 {
			return false, ""
		}
	}

	return true, fmt.Sprintf("amount $%.2f exceeds threshold $%.0f on qualifying account", amount, minAmount)
}

func (e *FraudRulesEngine) evalPattern(ctx context.Context, rule entities.FraudRule, tx *entities.MonitoredTransaction) (bool, string) {
	cond := rule.Conditions
	pattern, _ := cond["pattern"].(string)

	switch pattern {
	case "fund_through":
		return e.evalFundThrough(ctx, cond, tx)
	case "structuring":
		return e.evalStructuring(ctx, cond, tx)
	}
	return false, ""
}

func (e *FraudRulesEngine) evalFundThrough(ctx context.Context, cond map[string]interface{}, tx *entities.MonitoredTransaction) (bool, string) {
	if tx.Type != "withdrawal" {
		return false, ""
	}

	ratio := getFloat(cond, "withdrawal_ratio")
	maxDelay := getFloat(cond, "max_delay_seconds")
	if ratio == 0 {
		ratio = 0.8
	}
	if maxDelay == 0 {
		maxDelay = 3600
	}

	// Find recent deposits within the time window
	var depositAmount decimal.Decimal
	var depositTime time.Time
	err := e.db.QueryRowContext(ctx, `
		SELECT amount, created_at FROM transactions 
		WHERE user_id = $1 AND type = 'deposit' AND status = 'completed'
		AND created_at > NOW() - INTERVAL '1 second' * $2
		ORDER BY created_at DESC LIMIT 1`,
		tx.UserID, maxDelay).Scan(&depositAmount, &depositTime)

	if err != nil {
		return false, ""
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

func (e *FraudRulesEngine) evalStructuring(ctx context.Context, cond map[string]interface{}, tx *entities.MonitoredTransaction) (bool, string) {
	if tx.Type != "deposit" {
		return false, ""
	}

	threshold := getFloat(cond, "threshold")
	margin := getFloat(cond, "margin")
	countNeeded := getFloat(cond, "count")
	windowHours := getFloat(cond, "window_hours")

	if threshold == 0 || margin == 0 || countNeeded == 0 {
		return false, ""
	}

	amount := tx.Amount.InexactFloat64()
	// Check if this deposit is just under the threshold
	if amount < (threshold-margin) || amount >= threshold {
		return false, ""
	}

	// Count similar deposits in window
	window := time.Duration(windowHours) * time.Hour
	var count int
	e.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM transactions 
		WHERE user_id = $1 AND type = 'deposit' 
		AND amount >= $2 AND amount < $3
		AND created_at > $4`,
		tx.UserID, threshold-margin, threshold, time.Now().Add(-window)).Scan(&count)

	if float64(count) >= countNeeded {
		return true, fmt.Sprintf("%d deposits between $%.0f-$%.0f in %v (structuring pattern)",
			count, threshold-margin, threshold, window)
	}

	return false, ""
}

func (e *FraudRulesEngine) evalDevice(ctx context.Context, rule entities.FraudRule, tx *entities.MonitoredTransaction) (bool, string) {
	cond := rule.Conditions
	minAmount := getFloat(cond, "min_amount")
	maxDeviceAgeHours := getFloat(cond, "max_device_age_hours")
	maxAccountsPerDevice := getFloat(cond, "max_accounts_per_device")

	if maxAccountsPerDevice > 0 && tx.DeviceID != "" {
		var accountCount int
		e.db.QueryRowContext(ctx, `
			SELECT COUNT(DISTINCT user_id) FROM device_account_links 
			WHERE device_fingerprint = $1 AND created_at > NOW() - INTERVAL '30 days'`,
			tx.DeviceID).Scan(&accountCount)

		if float64(accountCount) > maxAccountsPerDevice {
			return true, fmt.Sprintf("device linked to %d accounts (max: %.0f)", accountCount, maxAccountsPerDevice)
		}
	}

	if maxDeviceAgeHours > 0 && tx.DeviceID != "" {
		amount := tx.Amount.InexactFloat64()
		if minAmount > 0 && amount < minAmount {
			return false, ""
		}

		var deviceCreatedAt time.Time
		err := e.db.QueryRowContext(ctx, `
			SELECT created_at FROM known_devices 
			WHERE user_id = $1 AND fingerprint = $2`,
			tx.UserID, tx.DeviceID).Scan(&deviceCreatedAt)

		if err == nil {
			ageHours := time.Since(deviceCreatedAt).Hours()
			if ageHours < maxDeviceAgeHours {
				return true, fmt.Sprintf("device age %.1fh (max: %.0fh) with amount $%.2f", ageHours, maxDeviceAgeHours, amount)
			}
		}
	}

	return false, ""
}

func (e *FraudRulesEngine) evalCustom(ctx context.Context, rule entities.FraudRule, tx *entities.MonitoredTransaction) (bool, string) {
	cond := rule.Conditions
	minAmount := getFloat(cond, "min_amount")
	hourStart := int(getFloat(cond, "hour_start"))
	hourEnd := int(getFloat(cond, "hour_end"))

	amount := tx.Amount.InexactFloat64()
	if minAmount > 0 && amount < minAmount {
		return false, ""
	}

	hour := time.Now().Hour()
	if hourStart > 0 && hourEnd > 0 {
		if hour >= hourStart && hour <= hourEnd {
			return true, fmt.Sprintf("transaction at %d:00 (unusual hours %d-%d) amount $%.2f", hour, hourStart, hourEnd, amount)
		}
	}

	return false, ""
}

// CreateAlert persists a fraud alert to the database.
func (e *FraudRulesEngine) CreateAlert(ctx context.Context, alert *entities.FraudRuleAlert) error {
	details, _ := json.Marshal(alert.Details)
	_, err := e.db.ExecContext(ctx, `
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

func getFloat(m map[string]interface{}, key string) float64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case json.Number:
		f, _ := val.Float64()
		return f
	}
	return 0
}
