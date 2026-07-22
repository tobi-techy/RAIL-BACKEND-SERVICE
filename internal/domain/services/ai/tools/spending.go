package tools

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
	"github.com/shopspring/decimal"
)

func RegisterSpendingTools(r *Registry) {
	r.Register(NewTool(
		"get_spending_summary",
		"Get current period's spending summary with category breakdown and comparison to last month",
		SimpleArgs(map[string]map[string]interface{}{
			"period": {"type": "string", "description": "Period: this_month, last_month, this_week, last_week", "enum": []string{"this_month", "last_month", "this_week", "last_week"}},
		}, nil),
		core.CategorySpending,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Spending == nil {
				return &core.ToolResult{Error: "spending service not available"}, nil
			}
			period := "this_month"
			if p, _ := args["period"].(string); p != "" {
				period = p
			}
			result, err := deps.Spending.GetSpendingSummary(ctx, userID, period)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: result}, nil
		},
	))

	r.Register(NewTool(
		"get_recent_transactions",
		"Get the user's most recent transactions with merchant names and amounts",
		SimpleArgs(map[string]map[string]interface{}{
			"limit": {"type": "integer", "description": "Number of transactions (default 10)"},
		}, nil),
		core.CategorySpending,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Transactions == nil {
				return &core.ToolResult{Error: "transactions service not available"}, nil
			}
			limit := 10
			if l, _ := args["limit"].(float64); l > 0 {
				limit = int(l)
			}
			txns, err := deps.Spending.GetRecentTransactions(ctx, userID, limit)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"transactions": txns}}, nil
		},
	))

	r.Register(NewTool(
		"get_money_flow",
		"Show income vs spending over recent months — inflows and outflows",
		SimpleArgs(map[string]map[string]interface{}{
			"months": {"type": "integer", "description": "Number of months (default 3)"},
		}, nil),
		core.CategorySpending,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Spending == nil {
				return &core.ToolResult{Error: "spending service not available"}, nil
			}
			months := 3
			if m, _ := args["months"].(float64); m > 0 {
				months = int(m)
			}
			flow, err := deps.Spending.GetMoneyFlow(ctx, userID, months)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: flow}, nil
		},
	))

	r.Register(NewTool(
		"get_spending_patterns",
		"Identify recurring patterns, anomalies, and unusual spending behavior",
		SimpleArgs(nil, nil),
		core.CategorySpending,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Spending == nil {
				return &core.ToolResult{Error: "spending service not available"}, nil
			}
			patterns, err := deps.Spending.GetSpendingPatterns(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: patterns}, nil
		},
	))

}

func RegisterBudgetTools(r *Registry) {
	r.Register(NewTool(
		"get_budget",
		"Get the user's current budget with category assignments and spending vs budget",
		SimpleArgs(nil, nil),
		core.CategoryBudget,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Budget == nil {
				return &core.ToolResult{Error: "budget service not available"}, nil
			}
			budget, err := deps.Budget.GetByUserID(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: budget}, nil
		},
	))

	r.Register(NewTool(
		"set_budget",
		"Set or update a budget category with an amount",
		SimpleArgs(map[string]map[string]interface{}{
			"category": {"type": "string", "description": "Budget category name (e.g. Food, Transport, Shopping)"},
			"amount":   {"type": "string", "description": "Budget amount (e.g. '50000' or '500.00')"},
		}, []string{"category", "amount"}),
		core.CategoryBudget,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Budget == nil {
				return &core.ToolResult{Error: "budget service not available"}, nil
			}
			category, _ := args["category"].(string)
			amountStr, _ := args["amount"].(string)
			if category == "" || amountStr == "" {
				return &core.ToolResult{Error: "category and amount are required"}, nil
			}
			amount, err := decimal.NewFromString(amountStr)
			if err != nil {
				return &core.ToolResult{Error: fmt.Sprintf("invalid amount: %s", amountStr)}, nil
			}
			err = deps.Budget.Upsert(ctx, userID, map[string]interface{}{
				"category": category,
				"amount":   amount,
			})
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"status": "ok", "category": category, "amount": amount.String()}}, nil
		},
	))
}

