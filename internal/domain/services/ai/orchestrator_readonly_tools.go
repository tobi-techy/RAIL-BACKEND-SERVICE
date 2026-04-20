package ai

import (
	"context"
	"fmt"
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
func (o *Orchestrator) SetCardTransactions(p CardTransactionProvider) {
	o.cardTransactions = p
}

// SetDepositHistory sets the deposit history provider.
func (o *Orchestrator) SetDepositHistory(p DepositHistoryProvider) {
	o.depositHistory = p
}

// SetYieldProvider sets the yield provider.
func (o *Orchestrator) SetYieldProvider(p YieldProvider) {
	o.yieldProvider = p
}

// SetWithdrawalHistory sets the withdrawal history provider.
func (o *Orchestrator) SetWithdrawalHistory(p WithdrawalHistoryProvider) {
	o.withdrawalHistory = p
}

// SetReceiptHistory sets the receipt history provider.
func (o *Orchestrator) SetReceiptHistory(p ReceiptHistoryProvider) {
	o.receiptHistory = p
}

// ReadOnlyTools returns tool definitions for read-only data access.
func ReadOnlyTools(hasCards, hasDeposits, hasYield, hasWithdrawals, hasReceipts bool) []infraai.Tool {
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
	if hasYield {
		tools = append(tools, infraai.Tool{
			Name:        ToolGetYieldEarned,
			Description: "Get yield earned on stash (USDB). Use when user asks about interest, yield, earnings on savings, or how much their stash has earned.",
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
			Description: "Get recent withdrawal history including Paj Cash NGN withdrawals, crypto withdrawals, and fiat offramps. Use when user asks where their money went, about withdrawals, cash outs, NGN conversions, or money leaving their account.",
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
		return map[string]interface{}{"yield_earned": "0.00", "message": "Not enough data yet"}, nil
	}
	first := snapshots[0].Balance
	last := snapshots[len(snapshots)-1].Balance
	earned := last.Sub(first)
	if earned.LessThan(decimal.Zero) {
		earned = decimal.Zero
	}
	return map[string]interface{}{
		"yield_earned":    earned.StringFixed(2),
		"start_balance":   first.StringFixed(2),
		"current_balance": last.StringFixed(2),
		"period_days":     int(now.Sub(from).Hours() / 24),
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
		cats[i] = map[string]interface{}{"category": c.Category, "total": c.Total.String(), "count": c.Count}
	}

	return map[string]interface{}{
		"receipts":         items,
		"count":            len(items),
		"category_totals":  cats,
		"note":             "These are receipts the user has scanned/uploaded. They represent offline/cash spending not captured by card transactions.",
	}, nil
}
