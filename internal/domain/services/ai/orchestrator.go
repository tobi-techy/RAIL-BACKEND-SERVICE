package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"go.uber.org/zap"
)

// Tool names
const (
	ToolGetPortfolioStats      = "get_portfolio_stats"
	ToolGetTopMovers           = "get_top_movers"
	ToolGetAllocations         = "get_allocations"
	ToolGetContributions       = "get_contributions"
	ToolGetWeeklyNews          = "get_weekly_news"
	ToolGetBasketRecommendations = "get_basket_recommendations"
	ToolGetStreak              = "get_streak"
)

// PortfolioDataProvider interface for portfolio data
type PortfolioDataProvider interface {
	GetWeeklyStats(ctx context.Context, userID uuid.UUID) (*PortfolioStats, error)
	GetTopMovers(ctx context.Context, userID uuid.UUID, limit int) ([]*Mover, error)
	GetAllocations(ctx context.Context, userID uuid.UUID) ([]*Allocation, error)
}

// ActivityDataProvider interface for activity data
type ActivityDataProvider interface {
	GetContributions(ctx context.Context, userID uuid.UUID, contributionType string, startDate, endDate time.Time) (*ContributionSummary, error)
	GetStreak(ctx context.Context, userID uuid.UUID) (*entities.InvestmentStreak, error)
}

// NewsDataProvider interface for news data
type NewsDataProvider interface {
	GetWeeklyNews(ctx context.Context, userID uuid.UUID) ([]*entities.UserNews, error)
}

// PortfolioStats represents weekly portfolio statistics
type PortfolioStats struct {
	TotalValue      decimal.Decimal `json:"total_value"`
	WeeklyReturn    decimal.Decimal `json:"weekly_return"`
	WeeklyReturnPct decimal.Decimal `json:"weekly_return_pct"`
	MonthlyReturn   decimal.Decimal `json:"monthly_return"`
	TotalGainLoss   decimal.Decimal `json:"total_gain_loss"`
}

// Mover represents a top gainer/loser
type Mover struct {
	Symbol    string          `json:"symbol"`
	Name      string          `json:"name"`
	Return    decimal.Decimal `json:"return"`
	ReturnPct decimal.Decimal `json:"return_pct"`
}

// Allocation represents portfolio allocation
type Allocation struct {
	BasketID   uuid.UUID       `json:"basket_id"`
	BasketName string          `json:"basket_name"`
	Value      decimal.Decimal `json:"value"`
	Weight     decimal.Decimal `json:"weight"`
}

// ContributionSummary represents contribution totals
type ContributionSummary struct {
	Deposits  decimal.Decimal `json:"deposits"`
	Roundups  decimal.Decimal `json:"roundups"`
	Cashback  decimal.Decimal `json:"cashback"`
	Total     decimal.Decimal `json:"total"`
}

// Orchestrator handles AI interactions with tool calling
type Orchestrator struct {
	aiProvider        ai.AIProvider
	portfolioProvider PortfolioDataProvider
	activityProvider  ActivityDataProvider
	newsProvider      NewsDataProvider
	logger            *zap.Logger
}

// NewOrchestrator creates a new AI orchestrator
func NewOrchestrator(
	aiProvider ai.AIProvider,
	portfolioProvider PortfolioDataProvider,
	activityProvider ActivityDataProvider,
	newsProvider NewsDataProvider,
	logger *zap.Logger,
) *Orchestrator {
	return &Orchestrator{
		aiProvider:        aiProvider,
		portfolioProvider: portfolioProvider,
		activityProvider:  activityProvider,
		newsProvider:      newsProvider,
		logger:            logger,
	}
}

// SystemPrompt for the AI Financial Manager
const SystemPrompt = `You are the RAIL Financial Manager - a friendly, Gen Z-native AI assistant.

Behavior Rules:
- Speak in short, punchy Spotify-Wrapped style
- Use emojis sparingly (1-2 per message max)
- Never invent numbers - only use data from tools
- NEVER give financial advice (no "you should buy/sell")
- Instead say "you might consider" or "some investors..."
- Keep responses under 200 words unless detailed analysis requested
- Be encouraging but realistic about performance`

