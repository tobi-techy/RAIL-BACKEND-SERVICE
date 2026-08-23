package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
	"github.com/shopspring/decimal"
)

func RegisterComparativeTool(r *Registry) {
	r.Register(NewTool(
		"get_comparative_context",
		"Compare user's spending against similar users or past periods to provide context on financial habits",
		SimpleArgs(nil, nil),
		core.CategoryPlanning,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Comparative == nil {
				return &core.ToolResult{Error: "comparative service not available"}, nil
			}
			result, err := deps.Comparative.GetComparativeContext(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: result}, nil
		},
	))

	r.Register(NewTool(
		"get_spending_comparison",
		"Compare spending for a specific period against the previous period to show changes",
		SimpleArgs(map[string]map[string]interface{}{
			"period": {"type": "string", "description": "Period to compare (this_month, last_month, this_week, last_week)"},
		}, nil),
		core.CategorySpending,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Comparative == nil {
				return &core.ToolResult{Error: "comparative service not available"}, nil
			}
			period := "this_month"
			if p, _ := args["period"].(string); p != "" {
				period = p
			}
			result, err := deps.Comparative.GetSpendingComparison(ctx, userID, period)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: result}, nil
		},
	))
}

func RegisterSimulatorTool(r *Registry) {
	r.Register(NewTool(
		"simulate_savings",
		"Show a savings projection — what happens if you save X amount monthly for Y years",
		SimpleArgs(map[string]map[string]interface{}{
			"monthly_amount": {"type": "string", "description": "Monthly savings amount (e.g. '50000')"},
			"years":          {"type": "integer", "description": "Number of years to project"},
		}, []string{"monthly_amount", "years"}),
		core.CategoryPlanning,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Simulator == nil {
				return &core.ToolResult{Error: "simulator not available"}, nil
			}
			amountStr, _ := args["monthly_amount"].(string)
			yearsFloat, _ := args["years"].(float64)
			if amountStr == "" || yearsFloat == 0 {
				return &core.ToolResult{Error: "monthly_amount and years are required"}, nil
			}
			amount, err := decimal.NewFromString(amountStr)
			if err != nil {
				return &core.ToolResult{Error: fmt.Sprintf("invalid amount: %s", amountStr)}, nil
			}
			result, err := deps.Simulator.SimulateSavings(ctx, userID, amount, int(yearsFloat))
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: result}, nil
		},
	))
}

func RegisterTaxTools(r *Registry) {
	r.Register(NewTool(
		"get_tax_summary",
		"Get the user's tax summary for the current tax year",
		SimpleArgs(nil, nil),
		core.CategoryPlanning,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Tax == nil {
				return &core.ToolResult{Error: "tax service not available"}, nil
			}
			result, err := deps.Tax.GetTaxSummary(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: result}, nil
		},
	))

	r.Register(NewTool(
		"get_tax_calendar",
		"Get important tax dates and deadlines for the user",
		SimpleArgs(nil, nil),
		core.CategoryPlanning,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Tax == nil {
				return &core.ToolResult{Error: "tax service not available"}, nil
			}
			result, err := deps.Tax.GetTaxCalendar(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: result}, nil
		},
	))

	r.Register(NewTool(
		"send_report",
		"Email a financial report to the user or their advisor",
		SimpleArgs(map[string]map[string]interface{}{
			"report_type": {"type": "string", "description": "Type of report (e.g. monthly, tax, annual)"},
			"email":       {"type": "string", "description": "Optional email override"},
		}, []string{"report_type"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.ReportEmail == nil {
				return &core.ToolResult{Error: "email report service not available"}, nil
			}
			reportType, _ := args["report_type"].(string)
			err := deps.ReportEmail.SendReport(ctx, userID, reportType, args)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"status": "sent", "type": reportType}}, nil
		},
	))
}

