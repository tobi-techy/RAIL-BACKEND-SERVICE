package ai

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
)

// Tool names for read-only data tools.
const (
	ToolGetCardTransactions  = "get_card_transactions"
	ToolGetDepositHistory    = "get_deposit_history"
	ToolGetIncomeTrend       = "get_income_trend"
	ToolGetYieldEarned       = "get_yield_earned"
	ToolGetWithdrawalHistory = "get_withdrawal_history"
	ToolGetReceiptHistory    = "get_receipt_history"
)

// CardTransactionProvider returns recent card transactions.
type CardTransactionProvider interface {
	GetTransactionsByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.BridgeCardTransaction, error)
}

// DepositHistoryProvider returns recent deposits.
type DepositHistoryProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Deposit, error)
}

// DepositIncomeProvider optionally returns aggregate deposit trend data.
type DepositIncomeProvider interface {
	GetCompletedMonthlyTotals(ctx context.Context, userID uuid.UUID, since, until time.Time) ([]entities.DepositMonthlyTotal, error)
}

// YieldProvider returns yield data.
type YieldProvider interface {
	GetSnapshotsInWindow(ctx context.Context, userID uuid.UUID, from, to time.Time) ([]*entities.YieldBalanceSnapshot, error)
}

// WithdrawalHistoryProvider returns recent withdrawals.
type WithdrawalHistoryProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Withdrawal, error)
}

// ReceiptHistoryProvider returns stored receipt scans.
type ReceiptHistoryProvider interface {
	GetByUserIDInRange(ctx context.Context, userID uuid.UUID, start, end time.Time, limit int) ([]*entities.ReceiptScan, error)
	GetTotalByCategory(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]entities.SpendingByCategory, error)
}

// SetCardTransactions sets the card transaction provider.
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *Orchestrator) SetCardTransactions(p CardTransactionProvider) {
	o.cardTransactions = p
}

// SetDepositHistory sets the deposit history provider.
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *Orchestrator) SetDepositHistory(p DepositHistoryProvider) {
	o.depositHistory = p
}

// SetYieldProvider sets the yield provider.
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *Orchestrator) SetYieldProvider(p YieldProvider) {
	o.yieldProvider = p
}

// SetWithdrawalHistory sets the withdrawal history provider.
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *Orchestrator) SetWithdrawalHistory(p WithdrawalHistoryProvider) {
	o.withdrawalHistory = p
}

// SetReceiptHistory sets the receipt history provider.
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *Orchestrator) SetReceiptHistory(p ReceiptHistoryProvider) {
	o.receiptHistory = p
}

// SetReceiptSplitter sets the receipt splitter for executing splits on AI confirmation.
func (o *Orchestrator) SetReceiptSplitter(s ReceiptSplitter) {
	o.receiptSplitter = s
}

// SetWithdrawalInitiator sets the withdrawal service for voice-triggered withdrawals.
func (o *Orchestrator) SetWithdrawalInitiator(w WithdrawalInitiator) {
	o.withdrawalInitiator = w
}

// SetBankAccountProvider sets the bank account provider for withdrawal details.
func (o *Orchestrator) SetBankAccountProvider(b BankAccountProvider) {
	o.bankAccountProvider = b
}

