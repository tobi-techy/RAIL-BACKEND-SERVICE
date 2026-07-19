package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// --- Types ---

type AnomalyType string

const (
	AnomalyBillSpike        AnomalyType = "bill_spike"
	AnomalyDuplicateCharge  AnomalyType = "duplicate_charge"
	AnomalyFraudSignal      AnomalyType = "fraud_signal"
	AnomalySpendingAccel    AnomalyType = "spending_acceleration"
	AnomalyMerchantPattern  AnomalyType = "merchant_pattern"
)

type AnomalySeverity string

const (
	SeverityLow      AnomalySeverity = "low"
	SeverityMedium   AnomalySeverity = "medium"
	SeverityHigh     AnomalySeverity = "high"
	SeverityCritical AnomalySeverity = "critical"
)

type AnomalyResult struct {
	Type        AnomalyType            `json:"type"`
	Severity    AnomalySeverity        `json:"severity"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Details     map[string]any         `json:"details,omitempty"`
	DetectedAt  time.Time              `json:"detected_at"`
}

// AnomalyStore persists recent anomaly results for orchestrator reference.
type AnomalyStore interface {
	Set(ctx context.Context, userID uuid.UUID, results []AnomalyResult, ttl time.Duration) error
	Get(ctx context.Context, userID uuid.UUID) ([]AnomalyResult, error)
}

// --- Interfaces ---

type AnomalyCategoryReader interface {
	GetSpendingByCategory(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]entities.SpendingByCategory, error)
}

type AnomalyMerchantReader interface {
	GetSpendingByMerchant(ctx context.Context, userID uuid.UUID, start, end time.Time, limit int) ([]entities.SpendingByMerchant, error)
}

type AnomalyOutflowReader interface {
	GetRecentOutflows(ctx context.Context, userID uuid.UUID, start, end time.Time, limit int) ([]entities.SpendingTransaction, error)
}

type AnomalyFlowReader interface {
	GetMoneyFlow(ctx context.Context, userID uuid.UUID, start, end time.Time) (*entities.MoneyFlowSummary, error)
}

// --- Default thresholds ---

var (
	DefaultBillSpikeThreshold     = decimal.NewFromFloat(1.25)
	DefaultSpendingAccelThreshold = decimal.NewFromFloat(1.5)
	DefaultDuplicateChargeWindow  = 72 * time.Hour
	DefaultLargeTxThreshold       = decimal.NewFromInt(500)
	DefaultMerchantVisitThreshold = 8
	DefaultNoiseFloor             = decimal.NewFromInt(20)
)

// --- Engine ---

type AnomalyEngine struct {
	categories  AnomalyCategoryReader
	merchants   AnomalyMerchantReader
	outflows    AnomalyOutflowReader
	flow        AnomalyFlowReader
	logger      *zap.Logger

	BillSpikeThreshold     decimal.Decimal
	SpendingAccelThreshold decimal.Decimal
	DuplicateChargeWindow  time.Duration
	LargeTxThreshold       decimal.Decimal
	MerchantVisitThreshold int
	NoiseFloor             decimal.Decimal
}

func NewAnomalyEngine(
	categories AnomalyCategoryReader,
	merchants AnomalyMerchantReader,
	outflows AnomalyOutflowReader,
	flow AnomalyFlowReader,
	logger *zap.Logger,
) *AnomalyEngine {
	return &AnomalyEngine{
		categories:             categories,
		merchants:              merchants,
		outflows:               outflows,
		flow:                   flow,
		logger:                 logger,
		BillSpikeThreshold:     DefaultBillSpikeThreshold,
		SpendingAccelThreshold: DefaultSpendingAccelThreshold,
		DuplicateChargeWindow:  DefaultDuplicateChargeWindow,
		LargeTxThreshold:       DefaultLargeTxThreshold,
		MerchantVisitThreshold: DefaultMerchantVisitThreshold,
		NoiseFloor:             DefaultNoiseFloor,
	}
}

// RunAllChecks runs every anomaly detection check and returns all results.
func (e *AnomalyEngine) RunAllChecks(ctx context.Context, userID uuid.UUID, now time.Time) []AnomalyResult {
	var results []AnomalyResult

	results = append(results, e.CheckBillSpikes(ctx, userID, now)...)
	results = append(results, e.CheckDuplicateCharges(ctx, userID, now)...)
	results = append(results, e.CheckFraudSignals(ctx, userID, now)...)
	results = append(results, e.CheckSpendingAcceleration(ctx, userID, now)...)
	results = append(results, e.CheckMerchantPatterns(ctx, userID, now)...)

	return results
}

// CheckBillSpikes detects categories where spending is significantly above the 3-month trailing average.
func (e *AnomalyEngine) CheckBillSpikes(ctx context.Context, userID uuid.UUID, now time.Time) []AnomalyResult {
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	trailingStart := monthStart.AddDate(0, -3, 0)

	current, err := e.categories.GetSpendingByCategory(ctx, userID, monthStart, now)
	if err != nil {
		e.logger.Warn("anomaly: bill spike category query failed", zap.String("user_id", userID.String()), zap.Error(err))
		return nil
	}
	trailing, err := e.categories.GetSpendingByCategory(ctx, userID, trailingStart, monthStart)
	if err != nil {
		e.logger.Warn("anomaly: bill spike trailing query failed", zap.String("user_id", userID.String()), zap.Error(err))
		return nil
	}

	if len(current) == 0 || len(trailing) == 0 {
		return nil
	}

	trailingMap := make(map[string]decimal.Decimal, len(trailing))
	for _, t := range trailing {
		trailingMap[t.Category] = t.Total
	}

	nowDays := int(now.Sub(monthStart).Hours() / 24)
	if nowDays < 1 {
		nowDays = 1
	}
	trailingMonths := decimal.NewFromInt(3)

	var results []AnomalyResult
	for _, c := range current {
		if c.Total.LessThan(e.NoiseFloor) {
			continue
		}
		avg, ok := trailingMap[c.Category]
		if !ok || avg.IsZero() {
			continue
		}
		avgMonthly := avg.Div(trailingMonths)

		dailyRate := c.Total.Div(decimal.NewFromInt(int64(nowDays)))
		projectedMonthly := dailyRate.Mul(decimal.NewFromInt(30))

		if projectedMonthly.GreaterThan(avgMonthly.Mul(e.BillSpikeThreshold)) && projectedMonthly.Sub(avgMonthly).GreaterThan(e.NoiseFloor) {
			ratio := projectedMonthly.Div(avgMonthly).Sub(decimal.NewFromInt(1)).Mul(decimal.NewFromInt(100))
			severity := SeverityMedium
			if ratio.GreaterThan(decimal.NewFromInt(50)) {
				severity = SeverityHigh
			}
			results = append(results, AnomalyResult{
				Type:        AnomalyBillSpike,
				Severity:    severity,
				Title:       fmt.Sprintf("%s spending spike", c.Category),
				Description: fmt.Sprintf("Your %s spending is tracking at %s/month — normally %s/month (%.0f%% increase).", c.Category, projectedMonthly.StringFixed(2), avgMonthly.StringFixed(2), ratio.InexactFloat64()),
				Details: map[string]any{
					"category":          c.Category,
					"projected_monthly": projectedMonthly.StringFixed(2),
					"avg_monthly":       avgMonthly.StringFixed(2),
					"ratio_pct":         ratio.InexactFloat64(),
				},
				DetectedAt: now,
			})
		}
	}
	return results
}

// CheckDuplicateCharges detects same-merchant, same-amount transactions within the DuplicateChargeWindow.
func (e *AnomalyEngine) CheckDuplicateCharges(ctx context.Context, userID uuid.UUID, now time.Time) []AnomalyResult {
	since := now.Add(-96 * time.Hour)
	txns, err := e.outflows.GetRecentOutflows(ctx, userID, since, now, 50)
	if err != nil {
		e.logger.Warn("anomaly: duplicate charge query failed", zap.String("user_id", userID.String()), zap.Error(err))
		return nil
	}
	if len(txns) < 2 {
		return nil
	}

	type key struct {
		merchant string
		amount   string
	}
	buckets := make(map[key][]entities.SpendingTransaction)
	for _, txn := range txns {
		// Skip withdrawals and P2P transfers — only meaningful for merchant card charges
		if isWithdrawalOrP2P(txn.Category) {
			continue
		}
		// Use Source as merchant name, Amount stringified for exact comparison
		k := key{merchant: txn.Source, amount: txn.Amount.StringFixed(2)}
		buckets[k] = append(buckets[k], txn)
	}

	var results []AnomalyResult
	cutoff := now.Add(-e.DuplicateChargeWindow)
	for _, group := range buckets {
		if len(group) < 2 {
			continue
		}
		withinWindow := 0
		for _, t := range group {
			tTime, err := time.Parse("2006-01-02", t.Date)
			if err == nil && tTime.After(cutoff) {
				withinWindow++
			}
		}
		if withinWindow < 2 {
			continue
		}
		merchant := group[0].Source
		results = append(results, AnomalyResult{
			Type:        AnomalyDuplicateCharge,
			Severity:    SeverityHigh,
			Title:       "Possible duplicate charge detected",
			Description: fmt.Sprintf("I spotted %d charges of %s at %s within the last %v — this could be a duplicate.", withinWindow, group[0].Amount.StringFixed(2), merchant, e.DuplicateChargeWindow),
			Details: map[string]any{
				"merchant":  merchant,
				"amount":    group[0].Amount.StringFixed(2),
				"count":     withinWindow,
				"window_hs": e.DuplicateChargeWindow.Hours(),
			},
			DetectedAt: now,
		})
	}
	return results
}

// CheckFraudSignals flags large transactions and transactions in unusual categories.
func (e *AnomalyEngine) CheckFraudSignals(ctx context.Context, userID uuid.UUID, now time.Time) []AnomalyResult {
	since := now.Add(-24 * time.Hour)
	txns, err := e.outflows.GetRecentOutflows(ctx, userID, since, now, 30)
	if err != nil {
		e.logger.Warn("anomaly: fraud signal query failed", zap.String("user_id", userID.String()), zap.Error(err))
		return nil
	}
	if len(txns) == 0 {
		return nil
	}

	highRiskCategories := map[string]bool{
		"Crypto Withdrawal": true,
		"Gambling":          true,
		"Casino":            true,
		"Cash Advance":      true,
	}

	var results []AnomalyResult
	for _, txn := range txns {
		if txn.Amount.GreaterThan(e.LargeTxThreshold) {
			results = append(results, AnomalyResult{
				Type:        AnomalyFraudSignal,
				Severity:    SeverityHigh,
				Title:       "Large transaction detected",
				Description: fmt.Sprintf("There's a %s charge of %s at %s — want to confirm this?", txn.Category, txn.Amount.StringFixed(2), txn.Source),
				Details: map[string]any{
					"amount":   txn.Amount.StringFixed(2),
					"merchant": txn.Source,
					"category": txn.Category,
					"date":     txn.Date,
				},
				DetectedAt: now,
			})
		}
		if highRiskCategories[txn.Category] {
			results = append(results, AnomalyResult{
				Type:        AnomalyFraudSignal,
				Severity:    SeverityMedium,
				Title:       fmt.Sprintf("Transaction in %s", txn.Category),
				Description: fmt.Sprintf("I noticed a %s transaction of %s at %s. Did you authorize this?", txn.Category, txn.Amount.StringFixed(2), txn.Source),
				Details: map[string]any{
					"amount":   txn.Amount.StringFixed(2),
					"merchant": txn.Source,
					"category": txn.Category,
					"date":     txn.Date,
				},
				DetectedAt: now,
			})
		}
	}
	return results
}

// CheckSpendingAcceleration compares month-to-date spending rate vs the trailing 3-month average.
func (e *AnomalyEngine) CheckSpendingAcceleration(ctx context.Context, userID uuid.UUID, now time.Time) []AnomalyResult {
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	trailingStart := monthStart.AddDate(0, -3, 0)

	currentFlow, err := e.flow.GetMoneyFlow(ctx, userID, monthStart, now)
	if err != nil || currentFlow == nil {
		e.logger.Warn("anomaly: spending accel current query failed", zap.String("user_id", userID.String()), zap.Error(err))
		return nil
	}

	trailingFlow, err := e.flow.GetMoneyFlow(ctx, userID, trailingStart, monthStart)
	if err != nil || trailingFlow == nil {
		e.logger.Warn("anomaly: spending accel trailing query failed", zap.String("user_id", userID.String()), zap.Error(err))
		return nil
	}

	totalCurrent := currentFlow.TotalCardSpend.Add(currentFlow.TotalP2P).Add(currentFlow.TotalWithdrawals)
	totalTrailing := trailingFlow.TotalCardSpend.Add(trailingFlow.TotalP2P).Add(trailingFlow.TotalWithdrawals)

	if totalCurrent.IsZero() || totalCurrent.LessThan(e.NoiseFloor) {
		return nil
	}
	if totalTrailing.IsZero() {
		return nil
	}

	daysElapsed := int(now.Sub(monthStart).Hours() / 24)
	if daysElapsed < 1 {
		daysElapsed = 1
	}
	trailingMonthlyAvg := totalTrailing.Div(decimal.NewFromInt(3))
	dailyRate := totalCurrent.Div(decimal.NewFromInt(int64(daysElapsed)))
	projected := dailyRate.Mul(decimal.NewFromInt(30))

	if projected.GreaterThan(trailingMonthlyAvg.Mul(e.SpendingAccelThreshold)) && projected.Sub(trailingMonthlyAvg).GreaterThan(e.NoiseFloor) {
		ratio := projected.Div(trailingMonthlyAvg).Sub(decimal.NewFromInt(1)).Mul(decimal.NewFromInt(100))
		severity := SeverityMedium
		if ratio.GreaterThan(decimal.NewFromInt(75)) {
			severity = SeverityHigh
		}
		return []AnomalyResult{
			{
				Type:        AnomalySpendingAccel,
				Severity:    severity,
				Title:       "Spending is accelerating",
				Description: fmt.Sprintf("You're on pace to spend %s this month — your average is %s (%.0f%% increase). Let's keep an eye on this.", projected.StringFixed(2), trailingMonthlyAvg.StringFixed(2), ratio.InexactFloat64()),
				Details: map[string]any{
					"projected_monthly":   projected.StringFixed(2),
					"avg_monthly":         trailingMonthlyAvg.StringFixed(2),
					"ratio_pct":           ratio.InexactFloat64(),
					"days_elapsed":        daysElapsed,
				},
				DetectedAt: now,
			},
		}
	}
	return nil
}

// CheckMerchantPatterns flags merchants visited unusually often in the trailing period.
func (e *AnomalyEngine) CheckMerchantPatterns(ctx context.Context, userID uuid.UUID, now time.Time) []AnomalyResult {
	since := now.AddDate(0, 0, -7)
	merchants, err := e.merchants.GetSpendingByMerchant(ctx, userID, since, now, 30)
	if err != nil {
		e.logger.Warn("anomaly: merchant pattern query failed", zap.String("user_id", userID.String()), zap.Error(err))
		return nil
	}
	if len(merchants) == 0 {
		return nil
	}

	highRiskKeywords := []string{
		"draftkings", "fanduel", "bet", "casino", "poker", "lottery",
		"liquor", "vape", "dispensary", "weed", "cannabis",
	}

	var results []AnomalyResult
	for _, m := range merchants {
		if m.Count < e.MerchantVisitThreshold {
			continue
		}
		lower := lowerString(m.Merchant)
		isRisky := false
		for _, kw := range highRiskKeywords {
			if containsString(lower, kw) {
				isRisky = true
				break
			}
		}
		if isRisky {
			results = append(results, AnomalyResult{
				Type:        AnomalyMerchantPattern,
				Severity:    SeverityHigh,
				Title:       "High-risk merchant pattern detected",
				Description: fmt.Sprintf("You've visited %s %d times in the past week — total spend %s. Want me to block it for 30 days?", m.Merchant, m.Count, m.Total.StringFixed(2)),
				Details: map[string]any{
					"merchant":     m.Merchant,
					"visit_count":  m.Count,
					"total_spent":  m.Total.StringFixed(2),
					"pattern_type": "high_risk",
				},
				DetectedAt: now,
			})
		} else if m.Count >= e.MerchantVisitThreshold*2 {
			results = append(results, AnomalyResult{
				Type:        AnomalyMerchantPattern,
				Severity:    SeverityMedium,
				Title:       "Frequent merchant visits detected",
				Description: fmt.Sprintf("You've visited %s %d times in the past week — total spend %s. Just want to make sure this is intentional.", m.Merchant, m.Count, m.Total.StringFixed(2)),
				Details: map[string]any{
					"merchant":     m.Merchant,
					"visit_count":  m.Count,
					"total_spent":  m.Total.StringFixed(2),
					"pattern_type": "high_frequency",
				},
				DetectedAt: now,
			})
		}
	}
	return results
}

// BuildAlertText converts anomaly results into a human-readable push notification.
func BuildAlertText(results []AnomalyResult) (title, body string) {
	if len(results) == 0 {
		return "", ""
	}

	highCount := 0
	for _, r := range results {
		if r.Severity == SeverityHigh || r.Severity == SeverityCritical {
			highCount++
		}
	}

	if highCount > 0 {
		title = fmt.Sprintf("Miriam Alert: %d issue%s found", highCount, pluralSuffix(highCount))
	} else {
		title = "Miriam Morning Check"
	}

	body = "Here's what I found:"
	for _, r := range results {
		prefix := "• "
		if r.Severity == SeverityHigh || r.Severity == SeverityCritical {
			prefix = "⚠️ "
		}
		body += "\n" + prefix + r.Description
	}
	body += "\n\nReply to ask me about any of these."
	return title, body
}

// isWithdrawalOrP2P returns true for categories that represent non-merchant outflows
// that shouldn't trigger duplicate charge detection.
func isWithdrawalOrP2P(category string) bool {
	if len(category) < 5 {
		return false
	}
	// Matches: "NGN Withdrawal", "EUR Withdrawal", "GBP Withdrawal", "USD Withdrawal",
	// "Crypto Withdrawal", "Withdrawal", "P2P Transfer", "P2P Merchant"
	low := lowerString(category)
	return len(low) >= 10 && low[len(low)-10:] == "withdrawal" ||
		len(low) >= 3 && low[:3] == "p2p"
}

func lowerString(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		if s[i] >= 'A' && s[i] <= 'Z' {
			b[i] = s[i] + 32
		} else {
			b[i] = s[i]
		}
	}
	return string(b)
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && containsStringInner(s, substr)
}

func containsStringInner(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := range substr {
			if s[i+j] != substr[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