func RegisterFinancialIntelligenceTools(r *Registry) {
	r.Register(NewTool(
		"get_financial_health",
		"Get the user's financial health score and recommendations",
		SimpleArgs(nil, nil),
		core.CategoryPlanning,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.FinancialGovernance == nil {
				return &core.ToolResult{Error: "financial governance not available"}, nil
			}
			result, err := deps.FinancialGovernance.GetFinancialHealth(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: result}, nil
		},
	))

	r.Register(NewTool(
		"get_financial_plan",
		"Get the user's personalized financial plan with recommendations",
		SimpleArgs(nil, nil),
		core.CategoryPlanning,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.FinancialGovernance == nil {
				return &core.ToolResult{Error: "financial governance not available"}, nil
			}
			result, err := deps.FinancialGovernance.GetFinancialPlan(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: result}, nil
		},
	))

	r.Register(NewTool(
		"get_cash_flow_forecast",
		"Forecast future cash flow based on income and spending patterns",
		SimpleArgs(nil, nil),
		core.CategoryPlanning,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.FinancialGovernance == nil {
				return &core.ToolResult{Error: "financial governance not available"}, nil
			}
			result, err := deps.FinancialGovernance.GetCashFlowForecast(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: result}, nil
		},
	))

	r.Register(NewTool(
		"get_financial_audit",
		"Run a complete financial audit for the user — identifies leaks, opportunities, and risks",
		SimpleArgs(nil, nil),
		core.CategoryPlanning,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.FinancialGovernance == nil {
				return &core.ToolResult{Error: "financial governance not available"}, nil
			}
			result, err := deps.FinancialGovernance.GetFinancialAudit(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: result}, nil
		},
	))

	r.Register(NewTool(
		"get_action_receipts",
		"Get recent AI-initiated action receipts (transactions, transfers, etc.)",
		SimpleArgs(map[string]map[string]interface{}{
			"limit": {"type": "integer", "description": "Number of receipts (default 5)"},
		}, nil),
		core.CategoryHistory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.MiriamIntell == nil {
				return &core.ToolResult{Error: "Miriam intelligence not available"}, nil
			}
			limit := 5
			if l, _ := args["limit"].(float64); l > 0 {
				limit = int(l)
			}
			result, err := deps.MiriamIntell.GetDecisionReceipts(ctx, userID, limit)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"receipts": result}}, nil
		},
	))
}

func RegisterOperatingPlanTool(r *Registry) {
	r.Register(NewTool(
		"get_money_operating_plan",
		"Get a personalized money operating plan — income allocation recommendations based on profile and goals",
		SimpleArgs(nil, nil),
		core.CategoryPlanning,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.FinancialGovernance == nil {
				return &core.ToolResult{Error: "financial governance not available"}, nil
			}
			result, err := deps.FinancialGovernance.GetFinancialPlan(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: result}, nil
		},
	))
}

func RegisterMiriamBriefTool(r *Registry) {
	r.Register(NewTool(
		"get_miriam_brief",
		"Get a quick financial brief — key numbers and updates the user should know right now. ALWAYS call this for 'how am I doing' / overview. Never invent numbers.",
		SimpleArgs(nil, nil),
		core.CategoryOverview,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			// Build a real brief from the same sources as the streaming path so
			// iMessage/eval never get a placeholder string.
			result := map[string]interface{}{
				"currency": "USD",
			}
			if deps.State != nil {
				state, err := deps.State.GetState(ctx, userID)
				if err != nil {
					return &core.ToolResult{Error: "could not load balances: " + err.Error()}, nil
				}
				if state != nil && state.Balances != nil {
					b := state.Balances
					result["spend_balance"] = "$" + b.Spend.StringFixed(2)
					result["stash_balance"] = "$" + b.Stash.StringFixed(2)
					result["total_balance"] = "$" + b.Spend.Add(b.Stash).StringFixed(2)
				}
			}
			if deps.Spending != nil {
				flow, err := deps.Spending.GetMoneyFlow(ctx, userID, 1)
				if err != nil {
					result["money_flow_error"] = err.Error()
				} else if flow != nil {
					result["this_month"] = flow
				} else {
					result["this_month"] = map[string]interface{}{"empty": true}
				}
			}
			if deps.MiriamIntell != nil {
				if ms, err := deps.MiriamIntell.GetMoneyState(ctx, userID); err == nil && ms != nil {
					result["money_state"] = ms
				}
			}
			if deps.AnomalyContextFn != nil {
				if a := deps.AnomalyContextFn(ctx, userID); a != "" {
					result["anomaly_context"] = a
				}
			}
			return &core.ToolResult{Data: result}, nil
		},
	))
}