func RegisterTransactionTools(r *Registry) {
	r.Register(NewTool(
		"get_card_transactions",
		"Get card transactions with merchant names and amounts",
		SimpleArgs(map[string]map[string]interface{}{
			"limit": {"type": "integer", "description": "Number of transactions (default 20)"},
		}, nil),
		core.CategoryHistory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Transactions == nil {
				return &core.ToolResult{Error: "transactions service not available"}, nil
			}
			limit := 20
			if l, _ := args["limit"].(float64); l > 0 {
				limit = int(l)
			}
			txns, err := deps.Transactions.GetCardTransactions(ctx, userID, limit)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"transactions": txns}}, nil
		},
	))

	r.Register(NewTool(
		"get_deposit_history",
		"Get deposit/credit history",
		SimpleArgs(map[string]map[string]interface{}{
			"limit": {"type": "integer", "description": "Number of deposits (default 10)"},
		}, nil),
		core.CategoryHistory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Transactions == nil {
				return &core.ToolResult{Error: "transactions service not available"}, nil
			}
			limit := 10
			if l, _ := args["limit"].(float64); l > 0 {
				limit = int(l)
			}
			deposits, err := deps.Transactions.GetDepositHistory(ctx, userID, limit)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"deposits": deposits}}, nil
		},
	))

	r.Register(NewTool(
		"get_withdrawal_history",
		"Get withdrawal history",
		SimpleArgs(map[string]map[string]interface{}{
			"limit": {"type": "integer", "description": "Number of withdrawals (default 10)"},
		}, nil),
		core.CategoryHistory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Transactions == nil {
				return &core.ToolResult{Error: "transactions service not available"}, nil
			}
			limit := 10
			if l, _ := args["limit"].(float64); l > 0 {
				limit = int(l)
			}
			withdrawals, err := deps.Transactions.GetWithdrawalHistory(ctx, userID, limit)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"withdrawals": withdrawals}}, nil
		},
	))

	r.Register(NewTool(
		"get_income_trend",
		"Show income trend over recent months",
		SimpleArgs(map[string]map[string]interface{}{
			"months": {"type": "integer", "description": "Number of months (default 6)"},
		}, nil),
		core.CategoryHistory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Transactions == nil {
				return &core.ToolResult{Error: "transactions service not available"}, nil
			}
			months := 6
			if m, _ := args["months"].(float64); m > 0 {
				months = int(m)
			}
			trend, err := deps.Transactions.GetIncomeTrend(ctx, userID, months)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: trend}, nil
		},
	))

	r.Register(NewTool(
		"get_yield_earned",
		"Show yield/interest earned on savings",
		SimpleArgs(nil, nil),
		core.CategoryHistory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Transactions == nil {
				return &core.ToolResult{Error: "transactions service not available"}, nil
			}
			yield, err := deps.Transactions.GetYieldEarned(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: yield}, nil
		},
	))

	r.Register(NewTool(
		"get_balance_history",
		"Get balance history over a time period",
		SimpleArgs(map[string]map[string]interface{}{
			"days": {"type": "integer", "description": "Number of days to look back (default 30)"},
		}, nil),
		core.CategoryHistory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Transactions == nil {
				return &core.ToolResult{Error: "transactions service not available"}, nil
			}
			days := 30
			if d, _ := args["days"].(float64); d > 0 {
				days = int(d)
			}
			history, err := deps.Transactions.GetBalanceHistory(ctx, userID, days)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"history": history}}, nil
		},
	))

	r.Register(NewTool(
		"get_receipt_history",
		"Get receipt/image history",
		SimpleArgs(map[string]map[string]interface{}{
			"limit": {"type": "integer", "description": "Number of receipts (default 10)"},
		}, nil),
		core.CategoryHistory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Transactions == nil {
				return &core.ToolResult{Error: "transactions service not available"}, nil
			}
			limit := 10
			if l, _ := args["limit"].(float64); l > 0 {
				limit = int(l)
			}
			receipts, err := deps.Transactions.GetReceiptHistory(ctx, userID, limit)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"receipts": receipts}}, nil
		},
	))
}

