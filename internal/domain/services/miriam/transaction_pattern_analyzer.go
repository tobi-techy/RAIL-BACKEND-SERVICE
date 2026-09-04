package miriam

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// TransferPattern represents an analyzed pattern for a specific counterparty.
type TransferPattern struct {
	Counterparty   string          `json:"counterparty"`
	Relationship   string          `json:"relationship"` // "family", "regular", "service", "unknown"
	TransferCount  int             `json:"transfer_count"`
	TotalAmount    decimal.Decimal `json:"total_amount"`
	AvgAmount      decimal.Decimal `json:"avg_amount"`
	LastTransferAt time.Time       `json:"last_transfer_at"`
	Frequency      string          `json:"frequency"` // "daily", "weekly", "biweekly", "monthly", "irregular"
	IsRecurring    bool            `json:"is_recurring"`
	MemberSince    time.Time       `json:"member_since"`
}

// PatternAnalysisResult holds all patterns for a user.
type PatternAnalysisResult struct {
	FamilyRecipients  []TransferPattern `json:"family_recipients"`
	RegularRecipients []TransferPattern `json:"regular_recipients"`
	ServicePayments   []TransferPattern `json:"service_payments"`
	Summary           string            `json:"summary"`
	TotalRecipients   int               `json:"total_recipients"`
	TotalTransfers    int               `json:"total_transfers"`
}

// UserProvider provides user name data for family matching.
type UserProvider interface {
	GetUserLastName(ctx context.Context, userID uuid.UUID) (string, error)
}

// TransferProvider provides outgoing transfer data for pattern analysis.
type TransferProvider interface {
	GetOutgoingTransfers(ctx context.Context, userID uuid.UUID, since time.Time) ([]TransferRecord, error)
}

// TransferRecord is a normalized outgoing transfer for pattern analysis.
type TransferRecord struct {
	Counterparty string          `db:"counterparty"`
	Amount       decimal.Decimal `db:"amount"`
	CreatedAt    time.Time       `db:"created_at"`
	Currency     string          `db:"currency"`
}

// TransactionPatternAnalyzer detects transfer patterns, family relationships,
// and behavioral clusters from enriched transaction data.
type TransactionPatternAnalyzer struct {
	userProvider     UserProvider
	transferProvider TransferProvider
	logger           *zap.Logger
}

// NewTransactionPatternAnalyzer creates the analyzer.
func NewTransactionPatternAnalyzer(userProvider UserProvider, transferProvider TransferProvider, logger *zap.Logger) *TransactionPatternAnalyzer {
	return &TransactionPatternAnalyzer{
		userProvider:     userProvider,
		transferProvider: transferProvider,
		logger:           logger,
	}
}

// AnalyzePatterns runs the full pattern analysis for a user.
func (a *TransactionPatternAnalyzer) AnalyzePatterns(ctx context.Context, userID uuid.UUID) (*PatternAnalysisResult, error) {
	userLastName, err := a.userProvider.GetUserLastName(ctx, userID)
	if err != nil {
		a.logger.Debug("failed to get user last name for pattern analysis", zap.Error(err))
		userLastName = ""
	}

	since := time.Now().UTC().AddDate(0, -6, 0) // 6 months lookback

	transfers, err := a.transferProvider.GetOutgoingTransfers(ctx, userID, since)
	if err != nil {
		return nil, err
	}

	if len(transfers) == 0 {
		return &PatternAnalysisResult{}, nil
	}

	// Group by counterparty
	grouped := groupByCounterparty(transfers)

	// Analyze each counterparty
	patterns := make([]TransferPattern, 0, len(grouped))
	for counterparty, txns := range grouped {
		pattern := analyzeCounterparty(counterparty, txns, userLastName)
		patterns = append(patterns, pattern)
	}

	// Classify patterns
	result := &PatternAnalysisResult{
		TotalRecipients: len(patterns),
	}

	for _, p := range patterns {
		result.TotalTransfers += p.TransferCount
		switch p.Relationship {
		case "family":
			result.FamilyRecipients = append(result.FamilyRecipients, p)
		case "service":
			result.ServicePayments = append(result.ServicePayments, p)
		default:
			result.RegularRecipients = append(result.RegularRecipients, p)
		}
	}

	result.Summary = buildPatternSummary(result)
	return result, nil
}

// groupByCounterparty groups transfer records by normalized counterparty name.
func groupByCounterparty(transfers []TransferRecord) map[string][]TransferRecord {
	grouped := make(map[string][]TransferRecord)
	for _, t := range transfers {
		key := strings.ToLower(strings.TrimSpace(t.Counterparty))
		if key == "" {
			continue
		}
		grouped[key] = append(grouped[key], t)
	}
	return grouped
}

// analyzeCounterparty analyzes a single counterparty's transfer pattern.
func analyzeCounterparty(counterparty string, txns []TransferRecord, userLastName string) TransferPattern {
	if len(txns) == 0 {
		return TransferPattern{Counterparty: counterparty}
	}

	// Sort by date ascending
	sort.Slice(txns, func(i, j int) bool {
		return txns[i].CreatedAt.Before(txns[j].CreatedAt)
	})

	totalAmount := decimal.Zero
	for _, t := range txns {
		totalAmount = totalAmount.Add(t.Amount)
	}

	avgAmount := totalAmount.Div(decimal.NewFromInt(int64(len(txns))))

	pattern := TransferPattern{
		Counterparty:   counterparty,
		TransferCount:  len(txns),
		TotalAmount:    totalAmount,
		AvgAmount:      avgAmount,
		LastTransferAt: txns[len(txns)-1].CreatedAt,
		MemberSince:    txns[0].CreatedAt,
	}

	// Detect relationship
	pattern.Relationship = detectRelationship(counterparty, userLastName, len(txns))

	// Detect frequency
	pattern.Frequency, pattern.IsRecurring = detectFrequency(txns)

	return pattern
}