func RegisterWarrantyTool(r *Registry) {
	r.Register(NewTool(
		"get_warranty_items",
		"Get warranty and protection plan items for the user's purchases",
		SimpleArgs(nil, nil),
		core.CategoryOverview,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Warranty == nil {
				return &core.ToolResult{Error: "warranty service not available"}, nil
			}
			items, err := deps.Warranty.GetItems(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"items": items}}, nil
		},
	))
}

func RegisterReceiptChallengeTool(r *Registry) {
	r.Register(NewTool(
		"get_receipt_challenges",
		"Get receipt scanning challenges available for the user",
		SimpleArgs(nil, nil),
		core.CategoryOverview,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.ReceiptChallenge == nil {
				return &core.ToolResult{Error: "receipt challenges not available"}, nil
			}
			challenges, err := deps.ReceiptChallenge.GetChallenges(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"challenges": challenges}}, nil
		},
	))
}

func RegisterPriceTrackingTool(r *Registry) {
	r.Register(NewTool(
		"get_price_changes",
		"Get price changes and tracking for items the user is watching",
		SimpleArgs(nil, nil),
		core.CategoryOverview,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.PriceTracker == nil {
				return &core.ToolResult{Error: "price tracker not available"}, nil
			}
			changes, err := deps.PriceTracker.GetChanges(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"changes": changes}}, nil
		},
	))
}

func RegisterMerchantInsightTool(r *Registry) {
	r.Register(NewTool(
		"get_merchant_insights",
		"Get insights about a specific merchant the user transacts with frequently",
		SimpleArgs(map[string]map[string]interface{}{
			"merchant": {"type": "string", "description": "Merchant name to analyze"},
		}, []string{"merchant"}),
		core.CategorySpending,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Merchant == nil {
				return &core.ToolResult{Error: "merchant service not available"}, nil
			}
			merchant, _ := args["merchant"].(string)
			if merchant == "" {
				return &core.ToolResult{Error: "merchant name is required"}, nil
			}
			insights, err := deps.Merchant.GetInsights(ctx, merchant)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: insights}, nil
		},
	))
}

func RegisterSplitReceiptTool(r *Registry) {
	r.Register(NewTool(
		"split_receipt",
		"Split a scanned receipt with friends. Divides the total equally among you + participants. Each friend is charged via P2P to their Rail tag (@name), email, or phone (non-users get a claim link). Moves real money — staged for Face ID confirmation. Use after a receipt was scanned and you have receipt_id.",
		SimpleArgs(map[string]map[string]interface{}{
			"receipt_id":   {"type": "string", "description": "UUID of the scanned receipt"},
			"participants": {"type": "string", "description": "Comma-separated rail tags, emails, or phones (e.g. '@john,@jane,friend@email.com')"},
			"message":      {"type": "string", "description": "Optional note on the split requests"},
		}, []string{"receipt_id", "participants"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Receipt == nil {
				return &core.ToolResult{Error: "receipt split not available"}, nil
			}
			receiptIDStr, _ := args["receipt_id"].(string)
			participantsStr, _ := args["participants"].(string)
			message, _ := args["message"].(string)
			if receiptIDStr == "" || participantsStr == "" {
				return &core.ToolResult{Error: "receipt_id and participants are required"}, nil
			}
			receiptID, err := uuid.Parse(receiptIDStr)
			if err != nil {
				return &core.ToolResult{Error: fmt.Sprintf("invalid receipt_id: %s", receiptIDStr)}, nil
			}
			participants := splitComma(participantsStr)
			if len(participants) == 0 {
				return &core.ToolResult{Error: "at least one participant is required"}, nil
			}
			result, err := deps.Receipt.Split(ctx, userID, receiptID, participants, message)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: result}, nil
		},
	))
}

