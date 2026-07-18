package tools

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
	"github.com/shopspring/decimal"
)

// Execution Engine tools (spec 5.2). Mutating tools follow a two-step
// confirm-argument contract: called without confirm=true they return an
// action_required payload describing exactly what would happen, so Miriam
// asks the user first; called with confirm=true they execute. The streaming
// chat path additionally intercepts these through the pending-action flow.

// needsConfirm returns a standard "ask the user first" payload unless the
// model passed confirm=true after an explicit user yes.
func needsConfirm(args map[string]interface{}, summary string) *core.ToolResult {
	if ok, _ := args["confirm"].(bool); ok {
		return nil
	}
	return &core.ToolResult{Data: map[string]interface{}{
		"action_required": "confirmation",
		"summary":         summary,
		"instruction":     "Do NOT execute yet. Restate plainly: the amount, what will happen, and any fees. Use the human-readable name (basket, conductor, bill), never the raw ID. Ask the user to confirm. Only after an explicit yes, call this tool again with confirm=true.",
	}}
}

// maxExecutionAmount caps single AI-initiated investment/copy-trading orders,
// mirroring the $500 chat-transfer cap's intent: bound the blast radius of a
// conversational confirmation. Larger orders go through the app.
const maxExecutionAmount = 1000.0

// checkAmountCap validates a positive amount within the AI execution cap.
func checkAmountCap(amount string) *core.ToolResult {
	amt, err := decimal.NewFromString(amount)
	if err != nil || !amt.IsPositive() {
		return &core.ToolResult{Error: fmt.Sprintf("invalid amount: %s", amount)}
	}
	if amt.GreaterThan(decimal.NewFromFloat(maxExecutionAmount)) {
		return &core.ToolResult{Error: fmt.Sprintf("For safety, orders through chat are capped at $%.0f. Larger orders need to go through the app.", maxExecutionAmount)}
	}
	return nil
}

func confirmParam() map[string]interface{} {
	return map[string]interface{}{
		"type":        "boolean",
		"description": "Set true ONLY after the user has explicitly confirmed this exact action in this conversation.",
	}
}

// RegisterBillPayTools registers upcoming-bill lookup and auto-pay setup.
func RegisterBillPayTools(r *Registry) {
	r.Register(NewTool(
		"get_upcoming_bills",
		"List the user's upcoming bills and obligations with amounts and due dates. Call when the user asks about bills due, rent, upcoming payments, or whether they can cover their bills.",
		SimpleArgs(nil, nil),
		core.CategoryPlanning,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.BillPay == nil {
				return &core.ToolResult{Error: "bill pay service not available"}, nil
			}
			bills, err := deps.BillPay.GetUpcomingBills(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"bills": bills, "count": len(bills)}}, nil
		},
	))

	r.Register(NewTool(
		"setup_bill_autopay",
		"Set up auto-pay for a bill. With a payee (their Rail tag, email, or phone — ask the user who receives the payment, e.g. their landlord), the bill amount is actually SENT to that payee on the due day and the user gets a payment receipt. Without a payee, the amount is only moved from Stash to Spend before the due date so it's covered. Requires an obligation id from get_upcoming_bills.",
		SimpleArgs(map[string]map[string]interface{}{
			"obligation_id": {"type": "string", "description": "ID of the obligation/bill from get_upcoming_bills"},
			"payee":         {"type": "string", "description": "Who receives the payment: Rail tag, email, or phone. Omit to only set funds aside."},
			"payee_name":    {"type": "string", "description": "Payee's display name for receipts, e.g. 'Mr Adewale (landlord)'"},
			"confirm":       confirmParam(),
		}, []string{"obligation_id"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.BillPay == nil {
				return &core.ToolResult{Error: "bill pay service not available"}, nil
			}
			obligationID := GetArgString(args, "obligation_id")
			if obligationID == "" {
				return &core.ToolResult{Error: "obligation_id is required"}, nil
			}
			payee := GetArgString(args, "payee")
			payeeName := GetArgString(args, "payee_name")
			summary := "Set up auto-pay: the bill amount is moved from Stash to Spend 2 days before each due date, with a notification."
			if payee != "" {
				label := payeeName
				if label == "" {
					label = payee
				}
				summary = fmt.Sprintf("Set up auto-pay: on each due date the bill amount is SENT to %s, and you get a payment receipt.", label)
			}
			if res := needsConfirm(args, summary); res != nil {
				return res, nil
			}
			out, err := deps.BillPay.SetupAutoPay(ctx, userID, obligationID, payee, payeeName)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: out}, nil
		},
	))
}