func RegisterGoalTools(r *Registry) {
	r.Register(NewTool(
		"set_savings_goal",
		"Create a new savings goal with a name and target amount",
		SimpleArgs(map[string]map[string]interface{}{
			"name":   {"type": "string", "description": "Goal name (e.g. 'Emergency Fund')"},
			"target": {"type": "string", "description": "Target amount (e.g. '500000')"},
		}, []string{"name", "target"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Goals == nil {
				return &core.ToolResult{Error: "goals service not available"}, nil
			}
			name, _ := args["name"].(string)
			targetStr, _ := args["target"].(string)
			if name == "" || targetStr == "" {
				return &core.ToolResult{Error: "name and target are required"}, nil
			}
			target, err := decimal.NewFromString(targetStr)
			if err != nil {
				return &core.ToolResult{Error: fmt.Sprintf("invalid target: %s", targetStr)}, nil
			}
			err = deps.Goals.Create(ctx, userID, &core.Goal{
				ID:     uuid.New(),
				Name:   name,
				Target: target,
				Status: "active",
			})
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"status": "ok", "name": name, "target": target.String()}}, nil
		},
	))

	r.Register(NewTool(
		"get_goals",
		"Get all savings goals with progress",
		SimpleArgs(nil, nil),
		core.CategoryOverview,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Goals == nil {
				return &core.ToolResult{Error: "goals service not available"}, nil
			}
			goals, err := deps.Goals.GetByUserID(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"goals": goals}}, nil
		},
	))
}

func RegisterObligationTools(r *Registry) {
	r.Register(NewTool(
		"list_obligations",
		"List all financial obligations/reminders with due dates",
		SimpleArgs(nil, nil),
		core.CategoryOverview,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Obligations == nil {
				return &core.ToolResult{Error: "obligations service not available"}, nil
			}
			obligations, err := deps.Obligations.List(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"obligations": obligations}}, nil
		},
	))

	r.Register(NewTool(
		"create_obligation_reminder",
		"Create a new obligation reminder (bill due date, etc.)",
		SimpleArgs(map[string]map[string]interface{}{
			"name":      {"type": "string", "description": "Obligation name"},
			"amount":    {"type": "string", "description": "Amount due"},
			"due_date":  {"type": "string", "description": "Due date (YYYY-MM-DD)"},
			"frequency": {"type": "string", "description": "Frequency: one_time, weekly, monthly, yearly"},
		}, []string{"name"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Obligations == nil {
				return &core.ToolResult{Error: "obligations service not available"}, nil
			}
			err := deps.Obligations.Create(ctx, userID, args)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"status": "ok"}}, nil
		},
	))

	r.Register(NewTool(
		"mark_obligation_paid",
		"Mark an obligation as paid/finished",
		SimpleArgs(map[string]map[string]interface{}{
			"match_id": {"type": "string", "description": "The obligation match ID"},
		}, []string{"match_id"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Obligations == nil {
				return &core.ToolResult{Error: "obligations service not available"}, nil
			}
			matchIDStr, _ := args["match_id"].(string)
			if matchIDStr == "" {
				return &core.ToolResult{Error: "match_id is required"}, nil
			}
			matchID, err := uuid.Parse(matchIDStr)
			if err != nil {
				return &core.ToolResult{Error: fmt.Sprintf("invalid match_id: %s", matchIDStr)}, nil
			}
			err = deps.Obligations.MarkPaid(ctx, userID, matchID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"status": "ok"}}, nil
		},
	))

	r.Register(NewTool(
		"find_payment_matches",
		"Find likely bill payments from recent transactions",
		SimpleArgs(nil, nil),
		core.CategoryOverview,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Obligations == nil {
				return &core.ToolResult{Error: "obligations service not available"}, nil
			}
			matches, err := deps.Obligations.FindPaymentMatches(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"matches": matches}}, nil
		},
	))
}