// RegisterP2PTools registers send-money tools (Rail tag or claim-link invite).
func RegisterP2PTools(r *Registry) {
	r.Register(NewTool(
		"lookup_recipient",
		"Look up whether a Rail tag, email, or phone can receive money. Call before send_money when the user names someone. Does not move money.",
		SimpleArgs(map[string]map[string]interface{}{
			"identifier": {"type": "string", "description": "Rail tag (@name), email, or phone"},
		}, []string{"identifier"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.P2P == nil {
				return &core.ToolResult{Error: "P2P transfers not available"}, nil
			}
			id := strings.TrimSpace(GetArgString(args, "identifier"))
			if id == "" {
				return &core.ToolResult{Error: "identifier is required"}, nil
			}
			res, err := deps.P2P.Lookup(ctx, id)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: res}, nil
		},
	))

	r.Register(NewTool(
		"send_money",
		"Send money from Spend to a friend. Identifier is a Rail tag (@name), email, or phone. If they are on Rail, funds transfer immediately after Face ID. If not, funds are reserved and a claim link is created for them. Moves real money — always staged for Face ID confirmation.",
		SimpleArgs(map[string]map[string]interface{}{
			"identifier": {"type": "string", "description": "Rail tag (@name), email, or phone of recipient"},
			"amount":     {"type": "string", "description": "USD amount e.g. '25.00'"},
			"note":       {"type": "string", "description": "Optional note for the recipient"},
		}, []string{"identifier", "amount"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.P2P == nil {
				return &core.ToolResult{Error: "P2P transfers not available"}, nil
			}
			identifier := strings.TrimSpace(GetArgString(args, "identifier"))
			amount := strings.TrimSpace(GetArgString(args, "amount"))
			note := GetArgString(args, "note")
			if identifier == "" || amount == "" {
				return &core.ToolResult{Error: "identifier and amount are required"}, nil
			}
			idem := GetArgString(args, "idempotency_key")
			if idem == "" {
				idem = fmt.Sprintf("miriam-p2p-%s-%s-%s", userID.String(), identifier, amount)
			}
			res, err := deps.P2P.Send(ctx, userID, identifier, amount, note, idem)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: res}, nil
		},
	))
}

func RegisterWebSearchTool(r *Registry) {
	r.Register(NewTool(
		"web_search",
		"Search the web for financial news, definitions, or current events",
		SimpleArgs(map[string]map[string]interface{}{
			"query": {"type": "string", "description": "Search query"},
		}, []string{"query"}),
		core.CategoryKnowledge,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.WebSearch == nil {
				return &core.ToolResult{Error: "web search not available"}, nil
			}
			query, _ := args["query"].(string)
			if query == "" {
				return &core.ToolResult{Error: "query is required"}, nil
			}
			limit := 5
			if l, _ := args["limit"].(float64); l > 0 {
				limit = int(l)
			}
			results, err := deps.WebSearch.Search(ctx, query, limit)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"results": results}}, nil
		},
	))
}

func RegisterInvestmentProductTool(r *Registry) {
	r.Register(NewTool(
		"get_investment_products",
		"Get available investment products the user can invest in",
		SimpleArgs(nil, nil),
		core.CategoryInvestment,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Investment == nil {
				return &core.ToolResult{Error: "investment service not available"}, nil
			}
			products, err := deps.Investment.GetProducts(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"products": products}}, nil
		},
	))
}