// RegisterSubscriptionAuditTools registers subscription audit + cancellation.
func RegisterSubscriptionAuditTools(r *Registry) {
	r.Register(NewTool(
		"audit_subscriptions",
		"Audit all detected recurring subscriptions: monthly cost, possibly-unused ones (no recent charge), and total potential savings. Call when the user asks what subscriptions they have, what they can cut, or where money leaks.",
		SimpleArgs(nil, nil),
		core.CategorySpending,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.SubscriptionAudit == nil {
				return &core.ToolResult{Error: "subscription audit service not available"}, nil
			}
			audit, err := deps.SubscriptionAudit.AuditSubscriptions(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: audit}, nil
		},
	))

	r.Register(NewTool(
		"cancel_subscription",
		"Cancel a subscription: marks it cancelled in Rail and (optionally) blocks the merchant so future charges on the Rail card decline. Rail cannot cancel with the merchant directly — tell the user to also cancel in the merchant's app, and offer the merchant block as enforcement.",
		SimpleArgs(map[string]map[string]interface{}{
			"name":           {"type": "string", "description": "Subscription/merchant name, e.g. 'Netflix'"},
			"block_merchant": {"type": "boolean", "description": "Also block the merchant on the Rail card so renewal charges decline"},
			"confirm":        confirmParam(),
		}, []string{"name"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.SubscriptionAudit == nil {
				return &core.ToolResult{Error: "subscription audit service not available"}, nil
			}
			name := GetArgString(args, "name")
			if name == "" {
				return &core.ToolResult{Error: "name is required"}, nil
			}
			block, _ := args["block_merchant"].(bool)
			summary := fmt.Sprintf("Cancel subscription %q in Rail", name)
			if block {
				summary += " and block the merchant so future card charges decline"
			}
			if res := needsConfirm(args, summary+"."); res != nil {
				return res, nil
			}
			out, err := deps.SubscriptionAudit.CancelSubscription(ctx, userID, name, block)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: out}, nil
		},
	))
}

// RegisterInvestmentExecutionTools registers basket listing + order execution.
func RegisterInvestmentExecutionTools(r *Registry) {
	r.Register(NewTool(
		"get_investment_options",
		"List the investment baskets available to buy or sell. Call before execute_investment when the user wants to invest and hasn't named a basket.",
		SimpleArgs(nil, nil),
		core.CategoryInvestment,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.InvestmentExec == nil {
				return &core.ToolResult{Error: "investment execution service not available"}, nil
			}
			options, err := deps.InvestmentExec.ListInvestmentOptions(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"baskets": options}}, nil
		},
	))

	r.Register(NewTool(
		"execute_investment",
		"Place a buy or sell order for an investment basket with real money. HIGH STAKES: always restate basket, side, and amount, and get an explicit yes before confirming.",
		SimpleArgs(map[string]map[string]interface{}{
			"basket_id": {"type": "string", "description": "Basket ID from get_investment_options"},
			"side":      EnumParam("Order side", []string{"buy", "sell"}),
			"amount":    {"type": "string", "description": "USD amount, e.g. '50.00'"},
			"confirm":   confirmParam(),
		}, []string{"basket_id", "side", "amount"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.InvestmentExec == nil {
				return &core.ToolResult{Error: "investment execution service not available"}, nil
			}
			basketID := GetArgString(args, "basket_id")
			side := GetArgString(args, "side")
			amount := GetArgString(args, "amount")
			if basketID == "" || amount == "" || (side != "buy" && side != "sell") {
				return &core.ToolResult{Error: "basket_id, side (buy/sell), and amount are required"}, nil
			}
			if res := checkAmountCap(amount); res != nil {
				return res, nil
			}
			if res := needsConfirm(args, fmt.Sprintf("Place a %s order of $%s on basket %s.", side, amount, basketID)); res != nil {
				return res, nil
			}
			out, err := deps.InvestmentExec.ExecuteInvestment(ctx, userID, basketID, side, amount)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: out}, nil
		},
	))
}

// RegisterYieldOptimizationTools registers yield status + idle-cash sweep.
func RegisterYieldOptimizationTools(r *Registry) {
	r.Register(NewTool(
		"get_yield_status",
		"Show how much of the user's money is earning yield (Stash) versus sitting idle (Spend), with a suggested amount to move. Call when the user asks about yield, interest, idle cash, or making their money work.",
		SimpleArgs(nil, nil),
		core.CategoryOverview,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.YieldOpt == nil {
				return &core.ToolResult{Error: "yield optimization service not available"}, nil
			}
			status, err := deps.YieldOpt.GetYieldStatus(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: status}, nil
		},
	))

	r.Register(NewTool(
		"optimize_yield",
		"Move idle money from Spend into the yield-bearing Stash so it earns. Moves real money — restate the amount and get an explicit yes first.",
		SimpleArgs(map[string]map[string]interface{}{
			"amount":  {"type": "string", "description": "USD amount to move from Spend to Stash, e.g. '100.00'"},
			"confirm": confirmParam(),
		}, []string{"amount"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.YieldOpt == nil {
				return &core.ToolResult{Error: "yield optimization service not available"}, nil
			}
			amount := GetArgString(args, "amount")
			if amount == "" {
				return &core.ToolResult{Error: "amount is required"}, nil
			}
			if res := needsConfirm(args, fmt.Sprintf("Move $%s from Spend into the yield-bearing Stash.", amount)); res != nil {
				return res, nil
			}
			out, err := deps.YieldOpt.OptimizeYield(ctx, userID, amount)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: out}, nil
		},
	))
}