// ReadOnlyTools returns tool definitions for read-only data access.
func ReadOnlyTools(hasCards, hasDeposits, hasIncomeTrend, hasYield, hasWithdrawals, hasReceipts bool) []infraai.Tool {
	var tools []infraai.Tool
	if hasCards {
		tools = append(tools, infraai.Tool{
			Name:        ToolGetCardTransactions,
			Description: "Get recent card transactions with merchant details and status. Use when user asks specifically about card purchases or needs merchant-level detail. For a full view of all spending (cards + withdrawals + P2P), use get_recent_transactions instead.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"limit": map[string]interface{}{"type": "integer", "description": "Number of transactions (max 10)", "default": 5},
				},
			},
		})
	}
	if hasDeposits {
		tools = append(tools, infraai.Tool{
			Name:        ToolGetDepositHistory,
			Description: "Get recent deposit history. Use when user asks about their deposits, funding history, or money coming in.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"limit": map[string]interface{}{"type": "integer", "description": "Number of deposits (max 10)", "default": 5},
				},
			},
		})
	}
	if hasIncomeTrend {
		tools = append(tools, infraai.Tool{
			Name:        ToolGetIncomeTrend,
			Description: "Get completed deposit trends and cautious monthly income estimates over time. Use when the user asks about income, money coming in, monthly earnings, deposit cadence, projected earning this month, payday rhythm, or whether their deposits are improving.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"months": map[string]interface{}{"type": "integer", "description": "History window in months, max 12", "default": 6},
				},
			},
		})
	}
	if hasYield {
		tools = append(tools, infraai.Tool{
			Name:        ToolGetYieldEarned,
			Description: "Get yield earned on stash. Use when user asks about interest, yield, earnings on savings, or how much their stash has earned.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"period": map[string]interface{}{"type": "string", "enum": []string{"last_7_days", "last_30_days", "last_90_days"}},
				},
			},
		})
	}
	if hasWithdrawals {
		tools = append(tools, infraai.Tool{
			Name:        ToolGetWithdrawalHistory,
			Description: "Get recent withdrawal history including naira withdrawals, crypto withdrawals, and fiat offramps. Use when user asks about withdrawals, cash outs, NGN conversions, or money leaving their account.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"limit": map[string]interface{}{"type": "integer", "description": "Number of withdrawals (max 10)", "default": 5},
				},
			},
		})
	}
	if hasReceipts {
		tools = append(tools, infraai.Tool{
			Name:        ToolGetReceiptHistory,
			Description: "Get scanned receipt history with full details: merchant, amount, date, category, and individual items purchased. Use when user asks about receipts they've scanned, specific purchases, or wants detailed spending breakdown from receipts. Also includes category totals from all scanned receipts.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"period": map[string]interface{}{"type": "string", "enum": []string{"this_month", "last_month", "last_7_days", "last_30_days"}},
				},
			},
		})
	}
	return tools
}

func (o *Orchestrator) executeCardTransactions(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.cardTransactions == nil {
		return map[string]interface{}{"error": "card transactions not available"}, nil
	}
	limit := 5
	if l, ok := args["limit"].(float64); ok && l > 0 && l <= 10 {
		limit = int(l)
	}
	txns, err := o.cardTransactions.GetTransactionsByUserID(ctx, userID, limit, 0)
	if err != nil {
		return nil, fmt.Errorf("card transactions: %w", err)
	}
	items := make([]map[string]interface{}, len(txns))
	for i, t := range txns {
		merchant := "Unknown"
		if t.MerchantName != nil {
			merchant = *t.MerchantName
		}
		items[i] = map[string]interface{}{
			"amount":   t.Amount.String(),
			"merchant": merchant,
			"date":     t.CreatedAt.Format("Jan 2, 2006"),
			"status":   t.Status,
		}
	}
	return map[string]interface{}{"transactions": items, "count": len(items)}, nil
}

func (o *Orchestrator) executeDepositHistory(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.depositHistory == nil {
		return map[string]interface{}{"error": "deposit history not available"}, nil
	}
	limit := 5
	if l, ok := args["limit"].(float64); ok && l > 0 && l <= 10 {
		limit = int(l)
	}
	// Fetch more than needed so we can filter to completed only
	deposits, err := o.depositHistory.GetByUserID(ctx, userID, limit*3, 0)
	if err != nil {
		return nil, fmt.Errorf("deposit history: %w", err)
	}
	items := make([]map[string]interface{}, 0, limit)
	for _, d := range deposits {
		if len(items) >= limit {
			break
		}
		// Only show confirmed/completed deposits
		if d.Status != "confirmed" && d.Status != "off_ramp_completed" && d.Status != "broker_funded" {
			continue
		}
		items = append(items, map[string]interface{}{
			"direction": "money_in",
			"amount":    d.Amount.String(),
			"token":     string(d.Token),
			"chain":     string(d.Chain),
			"status":    "completed",
			"date":      d.CreatedAt.Format("Jan 2, 2006"),
		})
	}
	return map[string]interface{}{"deposits": items, "count": len(items), "note": "Only showing completed deposits"}, nil
}