func RegisterEngagementTools(r *Registry) {
	r.Register(NewTool(
		"celebrate",
		"Send a celebration message for a financial milestone achieved",
		SimpleArgs(map[string]map[string]interface{}{
			"milestone": {"type": "string", "description": "What milestone was achieved"},
		}, []string{"milestone"}),
		core.CategoryEngagement,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			milestone, _ := args["milestone"].(string)
			if milestone == "" {
				milestone = "a financial milestone"
			}
			return &core.ToolResult{
				Data: map[string]interface{}{
					"message":  fmt.Sprintf("Congratulations on %s! That's awesome! 🎉", milestone),
					"emoji":    "🎉",
					"confetti": true,
				},
			}, nil
		},
	))

	r.Register(NewTool(
		"send_poll",
		"Create and send a poll to the user about their finances",
		SimpleArgs(map[string]map[string]interface{}{
			"question": {"type": "string", "description": "Poll question"},
			"options":  {"type": "string", "description": "Comma-separated poll options"},
		}, []string{"question", "options"}),
		core.CategoryEngagement,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			question, _ := args["question"].(string)
			options, _ := args["options"].(string)
			if question == "" {
				return &core.ToolResult{Error: "question is required"}, nil
			}
			return &core.ToolResult{
				Data: map[string]interface{}{
					"question": question,
					"options":  splitComma(options),
					"status":   "created",
				},
			}, nil
		},
	))
}

func RegisterMemoryTools(r *Registry) {
	r.Register(NewTool(
		"list_memory",
		"List what Miriam remembers about the user — goals, preferences, life events",
		SimpleArgs(nil, nil),
		core.CategoryMemory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Memory == nil {
				return &core.ToolResult{Error: "memory service not available"}, nil
			}
			facts, err := deps.Memory.SearchFacts(ctx, userID, "", 50)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			if len(facts) == 0 {
				return &core.ToolResult{Data: map[string]interface{}{
					"facts":   []interface{}{},
					"empty":   true,
					"message": "I don't have stored facts about you yet.",
				}}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"facts": facts, "count": len(facts)}}, nil
		},
	))

	r.Register(NewTool(
		"forget_memory",
		"Tell Miriam to forget a specific fact or memory. Prefer fact_id from list_memory when available; otherwise pass a text description to match.",
		SimpleArgs(map[string]map[string]interface{}{
			"fact":    {"type": "string", "description": "Description of what to forget"},
			"fact_id": {"type": "string", "description": "Fact UUID from list_memory, when known"},
		}, nil),
		core.CategoryMemory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Memory == nil {
				return &core.ToolResult{Error: "memory service not available"}, nil
			}
			factIDStr, _ := args["fact_id"].(string)
			factText, _ := args["fact"].(string)
			if factIDStr == "" && factText == "" {
				return &core.ToolResult{Error: "fact_id or fact description is required"}, nil
			}
			if factIDStr != "" {
				fid, err := uuid.Parse(factIDStr)
				if err != nil {
					return &core.ToolResult{Error: "invalid fact_id"}, nil
				}
				if err := deps.Memory.ForgetFact(ctx, userID, fid); err != nil {
					return &core.ToolResult{Error: err.Error()}, nil
				}
				return &core.ToolResult{Data: map[string]interface{}{"status": "forgotten", "fact_id": factIDStr}}, nil
			}
			// Text match: search facts and forget the best match.
			facts, err := deps.Memory.SearchFacts(ctx, userID, factText, 5)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			if len(facts) == 0 {
				return &core.ToolResult{Data: map[string]interface{}{
					"status":  "not_found",
					"message": "I couldn't find a matching memory to forget.",
				}}, nil
			}
			// Prefer a fact with an ID; otherwise report we need the id.
			for _, f := range facts {
				if f.ID == "" {
					continue
				}
				fid, err := uuid.Parse(f.ID)
				if err != nil {
					continue
				}
				if err := deps.Memory.ForgetFact(ctx, userID, fid); err != nil {
					return &core.ToolResult{Error: err.Error()}, nil
				}
				return &core.ToolResult{Data: map[string]interface{}{
					"status":  "forgotten",
					"fact_id": f.ID,
					"fact":    f.Fact,
				}}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{
				"status":  "needs_fact_id",
				"message": "I found matching memories but need a fact_id to forget them safely. Call list_memory first.",
				"matches": facts,
			}}, nil
		},
	))
}

