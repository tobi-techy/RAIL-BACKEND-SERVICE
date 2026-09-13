package investing

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	ledgersvc "github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
)

const (
	financialSnapshotMaxMonths = 24
	financialSnapshotDateFmt   = "2006-01-02"
)

// MoneyFlowReader computes money-in/money-out totals for a period (deposits,
// withdrawals, card spend, p2p, scanned receipts).
type MoneyFlowReader interface {
	GetMoneyFlow(ctx context.Context, userID uuid.UUID, start, end time.Time) (*entities.MoneyFlowSummary, error)
}

// AccountBalanceReader reads a user's current ledger account balance.
type AccountBalanceReader interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
}

// BudgetReader reads a user's monthly spending budget. (nil, nil) means unset.
type BudgetReader interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.SpendingBudget, error)
}

// FinancialProfileReader reads a user's durable financial profile. (nil, nil)
// means no profile yet.
type FinancialProfileReader interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.FinancialProfile, error)
}

// FinancialSnapshotHandler serves the ledger-backed facts the financial
// intelligence engine (the delegated Python agent) needs to compute health,
// cash-flow forecast, and plans: current balances, period money flow, a
// per-month series for trend analysis, the monthly budget, and the profile.
// It only reads; every mutation still goes through OTP-approval paths.
type FinancialSnapshotHandler struct {
	flow     MoneyFlowReader
	balances AccountBalanceReader
	budget   BudgetReader
	profile  FinancialProfileReader
	logger   *logger.Logger
}

// NewFinancialSnapshotHandler builds the handler with the ledger-facing
// providers. Any provider may be nil; the handler degrades (zeros / unset)
// instead of failing the whole snapshot.
func NewFinancialSnapshotHandler(
	flow MoneyFlowReader,
	balances AccountBalanceReader,
	budget BudgetReader,
	profile FinancialProfileReader,
	log *logger.Logger,
) *FinancialSnapshotHandler {
	return &FinancialSnapshotHandler{
		flow:     flow,
		balances: balances,
		budget:   budget,
		profile:  profile,
		logger:   log,
	}
}

// GetFinancialSnapshot handles GET /api/v1/analytics/financial-snapshot.
// Query params from/to (YYYY-MM-DD, both optional) bound the money-flow window;
// defaults are the current calendar month. from is clamped to at most
// financialSnapshotMaxMonths before to.
func (h *FinancialSnapshotHandler) GetFinancialSnapshot(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.RespondUnauthorized(c, "User not authenticated")
		return
	}

	to := time.Now().UTC()
	from := time.Date(to.Year(), to.Month(), 1, 0, 0, 0, 0, time.UTC)
	if q := c.Query("to"); q != "" {
		if t, parseErr := time.ParseInLocation(financialSnapshotDateFmt, q, time.UTC); parseErr == nil {
			to = t
			from = time.Date(to.Year(), to.Month(), 1, 0, 0, 0, 0, time.UTC)
		}
	}
	if q := c.Query("from"); q != "" {
		if t, parseErr := time.ParseInLocation(financialSnapshotDateFmt, q, time.UTC); parseErr == nil {
			from = t
		}
	}
	// Clamp the requested window so one request can't fan out into unbounded
	// per-month queries.
	if from.Before(to.AddDate(0, 0, -financialSnapshotMaxMonths*31)) {
		from = to.AddDate(0, 0, -financialSnapshotMaxMonths*31)
	}
	// End is exclusive for the query; use start of the day after `to`.
	end := to.AddDate(0, 0, 1)

	resp := map[string]interface{}{
		"period": map[string]interface{}{
			"from": time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC).Format(financialSnapshotDateFmt),
			"to":   time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC).Format(financialSnapshotDateFmt),
		},
		"balances":     h.snapshotBalances(c.Request.Context(), userID),
		"budget":       h.snapshotBudget(c.Request.Context(), userID),
		"profile":      h.snapshotProfile(c.Request.Context(), userID),
		"money_flow":   h.snapshotMoneyFlow(c.Request.Context(), userID, from, end),
		"monthly_flow": h.snapshotMonthlyFlow(c.Request.Context(), userID, from, end),
	}

	c.JSON(http.StatusOK, resp)
}

func (h *FinancialSnapshotHandler) snapshotMoneyFlow(ctx context.Context, userID uuid.UUID, from, end time.Time) map[string]interface{} {
	if h.flow == nil {
		return map[string]interface{}{"error": "money flow is unavailable"}
	}
	flow, err := h.flow.GetMoneyFlow(ctx, userID, from, end)
	if err != nil {
		h.logger.Error("financial snapshot: money flow failed", "error", err, "user_id", userID.String())
		return map[string]interface{}{"error": "money flow is unavailable"}
	}
	if flow == nil {
		flow = &entities.MoneyFlowSummary{}
	}
	return map[string]interface{}{
		"total_deposits":    flow.TotalDeposits.StringFixed(2),
		"deposit_count":     flow.DepositCount,
		"total_withdrawals": flow.TotalWithdrawals.StringFixed(2),
		"withdrawal_count":  flow.WithdrawalCount,
		"total_card_spend":  flow.TotalCardSpend.StringFixed(2),
		"card_spend_count":  flow.CardSpendCount,
		"total_p2p":         flow.TotalP2P.StringFixed(2),
		"p2p_count":         flow.P2PCount,
		"total_receipts":    flow.TotalReceipts.StringFixed(2),
		"receipt_count":     flow.ReceiptCount,
	}
}

