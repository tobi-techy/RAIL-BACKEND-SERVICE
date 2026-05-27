package miriam

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// TransactionProvider reads user transactions.
type TransactionProvider interface {
	GetUserTransactions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]entities.Transaction, error)
}

// ObligationAutoDetector analyzes transaction patterns to detect recurring
// payments (subscriptions, rent, utilities) and suggests obligations.
type ObligationAutoDetector struct {
	transactions TransactionProvider
	obligations  ObligationProvider
	balances     BalanceProvider
	logger       *zap.Logger
}

// NewObligationAutoDetector creates an obligation auto-detector.
func NewObligationAutoDetector(
	transactions TransactionProvider,
	obligations ObligationProvider,
	balances BalanceProvider,
	logger *zap.Logger,
) *ObligationAutoDetector {
	return &ObligationAutoDetector{
		transactions: transactions, obligations: obligations, balances: balances, logger: logger,
	}
}

// DetectedObligation is a suggested obligation from transaction analysis.
type DetectedObligation struct {
	UserID          uuid.UUID       `json:"user_id"`
	MerchantName    string          `json:"merchant_name"`
	Category        string          `json:"category"`
	EstimatedAmount decimal.Decimal `json:"estimated_amount"`
	Currency        string          `json:"currency"`
	Cadence         string          `json:"cadence"`
	DueDay          int             `json:"due_day"`
	Confidence      decimal.Decimal `json:"confidence"`
	Evidence        json.RawMessage `json:"evidence"`
	CreatedAt       time.Time       `json:"created_at"`
}

// DetectRecurringPayments analyzes the last 90 days of transactions to
// find recurring payment patterns and returns suggested obligations.
func (d *ObligationAutoDetector) DetectRecurringPayments(ctx context.Context, userID uuid.UUID) ([]DetectedObligation, error) {
	if d.transactions == nil {
		return nil, nil
	}
	// Get last 120 days of transactions (enough to detect monthly patterns)
	transactions, err := d.transactions.GetUserTransactions(ctx, userID, 200, 0)
	if err != nil {
		return nil, fmt.Errorf("get transactions: %w", err)
	}

	if len(transactions) == 0 {
		return nil, nil
	}

	// Group by description/merchant
	merchantGroups := make(map[string][]entities.Transaction)
	for _, tx := range transactions {
		// Only consider money-out transactions
		if tx.Amount.Sign() >= 0 || tx.Status != "confirmed" {
			continue
		}
		merchant := strings.ToLower(strings.TrimSpace(tx.Description))
		if merchant == "" {
			continue
		}
		merchantGroups[merchant] = append(merchantGroups[merchant], tx)
	}

	var detected []DetectedObligation
	now := time.Now().UTC()

	for merchant, txs := range merchantGroups {
		if len(txs) < 2 {
			continue // Need at least 2 occurrences for pattern detection
		}

		// Sort by date
		sort.Slice(txs, func(i, j int) bool {
			return txs[i].CreatedAt.Before(txs[j].CreatedAt)
		})

		// Analyze intervals between transactions
		intervals := analyzeIntervals(txs)
		if intervals == nil {
			continue
		}

		// Determine cadence from intervals
		cadence := determineCadence(intervals.AvgDays)
		if cadence == "" {
			continue
		}

		// Calculate average amount
		avgAmount := calculateAvgAmount(txs)

		// Determine confidence based on consistency
		confidence := calculateConfidence(intervals, txs)
		if confidence.LessThan(decimal.NewFromFloat(0.6)) {
			continue
		}

		// Skip if already tracked as obligation
		if d.isAlreadyTracked(ctx, userID, merchant) {
			continue
		}

		// Determine due day (most common day of month)
		dueDay := mostCommonDayOfMonth(txs)

		// Determine category
		category := categorizeObligation(merchant)

		evidence := map[string]interface{}{
			"transaction_count": len(txs),
			"avg_interval_days": intervals.AvgDays,
			"avg_amount":        avgAmount.InexactFloat64(),
			"consistency":       intervals.Consistency,
			"first_seen":        txs[0].CreatedAt,
			"last_seen":         txs[len(txs)-1].CreatedAt,
		}

		detected = append(detected, DetectedObligation{
			UserID:          userID,
			MerchantName:    normalizeMerchantName(merchant),
			Category:        category,
			EstimatedAmount: avgAmount,
			Currency:        "USD",
			Cadence:         cadence,
			DueDay:          dueDay,
			Confidence:      confidence,
			Evidence:        mustJSON(evidence),
			CreatedAt:       now,
		})
	}

	// Sort by confidence (highest first)
	sort.Slice(detected, func(i, j int) bool {
		return detected[i].Confidence.GreaterThan(detected[j].Confidence)
	})

	return detected, nil
}