func RegisterPersonalityModeTool(r *Registry) {
	r.Register(NewTool(
		"set_personality_mode",
		"Change how Miriam talks to the user. Modes: 'default' (direct, sharp, slightly witty), 'roast' (brutally honest, funny, calls out bad habits), 'coach' (encouraging, strategic, accountability-focused), 'protector' (urgent, clear, action-oriented when financial threats detected), 'celebration' (excited, proud, amplifies wins), 'quiet' (silent, invisible — minimal talk, just action).",
		SimpleArgs(map[string]map[string]interface{}{
			"mode": {
				"type":        "string",
				"enum":        []string{"default", "roast", "coach", "protector", "celebration", "quiet"},
				"description": "The personality mode to switch to.",
			},
		}, []string{"mode"}),
		core.CategoryMemory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			mode, _ := args["mode"].(string)
			if err := deps.Memory.SetPersonalityMode(ctx, userID, mode); err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"status": "ok", "mode": mode}}, nil
		},
	))
}

func RegisterControlLevelTool(r *Registry) {
	r.Register(NewTool(
		"set_control_level",
		"Control how autonomous Miriam is. Levels: 'full' (Full Autopilot — Miriam acts on pre-approved actions, asks on new ones), 'guided' (Guided — Miriam suggests actions, waits for approval before doing anything), 'monitor' (Manual — Miriam only alerts and advises).",
		SimpleArgs(map[string]map[string]interface{}{
			"level": {
				"type":        "string",
				"enum":        []string{"full", "guided", "monitor"},
				"description": "The control level: full (act autonomously), guided (suggest + confirm), monitor (only advise).",
			},
		}, []string{"level"}),
		core.CategoryMemory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			level, _ := args["level"].(string)
			if err := deps.Memory.SetControlLevel(ctx, userID, level); err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"status": "ok", "level": level}}, nil
		},
	))
}

func RegisterAccountSummaryTool(r *Registry) {
	r.Register(NewTool(
		"get_account_summary",
		"Get the user's full financial overview — balances, this month's totals, budget status, streak. ALWAYS call this for balance/overview questions. Never invent dollar amounts.",
		SimpleArgs(nil, nil),
		core.CategoryOverview,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			// Real overview for messaging/eval path (core registry). Must not return
			// a placeholder — iMessage uses this path exclusively.
			result := map[string]interface{}{
				"currency":      "USD",
				"currency_note": "All balances are in US Dollars (USDC).",
			}

			if deps.State == nil {
				return &core.ToolResult{Error: "balance data is unavailable"}, nil
			}
			state, err := deps.State.GetState(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: "balance fetch failed: " + err.Error()}, nil
			}
			if state != nil && state.Balances != nil {
				b := state.Balances
				result["spend_balance"] = "$" + b.Spend.StringFixed(2)
				result["stash_balance"] = "$" + b.Stash.StringFixed(2)
				result["total_balance"] = "$" + b.Spend.Add(b.Stash).StringFixed(2)
			} else {
				result["balances_error"] = "balance data is unavailable"
			}

			if deps.Spending != nil {
				flow, err := deps.Spending.GetMoneyFlow(ctx, userID, 1)
				if err != nil {
					result["this_month_error"] = err.Error()
				} else if flow != nil {
					result["this_month"] = flow
				} else {
					result["this_month"] = map[string]interface{}{"empty": true}
				}
			}

			if deps.Budget != nil {
				budget, err := deps.Budget.GetByUserID(ctx, userID)
				if err != nil {
					result["budget_error"] = err.Error()
				} else if budget != nil {
					result["budget"] = budget
				} else {
					result["budget"] = map[string]interface{}{"has_budget": false}
				}
			}

			if deps.Portfolio != nil {
				if streak, err := deps.Portfolio.GetStreak(ctx, userID); err == nil && streak != nil {
					result["streak_days"] = streak.Days
				}
			}

			return &core.ToolResult{Data: result}, nil
		},
	))
}