// RegisterMerchantBlockTools registers card-level merchant blocking.
func RegisterMerchantBlockTools(r *Registry) {
	r.Register(NewTool(
		"block_merchant",
		"Block a merchant on the user's Rail card so future charges decline. Use for gambling, impulse triggers, or unwanted subscriptions. Always ask first.",
		SimpleArgs(map[string]map[string]interface{}{
			"merchant": {"type": "string", "description": "Merchant name to block, e.g. 'DraftKings'"},
			"confirm":  confirmParam(),
		}, []string{"merchant"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.MerchantBlock == nil {
				return &core.ToolResult{Error: "merchant blocking service not available"}, nil
			}
			merchant := GetArgString(args, "merchant")
			if merchant == "" {
				return &core.ToolResult{Error: "merchant is required"}, nil
			}
			if res := needsConfirm(args, fmt.Sprintf("Block %q on the Rail card — future charges from this merchant will be declined until unblocked.", merchant)); res != nil {
				return res, nil
			}
			out, err := deps.MerchantBlock.BlockMerchant(ctx, userID, merchant)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: out}, nil
		},
	))

	r.Register(NewTool(
		"unblock_merchant",
		"Remove a merchant block from the user's Rail card so charges go through again.",
		SimpleArgs(map[string]map[string]interface{}{
			"merchant": {"type": "string", "description": "Merchant name to unblock"},
			"confirm":  confirmParam(),
		}, []string{"merchant"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.MerchantBlock == nil {
				return &core.ToolResult{Error: "merchant blocking service not available"}, nil
			}
			merchant := GetArgString(args, "merchant")
			if merchant == "" {
				return &core.ToolResult{Error: "merchant is required"}, nil
			}
			if res := needsConfirm(args, fmt.Sprintf("Unblock %q — charges from this merchant will be allowed again.", merchant)); res != nil {
				return res, nil
			}
			out, err := deps.MerchantBlock.UnblockMerchant(ctx, userID, merchant)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: out}, nil
		},
	))

	r.Register(NewTool(
		"list_blocked_merchants",
		"List merchants currently blocked on the user's Rail card.",
		SimpleArgs(nil, nil),
		core.CategorySpending,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.MerchantBlock == nil {
				return &core.ToolResult{Error: "merchant blocking service not available"}, nil
			}
			blocked, err := deps.MerchantBlock.ListBlockedMerchants(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"blocked_merchants": blocked, "count": len(blocked)}}, nil
		},
	))
}