func RegisterAutomationTools(r *Registry) {
	r.Register(NewTool(
		"list_automations",
		"List all automations and scheduled transfers",
		SimpleArgs(nil, nil),
		core.CategoryAutomation,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Automation == nil {
				return &core.ToolResult{Error: "automation service not available"}, nil
			}
			automations, err := deps.Automation.List(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"automations": automations}}, nil
		},
	))

	r.Register(NewTool(
		"create_automation",
		"Create a new automation or scheduled transfer",
		SimpleArgs(map[string]map[string]interface{}{
			"name":     {"type": "string", "description": "Automation name"},
			"type":     {"type": "string", "description": "Type: transfer, budget, bill"},
			"schedule": {"type": "string", "description": "Cron or frequency description"},
			"amount":   {"type": "string", "description": "Amount for transfers"},
			"from":     {"type": "string", "description": "Source account"},
			"to":       {"type": "string", "description": "Destination account"},
		}, []string{"name", "type"}),
		core.CategoryAutomation,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Automation == nil {
				return &core.ToolResult{Error: "automation service not available"}, nil
			}
			err := deps.Automation.Create(ctx, userID, args)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"status": "ok"}}, nil
		},
	))
}

func RegisterSubscriptionTools(r *Registry) {
	r.Register(NewTool(
		"list_subscriptions",
		"List all detected subscriptions and recurring payments",
		SimpleArgs(nil, nil),
		core.CategoryOverview,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Subscription == nil {
				return &core.ToolResult{Error: "subscription service not available"}, nil
			}
			subs, err := deps.Subscription.ListSubscriptions(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"subscriptions": subs}}, nil
		},
	))

	r.Register(NewTool(
		"protect_subscription",
		"Flag a subscription to prevent accidental cancellation",
		SimpleArgs(map[string]map[string]interface{}{
			"subscription_id": {"type": "string", "description": "ID of the subscription to protect"},
		}, []string{"subscription_id"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Subscription == nil {
				return &core.ToolResult{Error: "subscription service not available"}, nil
			}
			subIDStr, _ := args["subscription_id"].(string)
			if subIDStr == "" {
				return &core.ToolResult{Error: "subscription_id is required"}, nil
			}
			subID, err := uuid.Parse(subIDStr)
			if err != nil {
				return &core.ToolResult{Error: fmt.Sprintf("invalid subscription_id: %s", subIDStr)}, nil
			}
			err = deps.Subscription.ProtectSubscription(ctx, userID, subID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"status": "ok"}}, nil
		},
	))
}

func RegisterProfileTools(r *Registry) {
	r.Register(NewTool(
		"get_financial_profile",
		"Get the user's durable financial profile including risk tolerance and preferences",
		SimpleArgs(nil, nil),
		core.CategoryPlanning,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Profile == nil {
				return &core.ToolResult{Error: "profile service not available"}, nil
			}
			profile, err := deps.Profile.GetFinancialProfile(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: profile}, nil
		},
	))
}

func RegisterRecurringTools(r *Registry) {
	r.Register(NewTool(
		"get_recurring_expenses",
		"Find recurring expenses and subscriptions from transaction history",
		SimpleArgs(nil, nil),
		core.CategorySpending,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.RecurringExpense == nil {
				return &core.ToolResult{Error: "recurring expense service not available"}, nil
			}
			expenses, err := deps.RecurringExpense.GetRecurring(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"expenses": expenses}}, nil
		},
	))
}