// GetTools returns available tools for the AI
func (o *Orchestrator) GetTools() []ai.Tool {
	return []ai.Tool{
		{
			Name:        ToolGetPortfolioStats,
			Description: "Get current portfolio statistics including total value and returns",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"period": map[string]interface{}{"type": "string", "enum": []string{"1w", "1m", "3m", "1y"}},
				},
			},
		},
		{
			Name:        ToolGetTopMovers,
			Description: "Get biggest gainers and losers in the portfolio",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"limit": map[string]interface{}{"type": "integer", "default": 5},
				},
			},
		},
		{
			Name:        ToolGetAllocations,
			Description: "Get current portfolio allocation by basket",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			Name:        ToolGetContributions,
			Description: "Get user contributions (deposits, round-ups, cashback)",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type":   map[string]interface{}{"type": "string", "enum": []string{"all", "deposit", "roundup", "cashback"}},
					"period": map[string]interface{}{"type": "string", "enum": []string{"1w", "1m", "3m"}},
				},
			},
		},
		{
			Name:        ToolGetWeeklyNews,
			Description: "Get relevant news for user holdings this week",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			Name:        ToolGetStreak,
			Description: "Get user's investment streak information",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
	}
}

// Chat handles a chat message with tool calling
func (o *Orchestrator) Chat(ctx context.Context, userID uuid.UUID, message string, history []ai.Message) (*ChatResponse, error) {
	// Build messages with history
	messages := append(history, ai.Message{Role: "user", Content: message})

	// Initial request
	req := &ai.ChatRequest{
		Messages:     messages,
		SystemPrompt: SystemPrompt,
		MaxTokens:    500,
		Temperature:  0.7,
	}

	// Get response with tools
	resp, err := o.aiProvider.ChatCompletionWithTools(ctx, req, o.GetTools())
	if err != nil {
		return nil, fmt.Errorf("AI completion failed: %w", err)
	}

	// Process tool calls if any
	toolResults := make([]ToolResult, 0)
	if len(resp.ToolCalls) > 0 {
		for _, tc := range resp.ToolCalls {
			result, err := o.executeTool(ctx, userID, tc)
			if err != nil {
				o.logger.Warn("Tool execution failed", zap.String("tool", tc.Name), zap.Error(err))
				result = map[string]interface{}{"error": err.Error()}
			}
			toolResults = append(toolResults, ToolResult{Name: tc.Name, Result: result})
		}

		// Make follow-up request with tool results
		toolResultsJSON, _ := json.Marshal(toolResults)
		messages = append(messages, ai.Message{Role: "assistant", Content: resp.Content})
		messages = append(messages, ai.Message{Role: "user", Content: fmt.Sprintf("Tool results: %s", string(toolResultsJSON))})

		req.Messages = messages
		resp, err = o.aiProvider.ChatCompletion(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("follow-up completion failed: %w", err)
		}
	}

	// Apply safety filter
	content := o.applySafetyFilter(resp.Content)

	return &ChatResponse{
		Content:     content,
		ToolCalls:   toolResults,
		TokensUsed:  resp.TokensUsed,
		Provider:    resp.Provider,
	}, nil
}

// executeTool executes a tool call and returns the result
func (o *Orchestrator) executeTool(ctx context.Context, userID uuid.UUID, tc ai.ToolCall) (map[string]interface{}, error) {
	switch tc.Name {
	case ToolGetPortfolioStats:
		stats, err := o.portfolioProvider.GetWeeklyStats(ctx, userID)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"total_value":       stats.TotalValue.String(),
			"weekly_return":     stats.WeeklyReturn.String(),
			"weekly_return_pct": stats.WeeklyReturnPct.String(),
		}, nil

	case ToolGetTopMovers:
		limit := 5
		if l, ok := tc.Arguments["limit"].(float64); ok {
			limit = int(l)
		}
		movers, err := o.portfolioProvider.GetTopMovers(ctx, userID, limit)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"movers": movers}, nil

	case ToolGetAllocations:
		allocs, err := o.portfolioProvider.GetAllocations(ctx, userID)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"allocations": allocs}, nil

	case ToolGetContributions:
		now := time.Now()
		startDate := now.AddDate(0, 0, -7)
		summary, err := o.activityProvider.GetContributions(ctx, userID, "all", startDate, now)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"deposits": summary.Deposits.String(),
			"roundups": summary.Roundups.String(),
			"cashback": summary.Cashback.String(),
			"total":    summary.Total.String(),
		}, nil

	case ToolGetWeeklyNews:
		news, err := o.newsProvider.GetWeeklyNews(ctx, userID)
		if err != nil {
			return nil, err
		}
		headlines := make([]string, 0, len(news))
		for _, n := range news {
			headlines = append(headlines, n.Title)
		}
		return map[string]interface{}{"headlines": headlines, "count": len(news)}, nil

	case ToolGetStreak:
		streak, err := o.activityProvider.GetStreak(ctx, userID)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"current_streak": streak.CurrentStreak,
			"longest_streak": streak.LongestStreak,
		}, nil

	default:
		return nil, fmt.Errorf("unknown tool: %s", tc.Name)
	}
}