// RegisterMiriamIntelligenceTools exposes mandate / money-state tools on the core
// registry so iMessage and eval can answer "what can you do automatically?".
func RegisterMiriamIntelligenceTools(r *Registry) {
	r.Register(NewTool(
		"get_miriam_money_state",
		"Get Miriam's durable money state: safe-to-spend, runway, confidence, income cadence, upcoming obligations. Use before explaining what Miriam quietly sees.",
		SimpleArgs(nil, nil),
		core.CategoryOverview,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.MiriamIntell == nil {
				return &core.ToolResult{Error: "money state not available"}, nil
			}
			state, err := deps.MiriamIntell.GetMoneyState(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			if state == nil {
				return &core.ToolResult{Data: map[string]interface{}{"empty": true, "message": "No money state yet — fund the account first."}}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"state": state}}, nil
		},
	))

	r.Register(NewTool(
		"list_miriam_mandates",
		"List user-approved Miriam autopilot mandates and their rules. Use when the user asks what Miriam is allowed to do automatically.",
		SimpleArgs(nil, nil),
		core.CategoryAutomation,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.MiriamIntell == nil {
				return &core.ToolResult{Error: "mandates not available"}, nil
			}
			mandates, err := deps.MiriamIntell.GetMandates(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			if len(mandates) == 0 {
				return &core.ToolResult{Data: map[string]interface{}{
					"mandates": []interface{}{},
					"count":    0,
					"empty":    true,
					"message":  "No active mandates. You can approve one so I can quietly move surplus to Stash when safe.",
				}}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"mandates": mandates, "count": len(mandates)}}, nil
		},
	))

	r.Register(NewTool(
		"get_miriam_decision_receipts",
		"Get recent Miriam decision receipts for quiet actions, skips, and failures. Use when the user asks what Miriam did or why money moved.",
		SimpleArgs(map[string]map[string]interface{}{
			"limit": {"type": "integer", "description": "Number of receipts (default 5, max 20)"},
		}, nil),
		core.CategoryOverview,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.MiriamIntell == nil {
				return &core.ToolResult{Error: "decision receipts not available"}, nil
			}
			limit := 5
			if l, _ := args["limit"].(float64); l > 0 {
				limit = int(l)
			}
			if limit > 20 {
				limit = 20
			}
			receipts, err := deps.MiriamIntell.GetDecisionReceipts(ctx, userID, limit)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			if len(receipts) == 0 {
				return &core.ToolResult{Data: map[string]interface{}{
					"receipts": []interface{}{},
					"count":    0,
					"empty":    true,
					"message":  "No decision receipts yet.",
				}}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"receipts": receipts, "count": len(receipts)}}, nil
		},
	))

	r.Register(NewTool(
		"get_anomalies",
		"Get recent unusual spending signals Miriam detected (bill spikes, duplicates, fraud signals, spending acceleration). Use for 'anything weird' / anomaly questions.",
		SimpleArgs(nil, nil),
		core.CategorySpending,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.AnomalyContextFn == nil {
				return &core.ToolResult{Data: map[string]interface{}{
					"empty":   true,
					"message": "No anomaly scan data right now.",
				}}, nil
			}
			ctxStr := deps.AnomalyContextFn(ctx, userID)
			if strings.TrimSpace(ctxStr) == "" {
				return &core.ToolResult{Data: map[string]interface{}{
					"empty":   true,
					"message": "Nothing unusual in the latest scan.",
				}}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"anomalies": ctxStr}}, nil
		},
	))

	r.Register(NewTool(
		"list_mandate_suggestions",
		"List pending mandate suggestions Miriam wants the user to approve (e.g. quiet stash moves). Use when user asks what you can automate or after proposing autopilot.",
		SimpleArgs(nil, nil),
		core.CategoryAutomation,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.MiriamIntell == nil {
				return &core.ToolResult{Error: "mandate suggestions not available"}, nil
			}
			list, err := deps.MiriamIntell.ListMandateSuggestions(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			if len(list) == 0 {
				return &core.ToolResult{Data: map[string]interface{}{
					"suggestions": []interface{}{},
					"count":       0,
					"empty":       true,
					"message":     "No pending mandate suggestions right now.",
				}}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"suggestions": list, "count": len(list)}}, nil
		},
	))

	r.Register(NewTool(
		"accept_mandate_suggestion",
		"Accept a pending mandate suggestion so Miriam can act within its limits. Requires suggestion_id from list_mandate_suggestions. After accept, tell the user to switch to Act (set_control_level full) for quiet execution.",
		SimpleArgs(map[string]map[string]interface{}{
			"suggestion_id": {"type": "string", "description": "UUID of the pending suggestion"},
			"confirm":       {"type": "boolean", "description": "Must be true after user explicitly agrees"},
		}, []string{"suggestion_id"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.MiriamIntell == nil {
				return &core.ToolResult{Error: "mandate suggestions not available"}, nil
			}
			idStr, _ := args["suggestion_id"].(string)
			sid, err := uuid.Parse(idStr)
			if err != nil {
				return &core.ToolResult{Error: "invalid suggestion_id"}, nil
			}
			// Staging path strips confirm; on re-execute after user yes, confirm is true.
			if confirm, _ := args["confirm"].(bool); !confirm {
				return &core.ToolResult{
					Data: map[string]interface{}{
						"status":  "needs_confirmation",
						"message": "I'll activate this mandate so I can act within its limits. Confirm to proceed.",
					},
					Action: &core.PendingAction{
						Type:        "accept_mandate_suggestion",
						Description: "Accept mandate suggestion " + idStr,
						Params:      map[string]interface{}{"suggestion_id": idStr},
					},
				}, nil
			}
			out, err := deps.MiriamIntell.AcceptMandateSuggestion(ctx, userID, sid)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: out}, nil
		},
	))

	r.Register(NewTool(
		"dismiss_mandate_suggestion",
		"Dismiss a pending mandate suggestion the user does not want.",
		SimpleArgs(map[string]map[string]interface{}{
			"suggestion_id": {"type": "string", "description": "UUID of the pending suggestion"},
		}, []string{"suggestion_id"}),
		core.CategoryAutomation,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.MiriamIntell == nil {
				return &core.ToolResult{Error: "mandate suggestions not available"}, nil
			}
			idStr, _ := args["suggestion_id"].(string)
			sid, err := uuid.Parse(idStr)
			if err != nil {
				return &core.ToolResult{Error: "invalid suggestion_id"}, nil
			}
			if err := deps.MiriamIntell.DismissMandateSuggestion(ctx, userID, sid); err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"status": "dismissed", "suggestion_id": idStr}}, nil
		},
	))
}

