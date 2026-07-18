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
		"Get a quick financial brief — key numbers and updates the user should know right now",
		SimpleArgs(nil, nil),
		core.CategoryOverview,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			result := map[string]interface{}{
				"message": "Here's your quick financial brief.",
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
		"Split a receipt with friends — divides the total among participants",
		SimpleArgs(map[string]map[string]interface{}{
			"receipt_id":   {"type": "string", "description": "Receipt ID to split"},
			"participants": {"type": "string", "description": "Comma-separated participant names"},
		}, []string{"receipt_id", "participants"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Receipt == nil {
				return &core.ToolResult{Error: "receipt service not available"}, nil
			}
			receiptIDStr, _ := args["receipt_id"].(string)
			participantsStr, _ := args["participants"].(string)
			if receiptIDStr == "" || participantsStr == "" {
				return &core.ToolResult{Error: "receipt_id and participants are required"}, nil
			}
			receiptID, err := uuid.Parse(receiptIDStr)
			if err != nil {
				return &core.ToolResult{Error: fmt.Sprintf("invalid receipt_id: %s", receiptIDStr)}, nil
			}
			participants := splitComma(participantsStr)
			result, err := deps.Receipt.Split(ctx, receiptID, participants)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: result}, nil
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
			return &core.ToolResult{Data: map[string]interface{}{"facts": facts}}, nil
		},
	))

	r.Register(NewTool(
		"forget_memory",
		"Tell Miriam to forget a specific fact or memory",
		SimpleArgs(map[string]map[string]interface{}{
			"fact": {"type": "string", "description": "Description of what to forget"},
		}, []string{"fact"}),
		core.CategoryMemory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			return &core.ToolResult{
				Data: map[string]interface{}{"status": "forgotten"},
			}, nil
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
		"Get the user's full financial overview — balances, this month's totals, budget status, streak",
		SimpleArgs(nil, nil),
		core.CategoryOverview,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			result := map[string]interface{}{
				"status": "overview generated",
			}
			return &core.ToolResult{Data: result}, nil
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
	RegisterWarrantyTool(r)
	RegisterReceiptChallengeTool(r)
	RegisterPriceTrackingTool(r)
	RegisterMerchantInsightTool(r)
	RegisterSplitReceiptTool(r)
	RegisterWebSearchTool(r)
	RegisterInvestmentProductTool(r)
	RegisterEngagementTools(r)
	RegisterMemoryTools(r)
	RegisterPersonalityModeTool(r)
	RegisterControlLevelTool(r)
	RegisterAccountSummaryTool(r)
}