// applySafetyFilter removes financial advice from responses
func (o *Orchestrator) applySafetyFilter(content string) string {
	// Patterns that indicate financial advice
	advicePatterns := []string{
		`(?i)you should (buy|sell|invest)`,
		`(?i)i recommend (buying|selling)`,
		`(?i)definitely (buy|sell)`,
	}

	for _, pattern := range advicePatterns {
		re := regexp.MustCompile(pattern)
		if re.MatchString(content) {
			o.logger.Warn("Safety filter triggered", zap.String("pattern", pattern))
			content = re.ReplaceAllString(content, "some investors might consider")
		}
	}

	return content
}

// GenerateWrappedCards generates Spotify-Wrapped style cards
func (o *Orchestrator) GenerateWrappedCards(ctx context.Context, userID uuid.UUID) ([]entities.WrappedCard, error) {
	cards := make([]entities.WrappedCard, 0)

	// Get portfolio stats
	stats, err := o.portfolioProvider.GetWeeklyStats(ctx, userID)
	if err == nil {
		returnPct := stats.WeeklyReturnPct.Mul(decimal.NewFromInt(100))
		emoji := "📈"
		if returnPct.LessThan(decimal.Zero) {
			emoji = "📉"
		}
		cards = append(cards, entities.WrappedCard{
			Type:    "performance_headline",
			Title:   "This Week's Vibe",
			Content: fmt.Sprintf("You're %s%.2f%% this week %s", getSign(returnPct), returnPct.Abs().InexactFloat64(), emoji),
			Data:    map[string]interface{}{"weekly_return": returnPct.String()},
		})
	}

	// Get top mover
	movers, err := o.portfolioProvider.GetTopMovers(ctx, userID, 1)
	if err == nil && len(movers) > 0 {
		top := movers[0]
		cards = append(cards, entities.WrappedCard{
			Type:    "top_mover",
			Title:   "Your MVP Stock",
			Content: fmt.Sprintf("%s carried the team with %s%.1f%%", top.Symbol, getSign(top.ReturnPct), top.ReturnPct.Abs().InexactFloat64()),
			Data:    map[string]interface{}{"symbol": top.Symbol, "return": top.ReturnPct.String()},
		})
	}

	// Get contributions
	now := time.Now()
	contributions, err := o.activityProvider.GetContributions(ctx, userID, "all", now.AddDate(0, 0, -7), now)
	if err == nil {
		cards = append(cards, entities.WrappedCard{
			Type:    "contributions",
			Title:   "Money Moves",
			Content: fmt.Sprintf("$%s in deposits this week", contributions.Deposits.StringFixed(0)),
			Data:    map[string]interface{}{"deposits": contributions.Deposits.String(), "total": contributions.Total.String()},
		})
	}

	// Get streak
	streak, err := o.activityProvider.GetStreak(ctx, userID)
	if err == nil && streak.CurrentStreak > 0 {
		cards = append(cards, entities.WrappedCard{
			Type:    "streak",
			Title:   "On Fire 🔥",
			Content: fmt.Sprintf("%d day investing streak!", streak.CurrentStreak),
			Data:    map[string]interface{}{"current_streak": streak.CurrentStreak, "longest_streak": streak.LongestStreak},
		})
	}

	// Get news count
	news, err := o.newsProvider.GetWeeklyNews(ctx, userID)
	if err == nil && len(news) > 0 {
		cards = append(cards, entities.WrappedCard{
			Type:    "news",
			Title:   "What's Happening",
			Content: fmt.Sprintf("%d updates on your holdings", len(news)),
			Data:    map[string]interface{}{"count": len(news)},
		})
	}

	return cards, nil
}

func getSign(d decimal.Decimal) string {
	if d.GreaterThanOrEqual(decimal.Zero) {
		return "+"
	}
	return ""
}

// ChatResponse represents the response from a chat interaction
type ChatResponse struct {
	Content    string       `json:"content"`
	ToolCalls  []ToolResult `json:"tool_calls,omitempty"`
	TokensUsed int          `json:"tokens_used"`
	Provider   string       `json:"provider"`
}

// ToolResult represents the result of a tool execution
type ToolResult struct {
	Name   string                 `json:"name"`
	Result map[string]interface{} `json:"result"`
}