func RegisterWithdrawalTools(r *Registry) {
	r.Register(NewTool(
		"initiate_withdrawal",
		"Start a withdrawal from the user's balance to their bank account",
		SimpleArgs(map[string]map[string]interface{}{
			"amount":          {"type": "string", "description": "Amount to withdraw"},
			"bank_account_id": {"type": "string", "description": "Target bank account ID (optional if only one)"},
		}, []string{"amount"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.WithdrawalInitiator == nil {
				return &core.ToolResult{Error: "withdrawal service not available"}, nil
			}
			amountStr, _ := args["amount"].(string)
			bankIDStr, _ := args["bank_account_id"].(string)
			if amountStr == "" {
				return &core.ToolResult{Error: "amount is required"}, nil
			}
			amount, err := decimal.NewFromString(amountStr)
			if err != nil {
				return &core.ToolResult{Error: fmt.Sprintf("invalid amount: %s", amountStr)}, nil
			}
			bankID := uuid.Nil
			if bankIDStr != "" {
				bankID, _ = uuid.Parse(bankIDStr)
			}
			err = deps.WithdrawalInitiator.InitiateWithdrawal(ctx, userID, amount, bankID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"status": "initiated", "amount": amount.String()}}, nil
		},
	))

	r.Register(NewTool(
		"transfer_funds",
		"Move money between the user's Spend and Stash balances (e.g. 'move $200 into my stash'). Moves real money — it is staged for user confirmation, never executed inline.",
		SimpleArgs(map[string]map[string]interface{}{
			"from":   {"type": "string", "description": "Source balance: 'spend' or 'stash'"},
			"to":     {"type": "string", "description": "Destination balance: 'spend' or 'stash'"},
			"amount": {"type": "string", "description": "Amount to move, e.g. '200.00'"},
		}, []string{"to", "amount"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.FundsTransfer == nil {
				return &core.ToolResult{Error: "transfer service not available"}, nil
			}
			from := GetArgString(args, "from")
			if from == "" {
				from = "spend"
			}
			to := GetArgString(args, "to")
			amountStr := GetArgString(args, "amount")
			if to == "" || amountStr == "" {
				return &core.ToolResult{Error: "to and amount are required"}, nil
			}
			amount, err := decimal.NewFromString(amountStr)
			if err != nil {
				return &core.ToolResult{Error: fmt.Sprintf("invalid amount: %s", amountStr)}, nil
			}
			if err := deps.FundsTransfer.Transfer(ctx, userID, from, to, amount); err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"status": "transferred", "from": from, "to": to, "amount": amount.String()}}, nil
		},
	))

	r.Register(NewTool(
		"get_linked_banks",
		"Get linked bank accounts for withdrawal",
		SimpleArgs(nil, nil),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.WithdrawalInitiator == nil {
				return &core.ToolResult{Error: "withdrawal service not available"}, nil
			}
			banks, err := deps.WithdrawalInitiator.GetLinkedBanks(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"banks": banks}}, nil
		},
	))
}

func RegisterKnowledgeTool(r *Registry) {
	r.Register(NewTool(
		"search_knowledge",
		"Search financial knowledge base for tips, guides, and explanations",
		SimpleArgs(map[string]map[string]interface{}{
			"query": {"type": "string", "description": "What to search for"},
		}, []string{"query"}),
		core.CategoryKnowledge,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Knowledge == nil {
				return &core.ToolResult{Error: "knowledge service not available"}, nil
			}
			query, _ := args["query"].(string)
			if query == "" {
				return &core.ToolResult{Error: "query is required"}, nil
			}
			results, err := deps.Knowledge.Search(ctx, userID, query)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: results}, nil
		},
	))
}

func RegisterSavingsSuggestionTool(r *Registry) {
	r.Register(NewTool(
		"get_savings_suggestions",
		"Get personalized suggestions for saving more money",
		SimpleArgs(nil, nil),
		core.CategoryPlanning,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.SavingsSuggestion == nil {
				return &core.ToolResult{Error: "savings suggestion service not available"}, nil
			}
			suggestions, err := deps.SavingsSuggestion.GetSuggestions(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"suggestions": suggestions}}, nil
		},
	))
}

func RegisterAllSpendingAndTransactionTools(r *Registry) {
	RegisterSpendingTools(r)
	RegisterBudgetTools(r)
	RegisterTransactionTools(r)
	RegisterGoalTools(r)
	RegisterObligationTools(r)
	RegisterAutomationTools(r)
	RegisterSubscriptionTools(r)
	RegisterProfileTools(r)
	RegisterRecurringTools(r)
	RegisterWithdrawalTools(r)
	RegisterKnowledgeTool(r)
	RegisterSavingsSuggestionTool(r)
}