type intervalStats struct {
	AvgDays     float64
	Consistency float64 // 0-1, higher = more consistent
}

func analyzeIntervals(txs []entities.Transaction) *intervalStats {
	if len(txs) < 2 {
		return nil
	}

	var totalDays float64
	var intervals []float64
	for i := 1; i < len(txs); i++ {
		days := txs[i].CreatedAt.Sub(txs[i-1].CreatedAt).Hours() / 24
		totalDays += days
		intervals = append(intervals, days)
	}

	avg := totalDays / float64(len(txs)-1)

	// Calculate consistency (inverse of coefficient of variation)
	if avg == 0 {
		return nil
	}
	var variance float64
	for _, d := range intervals {
		diff := d - avg
		variance += diff * diff
	}
	// Sample variance (n-1) to avoid underestimating variance for small samples
	n := float64(len(intervals))
	if n > 1 {
		variance /= (n - 1)
	} else {
		variance = 0
	}
	stdDev := math.Sqrt(variance)
	cv := stdDev / avg               // coefficient of variation
	consistency := math.Max(0, 1-cv) // 1 = perfectly consistent, 0 = highly variable

	return &intervalStats{
		AvgDays:     avg,
		Consistency: consistency,
	}
}

func determineCadence(avgDays float64) string {
	switch {
	case avgDays >= 25 && avgDays <= 35:
		return "monthly"
	case avgDays >= 12 && avgDays <= 16:
		return "biweekly"
	case avgDays >= 5 && avgDays <= 9:
		return "weekly"
	default:
		return "" // not a recognizable cadence
	}
}

func calculateAvgAmount(txs []entities.Transaction) decimal.Decimal {
	if len(txs) == 0 {
		return decimal.Zero
	}
	total := decimal.Zero
	for _, tx := range txs {
		total = total.Add(tx.Amount.Abs())
	}
	return total.Div(decimal.NewFromInt(int64(len(txs))))
}

func calculateConfidence(intervals *intervalStats, txs []entities.Transaction) decimal.Decimal {
	if intervals == nil || len(txs) < 2 {
		return decimal.Zero
	}

	// Base confidence from consistency
	confidence := intervals.Consistency

	// Boost for more occurrences
	if len(txs) >= 6 {
		confidence += 0.15
	} else if len(txs) >= 3 {
		confidence += 0.1
	}

	return decimal.NewFromFloat(math.Min(0.95, confidence))
}

func mostCommonDayOfMonth(txs []entities.Transaction) int {
	dayCounts := make(map[int]int)
	for _, tx := range txs {
		dayCounts[tx.CreatedAt.Day()]++
	}

	maxDay := 1
	maxCount := 0
	for day, count := range dayCounts {
		if count > maxCount {
			maxCount = count
			maxDay = day
		}
	}
	return maxDay
}

func categorizeObligation(merchant string) string {
	subscriptionKeywords := []string{"netflix", "spotify", "youtube", "apple", "google", "amazon", "prime", "disney", "hulu", "sub", "membership"}
	billKeywords := []string{"electric", "water", "gas", "utility", "internet", "phone", "mobile", "telecom"}
	rentKeywords := []string{"rent", "landlord", "property", "housing"}
	insuranceKeywords := []string{"insurance", "premium", "coverage"}

	for _, kw := range subscriptionKeywords {
		if strings.Contains(merchant, kw) {
			return "subscription"
		}
	}
	for _, kw := range billKeywords {
		if strings.Contains(merchant, kw) {
			return "vendor_bill"
		}
	}
	for _, kw := range rentKeywords {
		if strings.Contains(merchant, kw) {
			return "rent"
		}
	}
	for _, kw := range insuranceKeywords {
		if strings.Contains(merchant, kw) {
			return "insurance"
		}
	}

	return "other"
}

func normalizeMerchantName(merchant string) string {
	// Capitalize first letter of each word
	words := strings.Fields(merchant)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

func (d *ObligationAutoDetector) isAlreadyTracked(ctx context.Context, userID uuid.UUID, merchant string) bool {
	if d.obligations == nil {
		return false
	}

	obligations, err := d.obligations.ListActive(ctx, userID)
	if err != nil {
		return false
	}

	merchantLower := strings.ToLower(merchant)
	for _, o := range obligations {
		if strings.Contains(strings.ToLower(o.Name), merchantLower) {
			return true
		}
	}

	return false
}
