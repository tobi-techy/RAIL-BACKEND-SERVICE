package tools

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
)

// RegisterPortfolioTools registers all portfolio-related tools.
func RegisterPortfolioTools(r *Registry) {
	r.Register(NewTool(
		"get_portfolio_stats",
		"Get the user's portfolio statistics including total value, weekly return, and weekly return percentage. If they ask 'how are my investments?' use this.",
		SimpleArgs(nil, nil),
		core.CategoryPortfolio,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Portfolio == nil {
				return &core.ToolResult{Error: "portfolio service not available"}, nil
			}
			stats, err := deps.Portfolio.GetWeeklyStats(ctx, userID)
			if err != nil {
				return nil, err
			}
			return &core.ToolResult{
				Data: map[string]interface{}{
					"total_value":       stats.TotalValue.String(),
					"weekly_return":     stats.WeeklyReturn.String(),
					"weekly_return_pct": stats.WeeklyReturnPct.String(),
				},
			}, nil
		},
	))

	r.Register(NewTool(
		"get_top_movers",
		"Get the top gainers and losers in the user's portfolio. Optionally specify a limit (1-20, default 5).",
		SimpleArgs(map[string]map[string]interface{}{
			"limit": NumberParam("Number of movers to return (1-20, default 5)"),
		}, nil),
		core.CategoryPortfolio,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Portfolio == nil {
				return &core.ToolResult{Error: "portfolio service not available"}, nil
			}
			limit := int(GetArgFloat(args, "limit"))
			if limit <= 0 || limit > 20 {
				limit = 5
			}

			movers, err := deps.Portfolio.GetTopMovers(ctx, userID, limit)
			if err != nil {
				return nil, err
			}
			return &core.ToolResult{
				Data: map[string]interface{}{"movers": movers},
			}, nil
		},
	))

	r.Register(NewTool(
		"get_allocations",
		"Get the user's portfolio allocation by basket/strategy. Shows how their money is distributed across different investment baskets.",
		SimpleArgs(nil, nil),
		core.CategoryPortfolio,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Portfolio == nil {
				return &core.ToolResult{Error: "portfolio service not available"}, nil
			}
			allocs, err := deps.Portfolio.GetAllocations(ctx, userID)
			if err != nil {
				return nil, err
			}
			return &core.ToolResult{
				Data: map[string]interface{}{"allocations": allocs},
			}, nil
		},
	))

	r.Register(NewTool(
		"get_contributions",
		"Get the user's contribution summary (deposits, roundups, cashback) for a given period and type.",
		SimpleArgs(map[string]map[string]interface{}{
			"type":   StringParam("Contribution type: 'deposits', 'roundups', 'cashback', or 'all'"),
			"period": StringParam("Period: '7d', '30d', '90d', or '1y'"),
		}, nil),
		core.CategoryPortfolio,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			return &core.ToolResult{Error: "contributions data not available in this version"}, nil
		},
	))

	r.Register(NewTool(
		"get_streak",
		"Get the user's investment streak — how many consecutive days/weeks they've been investing.",
		SimpleArgs(nil, nil),
		core.CategoryPortfolio,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Portfolio == nil {
				return &core.ToolResult{Error: "portfolio service not available"}, nil
			}
			streak, err := deps.Portfolio.GetStreak(ctx, userID)
			if err != nil {
				return nil, err
			}
			if streak == nil {
				return &core.ToolResult{Data: map[string]interface{}{"days": 0}}, nil
			}
			return &core.ToolResult{
				Data: map[string]interface{}{
					"days":        streak.Days,
					"longest":     streak.Longest,
					"last_active": streak.LastActive,
				},
			}, nil
		},
	))

	r.Register(NewTool(
		"get_weekly_news",
		"Get recent news about companies in the user's portfolio.",
		SimpleArgs(nil, nil),
		core.CategoryPortfolio,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Portfolio == nil {
				return &core.ToolResult{Error: "portfolio service not available"}, nil
			}
			news, err := deps.Portfolio.GetWeeklyNews(ctx, userID)
			if err != nil {
				return nil, err
			}
			return &core.ToolResult{
				Data: map[string]interface{}{"news": news},
			}, nil
		},
	))
}
