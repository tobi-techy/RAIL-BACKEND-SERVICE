package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// SetContextSignals wires active behavioral signals into ambient nudges.
func (o *Orchestrator) SetContextSignals(p ContextSignalProvider) {
	o.contextSignals = p
}

// GenerateEnhancedNudge uses multi-modal context (time, signals, spending patterns)
// to produce a richer, actionable nudge.
func (o *Orchestrator) GenerateEnhancedNudge(ctx context.Context, userID uuid.UUID, req entities.EnhancedNudgeRequest) (*entities.EnhancedNudgeResponse, error) {
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	daysInMonth := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	daysRemaining := maxInt(0, daysInMonth-now.Day())

	spend, stash, total := o.currentBalances(ctx, userID)
	flow := o.monthFlow(ctx, userID, monthStart, nextMonth)
	totalOut := flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P).Add(flow.TotalReceipts)

	var signals []entities.UserContextSignal
	if o.contextSignals != nil {
		if active, err := o.contextSignals.GetActiveByUser(ctx, userID); err == nil {
			signals = active
		} else if o.logger != nil {
			o.logger.Debug("enhanced nudge context signals unavailable", zap.Error(err), zap.String("user_id", userID.String()))
		}
	}

	// Budget info
	budgetStatus, budgetLimit, budgetRemaining := o.getBudgetContext(ctx, userID, totalOut)

	// Safe daily spend
	safeDailySpend := decimal.Zero
	if daysRemaining > 0 {
		if budgetRemaining.IsPositive() {
			safeDailySpend = budgetRemaining.Div(decimal.NewFromInt(int64(daysRemaining)))
		} else if spend.IsPositive() {
			safeDailySpend = spend.Div(decimal.NewFromInt(int64(daysRemaining)))
		}
	}

	// Build rich context
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Screen: %s\n", req.Screen))
	if req.Amount != "" {
		sb.WriteString(fmt.Sprintf("Transaction amount: %s %s\n", req.Amount, req.Currency))
	}
	sb.WriteString(fmt.Sprintf("Time of day: %s\n", req.TimeOfDay))
	sb.WriteString(fmt.Sprintf("Day of week: %d (0=Sun)\n", req.DayOfWeek))
	if req.DaysUntilPayday > 0 {
		sb.WriteString(fmt.Sprintf("Days until payday: %d\n", req.DaysUntilPayday))
	}
	if req.MerchantHint != "" {
		sb.WriteString(fmt.Sprintf("Merchant context: %s\n", req.MerchantHint))
	}
	sb.WriteString(fmt.Sprintf("Spending balance: $%s\n", spend.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("Stash balance: $%s\n", stash.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("Total balance: $%s\n", total.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("Month spent: $%s | Income: $%s\n", totalOut.StringFixed(2), flow.TotalDeposits.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("Days remaining: %d | Safe daily: $%s\n", daysRemaining, safeDailySpend.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("Budget: %s", budgetStatus))
	if budgetLimit.IsPositive() {
		sb.WriteString(fmt.Sprintf(" (limit: $%s, remaining: $%s)", budgetLimit.StringFixed(2), budgetRemaining.StringFixed(2)))
	}
	sb.WriteString("\n")

	// Add detected signals
	if len(signals) > 0 {
		sb.WriteString("\nDetected patterns:\n")
		for _, sig := range signals {
			sb.WriteString(fmt.Sprintf("- %s (confidence: %s): %s\n", sig.SignalType, sig.Confidence.StringFixed(2), string(sig.SignalData)))
		}
	}

	// Add recent actions context
	if len(req.RecentActions) > 0 {
		sb.WriteString(fmt.Sprintf("\nRecent user actions: %s\n", strings.Join(req.RecentActions, ", ")))
	}

	systemPrompt := `You are Miriam, a witty financial companion inside Rail Money. You give SHORT ambient nudges (1-2 sentences max) based on rich context about the user's financial state, time, and behavior patterns.

Rules:
- Be conversational, warm, and direct. Talk like a smart friend.
- Use context signals intelligently: if it's near payday, mention it. If spending is spiking, warn gently.
- If the user is about to overspend or is over budget, be blunt but caring.
- If things look good, celebrate briefly.
- NEVER give investment advice or mention specific assets.
- NEVER use emojis. Use plain text only.
- If you can suggest a specific action (like moving money to stash), include it.

Respond with ONLY a JSON object (no markdown):
{"show": true/false, "message": "your nudge", "severity": "info|warning|celebration", "shake": true/false, "action": {"type": "transfer|open_screen|confirm", "label": "button text", "destination": "stash|spend|goals|budget"} | null, "expires_in": 15}

Set show=false if nothing useful to say. Set action=null if no action needed.
Set shake=true ONLY for genuine warnings. expires_in is seconds before auto-dismiss (8-30).`

	temp := infraai.Float64(0.7)
	resp, err := o.aiProvider.ChatCompletion(ctx, &infraai.ChatRequest{
		SystemPrompt: systemPrompt,
		Messages:     []infraai.Message{{Role: "user", Content: sb.String()}},
		MaxTokens:    200,
		Temperature:  temp,
		UserID:       userID.String(),
	})
	if err != nil {
		o.logger.Warn("enhanced nudge LLM failed", zap.Error(err))
		return &entities.EnhancedNudgeResponse{Show: false, Severity: "info"}, nil
	}

	return parseEnhancedNudgeResponse(resp.Content), nil
}

func parseEnhancedNudgeResponse(raw string) *entities.EnhancedNudgeResponse {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var nr entities.EnhancedNudgeResponse
	if err := json.Unmarshal([]byte(raw), &nr); err != nil {
		if len(raw) > 0 && len(raw) < 200 {
			return &entities.EnhancedNudgeResponse{Show: true, Message: raw, Severity: "info", ExpiresIn: 15}
		}
		return &entities.EnhancedNudgeResponse{Show: false, Severity: "info"}
	}
	switch nr.Severity {
	case "warning", "celebration", "info":
	default:
		nr.Severity = "info"
	}
	if nr.ExpiresIn == 0 {
		nr.ExpiresIn = 15
	}
	return &nr
}

func (o *Orchestrator) getBudgetContext(ctx context.Context, userID uuid.UUID, totalOut decimal.Decimal) (string, decimal.Decimal, decimal.Decimal) {
	budgetStatus := "not_set"
	var budgetLimit, budgetRemaining decimal.Decimal
	if o.budgetProvider != nil {
		if b, err := o.budgetProvider.GetByUserID(ctx, userID); err == nil && b != nil && b.MonthlyLimit.IsPositive() {
			budgetLimit = b.MonthlyLimit
			budgetRemaining = b.MonthlyLimit.Sub(totalOut)
			if budgetRemaining.IsNegative() {
				budgetStatus = "over_budget"
			} else {
				pct := totalOut.Div(b.MonthlyLimit).Mul(decimal.NewFromInt(100))
				if pct.GreaterThan(decimal.NewFromInt(90)) {
					budgetStatus = "near_limit"
				} else if pct.GreaterThan(decimal.NewFromInt(70)) {
					budgetStatus = "tight"
				} else {
					budgetStatus = "on_track"
				}
			}
		}
	}
	return budgetStatus, budgetLimit, budgetRemaining
}