func (o *Orchestrator) executeIncomeTrend(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.depositHistory == nil {
		return map[string]interface{}{"error": "deposit history not available"}, nil
	}
	incomeProvider, ok := o.depositHistory.(DepositIncomeProvider)
	if !ok {
		return map[string]interface{}{"error": "income trend not available"}, nil
	}
	months := 6
	if l, ok := args["months"].(float64); ok && l > 0 {
		months = int(l)
	}
	if months < 3 {
		months = 3
	}
	if months > 12 {
		months = 12
	}

	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	historyStart := monthStart.AddDate(0, -(months - 1), 0)
	nextMonth := monthStart.AddDate(0, 1, 0)

	totals, err := incomeProvider.GetCompletedMonthlyTotals(ctx, userID, historyStart, nextMonth)
	if err != nil {
		return nil, fmt.Errorf("income trend: %w", err)
	}

	byMonth := make(map[string]entities.DepositMonthlyTotal, len(totals))
	for _, t := range totals {
		byMonth[t.Month.Format("2006-01")] = t
	}

	series := make([]map[string]interface{}, 0, months)
	currentTotal := decimal.Zero
	currentCount := 0
	priorTotal := decimal.Zero
	priorMonths := 0
	activePriorMonths := 0
	var lastDepositAt *time.Time
	for i := 0; i < months; i++ {
		month := historyStart.AddDate(0, i, 0)
		key := month.Format("2006-01")
		total := decimal.Zero
		count := 0
		if v, ok := byMonth[key]; ok {
			total = v.Total
			count = v.Count
			if v.LastDepositAt != nil && (lastDepositAt == nil || v.LastDepositAt.After(*lastDepositAt)) {
				lastDepositAt = v.LastDepositAt
			}
		}
		if month.Equal(monthStart) {
			currentTotal = total
			currentCount = count
		} else {
			priorMonths++
			priorTotal = priorTotal.Add(total)
			if count > 0 {
				activePriorMonths++
			}
		}
		series = append(series, map[string]interface{}{
			"month": key,
			"total": total.StringFixed(2),
			"count": count,
		})
	}

	avgMonthly := decimal.Zero
	if priorMonths > 0 {
		avgMonthly = priorTotal.Div(decimal.NewFromInt(int64(priorMonths)))
	}
	activeAvgMonthly := decimal.Zero
	if activePriorMonths > 0 {
		activeAvgMonthly = priorTotal.Div(decimal.NewFromInt(int64(activePriorMonths)))
	}

	daysInMonth := nextMonth.Sub(monthStart).Hours() / 24
	elapsedDays := now.Sub(monthStart).Hours()/24 + 1
	proratedEstimate := currentTotal
	if elapsedDays > 0 && currentTotal.IsPositive() {
		proratedEstimate = currentTotal.Div(decimal.NewFromFloat(elapsedDays)).Mul(decimal.NewFromFloat(daysInMonth))
	}
	estimated := proratedEstimate
	if !currentTotal.IsPositive() && avgMonthly.IsPositive() {
		estimated = avgMonthly
	}
	if estimated.LessThan(currentTotal) {
		estimated = currentTotal
	}

	recentDeposits, _ := o.depositHistory.GetByUserID(ctx, userID, 20, 0)
	cadence, cadenceConfidence := inferDepositCadence(recentDeposits)
	confidence := incomeEstimateConfidence(priorMonths, activePriorMonths, currentCount, cadenceConfidence)

	result := map[string]interface{}{
		"window_months":    months,
		"monthly_deposits": series,
		"current_month": map[string]interface{}{
			"month":              monthStart.Format("2006-01"),
			"completed_deposits": currentTotal.StringFixed(2),
			"deposit_count":      currentCount,
		},
		"averages": map[string]interface{}{
			"average_all_prior_months":    avgMonthly.StringFixed(2),
			"average_active_prior_months": activeAvgMonthly.StringFixed(2),
			"active_prior_months":         activePriorMonths,
		},
		"estimate": map[string]interface{}{
			"estimated_month_end_income": estimated.StringFixed(2),
			"prorated_current_pace":      proratedEstimate.StringFixed(2),
			"confidence":                 confidence,
			"note":                       "Estimate from completed deposits only. It is a projection, not guaranteed income.",
		},
		"deposit_cadence": cadence,
		"data_used":       []string{"completed_deposits", "monthly_deposit_totals", "recent_deposit_dates"},
	}
	if lastDepositAt != nil {
		result["last_deposit_at"] = lastDepositAt.Format(time.RFC3339)
	}
	return result, nil
}