func splitComma(s string) []string {
	if s == "" {
		return nil
	}
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			if i > start {
				item := strings.TrimSpace(s[start:i])
				if item != "" {
					result = append(result, item)
				}
			}
			start = i + 1
		}
	}
	if start < len(s) {
		item := strings.TrimSpace(s[start:])
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func RegisterAllRemainingTools(r *Registry) {
	RegisterComparativeTool(r)
	RegisterSimulatorTool(r)
	RegisterTaxTools(r)
	RegisterFinancialIntelligenceTools(r)
	RegisterOperatingPlanTool(r)
	RegisterMiriamBriefTool(r)
	RegisterMiriamIntelligenceTools(r)
	RegisterWarrantyTool(r)
	RegisterReceiptChallengeTool(r)
	RegisterPriceTrackingTool(r)
	RegisterMerchantInsightTool(r)
	RegisterSplitReceiptTool(r)
	RegisterP2PTools(r)
	RegisterWebSearchTool(r)
	RegisterInvestmentProductTool(r)
	RegisterEngagementTools(r)
	RegisterMemoryTools(r)
	RegisterPersonalityModeTool(r)
	RegisterControlLevelTool(r)
	RegisterAccountSummaryTool(r)
	RegisterGameplayTools(r)
	RegisterBabyStepsTools(r)
	RegisterBankStatementTools(r)
}