func (h *FinancialSnapshotHandler) snapshotMonthlyFlow(ctx context.Context, userID uuid.UUID, from, end time.Time) []map[string]interface{} {
	if h.flow == nil {
		return nil
	}
	months := []map[string]interface{}{}
	cursor := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	seen := 0
	for cursor.Before(end) && seen < financialSnapshotMaxMonths {
		monthEnd := cursor.AddDate(0, 1, 0)
		flow, err := h.flow.GetMoneyFlow(ctx, userID, cursor, monthEnd)
		if err != nil {
			h.logger.Error("financial snapshot: monthly flow failed",
				"error", err, "user_id", userID.String(), "month", cursor.Format("2006-01"))
			flow = nil
		}
		if flow == nil {
			flow = &entities.MoneyFlowSummary{}
		}
		outflow := flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P).Add(flow.TotalReceipts)
		months = append(months, map[string]interface{}{
			"month":           cursor.Format("2006-01"),
			"total_deposits":  flow.TotalDeposits.StringFixed(2),
			"deposit_count":   flow.DepositCount,
			"total_outflow":   outflow.StringFixed(2),
			"outflow_count":   flow.WithdrawalCount + flow.CardSpendCount + flow.P2PCount + flow.ReceiptCount,
			"total_card_spend": flow.TotalCardSpend.StringFixed(2),
			"card_spend_count": flow.CardSpendCount,
		})
		cursor = monthEnd
		seen++
	}
	return months
}

func (h *FinancialSnapshotHandler) snapshotBalances(ctx context.Context, userID uuid.UUID) map[string]interface{} {
	out := map[string]interface{}{
		"spending_balance": "0.00",
		"stash_balance":    "0.00",
		"total_balance":    "0.00",
	}
	if h.balances == nil {
		return out
	}
	spend, errS := h.balances.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if errS != nil && !errors.Is(errS, ledgersvc.ErrAccountNotFound) {
		h.logger.Error("financial snapshot: spending balance failed", "error", errS, "user_id", userID.String())
	}
	stash, errI := h.balances.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	if errI != nil && !errors.Is(errI, ledgersvc.ErrAccountNotFound) {
		h.logger.Error("financial snapshot: stash balance failed", "error", errI, "user_id", userID.String())
	}
	total := spend.Add(stash)
	out["spending_balance"] = spend.StringFixed(2)
	out["stash_balance"] = stash.StringFixed(2)
	out["total_balance"] = total.StringFixed(2)
	return out
}

func (h *FinancialSnapshotHandler) snapshotBudget(ctx context.Context, userID uuid.UUID) map[string]interface{} {
	if h.budget == nil {
		return map[string]interface{}{"set": false}
	}
	budget, err := h.budget.GetByUserID(ctx, userID)
	if err != nil {
		h.logger.Error("financial snapshot: budget failed", "error", err, "user_id", userID.String())
		return map[string]interface{}{"set": false}
	}
	if budget == nil {
		return map[string]interface{}{"set": false}
	}
	return map[string]interface{}{
		"set":          true,
		"monthly_limit": budget.MonthlyLimit.StringFixed(2),
		"currency":     budget.Currency,
	}
}

func (h *FinancialSnapshotHandler) snapshotProfile(ctx context.Context, userID uuid.UUID) map[string]interface{} {
	if h.profile == nil {
		return map[string]interface{}{"has_profile": false}
	}
	profile, err := h.profile.GetByUserID(ctx, userID)
	if err != nil {
		h.logger.Error("financial snapshot: profile failed", "error", err, "user_id", userID.String())
		return map[string]interface{}{"has_profile": false}
	}
	if profile == nil {
		return map[string]interface{}{"has_profile": false}
	}
	return map[string]interface{}{
		"has_profile":           true,
		"primary_currency":      profile.PrimaryCurrency,
		"income_frequency":      profile.IncomeFrequency,
		"monthly_income":        profile.MonthlyIncome.StringFixed(2),
		"monthly_fixed_costs":   profile.MonthlyFixedCosts.StringFixed(2),
		"monthly_savings_target": profile.MonthlySavingsTarget.StringFixed(2),
		"emergency_fund_target": profile.EmergencyFundTarget.StringFixed(2),
		"risk_tolerance":        profile.RiskTolerance,
		"investment_horizon":    profile.InvestmentHorizon,
		"financial_goal":        profile.FinancialGoal,
	}
}