func (o *Orchestrator) executeYieldEarned(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.yieldProvider == nil {
		return map[string]interface{}{"error": "yield data not available"}, nil
	}
	now := time.Now().UTC()
	var from time.Time
	switch args["period"] {
	case "last_90_days":
		from = now.AddDate(0, 0, -90)
	case "last_30_days":
		from = now.AddDate(0, 0, -30)
	default:
		from = now.AddDate(0, 0, -7)
	}
	snapshots, err := o.yieldProvider.GetSnapshotsInWindow(ctx, userID, from, now)
	if err != nil {
		return nil, fmt.Errorf("yield data: %w", err)
	}
	if len(snapshots) < 2 {
		return map[string]interface{}{"estimated_yield": "0.00", "message": "Not enough data yet"}, nil
	}
	first := snapshots[0].Balance
	last := snapshots[len(snapshots)-1].Balance
	earned := last.Sub(first)
	if earned.LessThan(decimal.Zero) {
		earned = decimal.Zero
	}
	return map[string]interface{}{
		"estimated_yield": earned.StringFixed(2),
		"start_balance":   first.StringFixed(2),
		"current_balance": last.StringFixed(2),
		"period_days":     int(now.Sub(from).Hours() / 24),
		"note":            "This is an estimate based on balance change. It may include effects of deposits and withdrawals, not just yield.",
	}, nil
}

func (o *Orchestrator) executeWithdrawalHistory(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.withdrawalHistory == nil {
		return map[string]interface{}{"error": "withdrawal history not available"}, nil
	}
	limit := 5
	if l, ok := args["limit"].(float64); ok && l > 0 && l <= 10 {
		limit = int(l)
	}
	// Fetch more than needed so we can filter to completed only
	withdrawals, err := o.withdrawalHistory.GetByUserID(ctx, userID, limit*3, 0)
	if err != nil {
		return nil, fmt.Errorf("withdrawal history: %w", err)
	}
	items := make([]map[string]interface{}, 0, limit)
	for _, w := range withdrawals {
		if len(items) >= limit {
			break
		}
		// Only show completed withdrawals
		if w.Status != entities.WithdrawalStatusCompleted {
			continue
		}
		item := map[string]interface{}{
			"direction":      "money_out",
			"amount":         w.Amount.String(),
			"currency":       string(w.Currency),
			"type":           string(w.WithdrawalType),
			"source_account": string(w.SourceAccount),
			"status":         "completed",
			"date":           w.CreatedAt.Format("Jan 2, 2006"),
		}
		if w.DestinationAddress != nil {
			item["destination"] = *w.DestinationAddress
		}
		if w.FeeAmount.IsPositive() {
			item["fee"] = w.FeeAmount.String()
		}
		items = append(items, item)
	}
	return map[string]interface{}{"withdrawals": items, "count": len(items), "note": "Only showing completed withdrawals"}, nil
}