// RegisterTradeCopyTools registers copy-trading discovery and management.
func RegisterTradeCopyTools(r *Registry) {
	r.Register(NewTool(
		"list_trade_conductors",
		"List popular public figures (members of Congress and other public investors) whose disclosed trades users can copy, with recent activity and top tickers. Call when the user asks about copy trading or who they can copy.",
		SimpleArgs(nil, nil),
		core.CategoryInvestment,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.TradeCopy == nil {
				return &core.ToolResult{Error: "copy trading service not available"}, nil
			}
			conductors, err := deps.TradeCopy.ListConductors(ctx)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"conductors": conductors}}, nil
		},
	))

	r.Register(NewTool(
		"research_trader",
		"Research a public figure's publicly disclosed trades before copying: recent buys/sells, amounts, dates, and top tickers. ALWAYS call this when the user names someone to copy (\"copy Nancy Pelosi's trades\") and present the findings before offering copy_trader. If the user sent an image, get the person's name from it (or ask) and pass the name here.",
		SimpleArgs(map[string]map[string]interface{}{
			"trader": {"type": "string", "description": "The public figure's name, e.g. 'Nancy Pelosi'"},
		}, []string{"trader"}),
		core.CategoryInvestment,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.TradeCopy == nil {
				return &core.ToolResult{Error: "copy trading service not available"}, nil
			}
			trader := GetArgString(args, "trader")
			if trader == "" {
				return &core.ToolResult{Error: "trader is required"}, nil
			}
			research, err := deps.TradeCopy.ResearchTrader(ctx, userID, trader)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: research}, nil
		},
	))

	r.Register(NewTool(
		"get_copy_trading_status",
		"Show the user's active copy-trading positions (drafts): who they copy, allocated capital, and status.",
		SimpleArgs(nil, nil),
		core.CategoryInvestment,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.TradeCopy == nil {
				return &core.ToolResult{Error: "copy trading service not available"}, nil
			}
			status, err := deps.TradeCopy.GetCopyStatus(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"drafts": status}}, nil
		},
	))

	r.Register(NewTool(
		"copy_trader",
		"Copy a public figure's trades with real money: immediately splits the user's amount across the figure's recently disclosed stock buys (real brokerage orders) and keeps copying new disclosures automatically. Run research_trader first and present the recent trades. HIGH STAKES: restate the figure, the amount, which trades will execute now, and the disclosure lag, then get an explicit yes.",
		SimpleArgs(map[string]map[string]interface{}{
			"conductor": {"type": "string", "description": "The public figure's name, e.g. 'Nancy Pelosi'"},
			"amount":    {"type": "string", "description": "USD to put on this figure's trades, e.g. '200.00'"},
			"confirm":   confirmParam(),
		}, []string{"conductor", "amount"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.TradeCopy == nil {
				return &core.ToolResult{Error: "copy trading service not available"}, nil
			}
			conductor := GetArgString(args, "conductor")
			if conductor == "" {
				// Backward compatibility with the original argument name.
				conductor = GetArgString(args, "conductor_id")
			}
			amount := GetArgString(args, "amount")
			if conductor == "" || amount == "" {
				return &core.ToolResult{Error: "conductor and amount are required"}, nil
			}
			if res := checkAmountCap(amount); res != nil {
				return res, nil
			}
			if res := needsConfirm(args, fmt.Sprintf("Allocate $%s to automatically mirror %s's trades.", amount, conductor)); res != nil {
				return res, nil
			}
			out, err := deps.TradeCopy.StartCopying(ctx, userID, conductor, amount)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: out}, nil
		},
	))

	manage := func(name, desc, verb string, fn func(ctx context.Context, deps *core.Dependencies, userID uuid.UUID, draftID string) (map[string]interface{}, error)) *core.Tool {
		return NewTool(
			name,
			desc,
			SimpleArgs(map[string]map[string]interface{}{
				"draft_id": {"type": "string", "description": "Draft ID from get_copy_trading_status"},
				"confirm":  confirmParam(),
			}, []string{"draft_id"}),
			core.CategoryAction,
			func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
				if deps.TradeCopy == nil {
					return &core.ToolResult{Error: "copy trading service not available"}, nil
				}
				draftID := GetArgString(args, "draft_id")
				if draftID == "" {
					return &core.ToolResult{Error: "draft_id is required"}, nil
				}
				if res := needsConfirm(args, fmt.Sprintf("%s copy trading for draft %s.", verb, draftID)); res != nil {
					return res, nil
				}
				out, err := fn(ctx, deps, userID, draftID)
				if err != nil {
					return &core.ToolResult{Error: err.Error()}, nil
				}
				return &core.ToolResult{Data: out}, nil
			},
		)
	}

	r.Register(manage("pause_trade_copying",
		"Pause copying a conductor's trades (positions stay, no new trades mirror).", "Pause",
		func(ctx context.Context, deps *core.Dependencies, userID uuid.UUID, draftID string) (map[string]interface{}, error) {
			return deps.TradeCopy.PauseCopying(ctx, userID, draftID)
		}))
	r.Register(manage("resume_trade_copying",
		"Resume a paused copy-trading draft so new trades mirror again.", "Resume",
		func(ctx context.Context, deps *core.Dependencies, userID uuid.UUID, draftID string) (map[string]interface{}, error) {
			return deps.TradeCopy.ResumeCopying(ctx, userID, draftID)
		}))
	r.Register(manage("stop_trade_copying",
		"Stop copying a conductor permanently and unlink the draft (existing positions are kept, nothing is sold automatically).", "Stop",
		func(ctx context.Context, deps *core.Dependencies, userID uuid.UUID, draftID string) (map[string]interface{}, error) {
			return deps.TradeCopy.StopCopying(ctx, userID, draftID)
		}))
}

// RegisterExecutionTools registers all Execution Engine (spec 5.2) tools.
func RegisterExecutionTools(r *Registry) {
	RegisterBillPayTools(r)
	RegisterSubscriptionAuditTools(r)
	RegisterInvestmentExecutionTools(r)
	RegisterYieldOptimizationTools(r)
	RegisterMerchantBlockTools(r)
	RegisterTradeCopyTools(r)
}
