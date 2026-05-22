package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// nudgeCooldowns tracks per-user per-screen nudge timestamps for deduplication.
var nudgeCooldowns sync.Map

const nudgeCooldownTTL = 15 * time.Minute

func init() {
	go func() {
		for range time.NewTicker(5 * time.Minute).C {
			now := time.Now()
			nudgeCooldowns.Range(func(key, val interface{}) bool {
				if now.Sub(val.(time.Time)) > nudgeCooldownTTL {
					nudgeCooldowns.Delete(key)
				}
				return true
			})
		}
	}()
}

// NudgeRequest describes the screen context for an ambient nudge.
type NudgeRequest struct {
	Screen   string `json:"screen"`             // "home", "withdraw", "send", "stash"
	Amount   string `json:"amount,omitempty"`    // transaction amount being entered
	Currency string `json:"currency,omitempty"`  // e.g. "USDC"
}

// NudgeResponse is the lightweight nudge returned to the client.
type NudgeResponse struct {
	Show     bool   `json:"show"`
	Message  string `json:"message,omitempty"`
	Severity string `json:"severity"` // "info", "warning", "celebration"
	Shake    bool   `json:"shake"`    // trigger device shake haptic
}

// GenerateNudge gathers financial context and asks the LLM for a short,
// conversational nudge. It is designed to be fast (<3s) and cheap (small prompt).
func (o *Orchestrator) GenerateNudge(ctx context.Context, userID uuid.UUID, req NudgeRequest) (*NudgeResponse, error) {
	// Deduplication: skip if same user+screen was nudged within cooldown window
	cooldownKey := fmt.Sprintf("nudge:%s:%s", userID.String(), req.Screen)
	if lastTime, ok := nudgeCooldowns.Load(cooldownKey); ok {
		if time.Since(lastTime.(time.Time)) < nudgeCooldownTTL {
			return &NudgeResponse{Show: false, Severity: "info"}, nil
		}
	}

	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	daysElapsed := maxInt(1, now.Day())
	daysInMonth := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	daysRemaining := maxInt(0, daysInMonth-daysElapsed)

	spend, stash, total := o.currentBalances(ctx, userID)
	flow := o.monthFlow(ctx, userID, monthStart, nextMonth)
	totalOut := flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P).Add(flow.TotalReceipts)

	// Budget info
	budgetStatus := "not_set"
	var budgetLimit, budgetRemaining decimal.Decimal
	if o.budgetProvider != nil {
		if b, err := o.budgetProvider.GetByUserID(ctx, userID); err == nil && b != nil && b.MonthlyLimit.IsPositive() {
			budgetLimit = b.MonthlyLimit
			budgetRemaining = b.MonthlyLimit.Sub(totalOut)
			if budgetRemaining.IsNegative() {
				budgetStatus = "over_budget"
			} else if b.MonthlyLimit.GreaterThan(decimal.Zero) {
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

	// Safe daily spend
	safeDailySpend := decimal.Zero
	if daysRemaining > 0 {
		if budgetRemaining.IsPositive() {
			safeDailySpend = budgetRemaining.Div(decimal.NewFromInt(int64(daysRemaining)))
		} else if spend.IsPositive() {
			safeDailySpend = spend.Div(decimal.NewFromInt(int64(daysRemaining)))
		}
	}

	// Build context block
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Screen: %s\n", req.Screen))
	if req.Amount != "" {
		sb.WriteString(fmt.Sprintf("Transaction amount: %s %s\n", req.Amount, req.Currency))
	}
	sb.WriteString(fmt.Sprintf("Spending balance: $%s\n", spend.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("Stash balance: $%s\n", stash.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("Total balance: $%s\n", total.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("Month spent so far: $%s\n", totalOut.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("Month income so far: $%s\n", flow.TotalDeposits.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("Days remaining in month: %d\n", daysRemaining))
	sb.WriteString(fmt.Sprintf("Safe daily spend: $%s\n", safeDailySpend.StringFixed(2)))
	sb.WriteString(fmt.Sprintf("Budget status: %s\n", budgetStatus))
	if budgetLimit.IsPositive() {
		sb.WriteString(fmt.Sprintf("Budget limit: $%s, remaining: $%s\n", budgetLimit.StringFixed(2), budgetRemaining.StringFixed(2)))
	}
	if ms := nearMilestone(stash); ms != "" {
		sb.WriteString(fmt.Sprintf("Near milestone: stash is close to %s\n", ms))
	}

	systemPrompt := `You are Miriam, a witty financial companion inside the Rail Money app. You give SHORT ambient nudges (1-2 sentences max) based on the user's financial context.

Rules:
- Be conversational, warm, and direct. Talk like a smart friend, not a bank.
- Use casual language. Examples: "Yo, that's a big one", "Nice move!", "Chill on the spending bro"
- If the user is about to overspend or is over budget, be blunt but caring.
- If things look good, celebrate briefly.
- NEVER give investment advice or mention specific assets.
- NEVER use emojis. Use plain text only.

Respond with ONLY a JSON object (no markdown, no code fences):
{"show": true/false, "message": "your nudge", "severity": "info|warning|celebration", "shake": true/false}

Set show=false if there's nothing useful to say for this screen.
Set shake=true ONLY for genuine warnings (over budget, spending > 50% of balance, etc).`

	userMsg := sb.String()

	temp := infraai.Float64(0.7)
	resp, err := o.aiProvider.ChatCompletion(ctx, &infraai.ChatRequest{
		SystemPrompt: systemPrompt,
		Messages:     []infraai.Message{{Role: "user", Content: userMsg}},
		MaxTokens:    150,
		Temperature:  temp,
		UserID:       userID.String(),
	})
	if err != nil {
		o.logger.Warn("nudge LLM call failed", zap.Error(err), zap.String("user_id", userID.String()))
		return &NudgeResponse{Show: false, Severity: "info"}, nil
	}

	result := parseNudgeResponse(resp.Content)
	if result.Show {
		nudgeCooldowns.Store(cooldownKey, time.Now())
	}
	return result, nil
}

// parseNudgeResponse extracts the JSON nudge from the LLM output.
func parseNudgeResponse(raw string) *NudgeResponse {
	raw = strings.TrimSpace(raw)
	// Strip markdown code fences if the LLM wraps it
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var nr NudgeResponse
	if err := json.Unmarshal([]byte(raw), &nr); err != nil {
		// Fallback: treat the whole thing as a message
		if len(raw) > 0 && len(raw) < 200 {
			return &NudgeResponse{Show: true, Message: raw, Severity: "info"}
		}
		return &NudgeResponse{Show: false, Severity: "info"}
	}
	// Sanitize severity
	switch nr.Severity {
	case "warning", "celebration", "info":
	default:
		nr.Severity = "info"
	}
	return &nr
}

// nearMilestone returns the milestone label if balance is within 10% below it.
func nearMilestone(balance decimal.Decimal) string {
	milestones := []int64{10000, 5000, 2500, 1000, 500, 250, 100}
	val := balance.IntPart()
	for _, m := range milestones {
		threshold := m - m/10 // within 10% below
		if val >= threshold && val < m {
			return fmt.Sprintf("$%d", m)
		}
	}
	return ""
}