func inferDepositCadence(deposits []*entities.Deposit) (map[string]interface{}, int) {
	dates := make([]time.Time, 0, len(deposits))
	for _, d := range deposits {
		if d == nil {
			continue
		}
		if d.Status != "confirmed" && d.Status != "off_ramp_completed" && d.Status != "broker_funded" {
			continue
		}
		dates = append(dates, d.CreatedAt.UTC())
	}
	if len(dates) < 2 {
		return map[string]interface{}{
			"frequency":  "unknown",
			"confidence": "low",
			"reason":     "Not enough completed deposits to infer cadence.",
		}, 0
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })

	var totalGap float64
	gaps := make([]float64, 0, len(dates)-1)
	for i := 1; i < len(dates); i++ {
		gap := dates[i].Sub(dates[i-1]).Hours() / 24
		if gap < 1 {
			continue
		}
		gaps = append(gaps, gap)
		totalGap += gap
	}
	if len(gaps) == 0 {
		return map[string]interface{}{
			"frequency":     "irregular",
			"confidence":    "low",
			"deposit_count": len(dates),
		}, 0
	}
	avgGap := totalGap / float64(len(gaps))
	frequency := "irregular"
	switch {
	case avgGap >= 5 && avgGap <= 9:
		frequency = "weekly"
	case avgGap >= 10 && avgGap <= 18:
		frequency = "biweekly"
	case avgGap >= 24 && avgGap <= 38:
		frequency = "monthly"
	}

	confidence := "low"
	score := 25
	if len(gaps) >= 4 {
		score = 70
		confidence = "medium"
	}
	if len(gaps) >= 6 && frequency != "irregular" {
		score = 85
		confidence = "high"
	}

	nextExpected := dates[len(dates)-1].Add(time.Duration(avgGap*24) * time.Hour)
	return map[string]interface{}{
		"frequency":        frequency,
		"average_gap_days": fmt.Sprintf("%.1f", avgGap),
		"confidence":       confidence,
		"deposit_count":    len(dates),
		"last_deposit_at":  dates[len(dates)-1].Format(time.RFC3339),
		"next_expected_at": nextExpected.Format(time.RFC3339),
	}, score
}

func incomeEstimateConfidence(priorMonths, activePriorMonths, currentCount, cadenceScore int) string {
	switch {
	case priorMonths >= 5 && activePriorMonths >= 4 && cadenceScore >= 70:
		return "high"
	case priorMonths >= 3 && activePriorMonths >= 2 && (currentCount > 0 || cadenceScore >= 70):
		return "medium"
	default:
		return "low"
	}
}

func (o *Orchestrator) executeReceiptHistory(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.receiptHistory == nil {
		return map[string]interface{}{"error": "receipt history not available"}, nil
	}

	period, _ := args["period"].(string)
	start, end := parsePeriod(period)

	receipts, err := o.receiptHistory.GetByUserIDInRange(ctx, userID, start, end, 15)
	if err != nil {
		return nil, fmt.Errorf("receipt history: %w", err)
	}

	items := make([]map[string]interface{}, len(receipts))
	for i, r := range receipts {
		item := map[string]interface{}{
			"merchant": r.Merchant,
			"amount":   r.Amount.String(),
			"currency": r.Currency,
			"category": r.Category,
			"date":     r.CreatedAt.Format("Jan 2, 2006"),
		}
		if r.ReceiptDate != nil {
			item["receipt_date"] = r.ReceiptDate.Format("Jan 2, 2006")
		}
		parsed := r.ParsedItems()
		if len(parsed) > 0 {
			item["items"] = parsed
		}
		items[i] = item
	}

	// Category totals
	catTotals, _ := o.receiptHistory.GetTotalByCategory(ctx, userID, start, end)
	cats := make([]map[string]interface{}, len(catTotals))
	for i, c := range catTotals {
		cats[i] = map[string]interface{}{"category": humanizeCategory(c.Category), "total": c.Total.String(), "count": c.Count}
	}

	return map[string]interface{}{
		"receipts":        items,
		"count":           len(items),
		"category_totals": cats,
		"note":            "These are receipts the user has scanned/uploaded. They represent offline/cash spending not captured by card transactions.",
	}, nil
}