// detectRelationship determines the relationship type based on name matching and patterns.
func detectRelationship(counterparty, userLastName string, transferCount int) string {
	counterpartyLower := strings.ToLower(strings.TrimSpace(counterparty))
	userLastLower := strings.ToLower(strings.TrimSpace(userLastName))

	if userLastLower == "" {
		if transferCount >= 5 {
			return "regular"
		}
		return "unknown"
	}

	// Check for service/merchant indicators first
	serviceIndicators := []string{
		"shop", "store", "market", "mart", "ltd", "inc", "corp", "llc",
		"bank", "pay", "transfer", "remittance", "wallet", "exchange",
		"uber", "bolt", "lyft", "airbnb", "netflix", "spotify",
	}
	for _, indicator := range serviceIndicators {
		if strings.Contains(counterpartyLower, indicator) {
			return "service"
		}
	}

	// Extract last name from counterparty (assume last word is last name)
	words := strings.Fields(counterpartyLower)
	if len(words) == 0 {
		return "unknown"
	}
	counterpartyLastName := words[len(words)-1]

	// Direct last name match → likely family
	if counterpartyLastName == userLastLower {
		return "family"
	}

	// Compound name match with word-boundary check (e.g., "Okafor-Smith" contains "Smith")
	// Split on common separators to avoid false positives like "Lee" matching "Lester"
	if strings.Contains(counterpartyLastName, "-") {
		parts := strings.Split(counterpartyLastName, "-")
		for _, part := range parts {
			if part == userLastLower {
				return "family"
			}
		}
	}

	// Check for common family title indicators in the name
	familyIndicators := []string{
		"mama", "papa", "brother", "sister", "uncle", "auntie", "aunt",
		"cousin", "nephew", "niece", "in-law", "son", "daughter",
		"chief", "elder", "queen mother",
	}
	for _, indicator := range familyIndicators {
		if strings.Contains(counterpartyLower, indicator) {
			return "family"
		}
	}

	// High frequency + consistent amounts → regular contact
	if transferCount >= 3 {
		return "regular"
	}

	return "unknown"
}

// detectFrequency determines how often transfers occur to this counterparty.
func detectFrequency(txns []TransferRecord) (string, bool) {
	if len(txns) < 2 {
		return "irregular", false
	}

	// Calculate gaps between consecutive transfers
	gaps := make([]float64, 0, len(txns)-1)
	for i := 1; i < len(txns); i++ {
		gap := txns[i].CreatedAt.Sub(txns[i-1].CreatedAt).Hours()
		gaps = append(gaps, gap)
	}

	// Median gap
	sort.Float64s(gaps)
	medianGap := gaps[len(gaps)/2]

	// Coefficient of variation — lower = more regular
	avg := mean(gaps)
	stddevVal := stddev(gaps, avg)
	cv := 0.0
	if avg > 0 {
		cv = stddevVal / avg
	}

	// Classify by median gap and regularity
	isRecurring := cv < 0.5 && len(txns) >= 3

	switch {
	case medianGap < 48: // ~2 days
		return "daily", isRecurring
	case medianGap < 8*7: // ~8 days → weekly
		return "weekly", isRecurring
	case medianGap < 8*14: // ~16 days → biweekly
		return "biweekly", isRecurring
	case medianGap < 8*35: // ~35 days → monthly
		return "monthly", isRecurring
	default:
		return "irregular", false
	}
}

// buildPatternSummary creates a human-readable summary for Miriam's context.
func buildPatternSummary(result *PatternAnalysisResult) string {
	if result.TotalRecipients == 0 {
		return ""
	}

	var parts []string

	if len(result.FamilyRecipients) > 0 {
		names := make([]string, 0, len(result.FamilyRecipients))
		for _, f := range result.FamilyRecipients {
			names = append(names, fmt.Sprintf("%s (%s, %d transfers)", f.Counterparty, f.Frequency, f.TransferCount))
		}
		parts = append(parts, "Family: "+strings.Join(names, "; "))
	}

	if len(result.RegularRecipients) > 0 {
		names := make([]string, 0, len(result.RegularRecipients))
		for _, r := range result.RegularRecipients {
			names = append(names, fmt.Sprintf("%s (%s, %d transfers)", r.Counterparty, r.Frequency, r.TransferCount))
		}
		parts = append(parts, "Regular: "+strings.Join(names, "; "))
	}

	if len(result.ServicePayments) > 0 {
		parts = append(parts, fmt.Sprintf("Services: %d recipients", len(result.ServicePayments)))
	}

	summary := fmt.Sprintf("[TRANSFER PATTERNS: %d transfers to %d recipients. %s]",
		result.TotalTransfers, result.TotalRecipients, strings.Join(parts, ". "))
	return summary
}

func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func stddev(vals []float64, avg float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		diff := v - avg
		sum += diff * diff
	}
	return math.Sqrt(sum / float64(len(vals)))
}
