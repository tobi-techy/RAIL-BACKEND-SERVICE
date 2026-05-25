package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// FullUserProfileProvider extends UserProfileProvider with full profile access.
type FullUserProfileProvider interface {
	GetProfile(ctx context.Context, userID uuid.UUID) (*entities.UserProfile, error)
}

func (o *Orchestrator) buildUserProfileContext(ctx context.Context, userID uuid.UUID) string {
	provider, ok := o.userProfile.(FullUserProfileProvider)
	if !ok || provider == nil {
		return ""
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	user, err := provider.GetProfile(fetchCtx, userID)
	if err != nil || user == nil {
		return ""
	}

	name := strings.TrimSpace(strings.Join(compactStrings(stringPtrValue(user.FirstName), stringPtrValue(user.LastName)), " "))
	country := firstNonEmpty(stringPtrValue(user.AddressCountry), stringPtrValue(user.Country))
	city := stringPtrValue(user.AddressCity)
	state := stringPtrValue(user.AddressState)
	localeStyle := inferLocaleStyle(country, city)

	var age string
	if user.DateOfBirth != nil {
		years := ageYears(*user.DateOfBirth, time.Now().UTC())
		if years >= 18 && years <= 120 {
			age = fmt.Sprintf("%d", years)
		}
	}

	parts := []string{
		fmt.Sprintf("locale style: %s", localeStyle),
	}
	if name != "" {
		parts = append(parts, fmt.Sprintf("name: %s", name))
	}
	if user.RailTag != nil && strings.TrimSpace(*user.RailTag) != "" {
		parts = append(parts, fmt.Sprintf("Rail tag: %s", strings.TrimSpace(*user.RailTag)))
	}
	if city != "" {
		parts = append(parts, fmt.Sprintf("city: %s", city))
	}
	if state != "" {
		parts = append(parts, fmt.Sprintf("state/region: %s", state))
	}
	if country != "" {
		parts = append(parts, fmt.Sprintf("country: %s", strings.ToUpper(country)))
	}
	if age != "" {
		parts = append(parts, fmt.Sprintf("age: %s", age))
	}

	return "[App profile context — " + strings.Join(parts, " | ") + ". Use this for personalization only when relevant. Do not mention private fields unnecessarily. Do not invent culture, job, income, or family details.]"
}

// BuildRealtimeGreeting returns the short spoken opener AssemblyAI uses when
// the voice session starts. Keep it calm, contextual, and privacy-aware.
func (o *Orchestrator) BuildRealtimeGreeting(ctx context.Context, userID uuid.UUID) string {
	name := o.realtimeFirstName(ctx, userID)

	loc, _, _ := o.resolveMiriamLocation(ctx, userID, nil)
	if loc == nil {
		loc = time.Local
	}
	hour := time.Now().In(loc).Hour()
	var greeting string
	switch {
	case hour >= 5 && hour < 12:
		if name != "" {
			greeting = "Morning " + name + ". Miriam."
		} else {
			greeting = "Morning. Miriam."
		}
	case hour >= 12 && hour < 17:
		if name != "" {
			greeting = "Hey " + name + ". Miriam here."
		} else {
			greeting = "Hey. Miriam here."
		}
	case hour >= 17 && hour < 21:
		if name != "" {
			greeting = name + ". Miriam."
		} else {
			greeting = "Miriam."
		}
	default:
		if name != "" {
			greeting = "Late one, " + name + ". Miriam."
		} else {
			greeting = "Late one. Miriam."
		}
	}

	insight := o.realtimeProactiveInsight(ctx, userID)
	if insight != "" {
		return greeting + " " + insight
	}
	return greeting + " What money move are we making?"
}

// realtimeProactiveInsight builds a short, actionable opener based on real account state.
// Priority: balance info > spending spike > failed txns > generic.
func (o *Orchestrator) realtimeProactiveInsight(ctx context.Context, userID uuid.UUID) string {
	fetchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	// 1. Balance context (most useful, always fresh)
	if o.aggregateStats != nil {
		stash, err := o.aggregateStats.GetAccountBalance(fetchCtx, userID, entities.AccountTypeStashBalance)
		if err == nil && !stash.IsZero() {
			spend, _ := o.aggregateStats.GetAccountBalance(fetchCtx, userID, entities.AccountTypeSpendingBalance)
			return fmt.Sprintf("Spend is %s, stash is %s.",
				formatBalanceShort(spend), formatBalanceShort(stash))
		}
	}

	// 2. Spending spike (interesting insight)
	if o.spending != nil {
		now := time.Now().UTC()
		weekStart := now.AddDate(0, 0, -7)
		prevWeekStart := now.AddDate(0, 0, -14)
		thisWeek, err1 := o.spending.GetSummary(fetchCtx, userID, weekStart, now)
		lastWeek, err2 := o.spending.GetSummary(fetchCtx, userID, prevWeekStart, weekStart)
		if err1 == nil && err2 == nil && thisWeek != nil && lastWeek != nil {
			if !lastWeek.Total.IsZero() && thisWeek.Total.GreaterThan(lastWeek.Total.Mul(onePointFive)) {
				pct := thisWeek.Total.Sub(lastWeek.Total).Div(lastWeek.Total).Mul(hundred).IntPart()
				return fmt.Sprintf("Spending jumped about %d percent this week.", pct)
			}
		}
	}

	// 3. Failed withdrawal (only mention once, not every time)
	if o.withdrawalHistory != nil {
		if withdrawals, err := o.withdrawalHistory.GetByUserID(fetchCtx, userID, 3, 0); err == nil {
			for _, w := range withdrawals {
				if w.Status == entities.WithdrawalStatusFailed && time.Since(w.CreatedAt) < 24*time.Hour {
					return "Something needs your attention with a recent withdrawal."
				}
			}
		}
	}

	return ""
}

func nearestMilestone(total decimal.Decimal) string {
	milestones := []int64{10000, 5000, 2000, 1000, 500, 100}
	val := total.IntPart()
	for _, m := range milestones {
		if val >= m && val < m+m/10 { // within 10% above milestone
			return fmt.Sprintf("$%d", m)
		}
	}
	return ""
}

func formatBalanceShort(d decimal.Decimal) string {
	val := d.IntPart()
	if val >= 1000 {
		return fmt.Sprintf("about $%d", val)
	}
	return fmt.Sprintf("$%d", val)
}

var onePointFive = decimal.NewFromFloat(1.5)
var hundred = decimal.NewFromInt(100)

func (o *Orchestrator) realtimeFirstName(ctx context.Context, userID uuid.UUID) string {
	provider, ok := o.userProfile.(FullUserProfileProvider)
	if !ok || provider == nil {
		return ""
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	user, err := provider.GetProfile(fetchCtx, userID)
	if err != nil || user == nil {
		return ""
	}
	firstName := stringPtrValue(user.FirstName)
	if firstName == "" {
		return ""
	}
	fields := strings.Fields(firstName)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func (o *Orchestrator) realtimeHasBalanceContext(ctx context.Context, userID uuid.UUID) bool {
	if o.aggregateStats == nil {
		return false
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if _, err := o.aggregateStats.GetAccountBalance(fetchCtx, userID, entities.AccountTypeSpendingBalance); err == nil {
		return true
	}
	if _, err := o.aggregateStats.GetAccountBalance(fetchCtx, userID, entities.AccountTypeStashBalance); err == nil {
		return true
	}
	return false
}

// BuildRealtimeInstructions returns Miriam's voice prompt with the same personal context
// used by text chat. It is best-effort: missing context is skipped.
func (o *Orchestrator) BuildRealtimeInstructions(ctx context.Context, userID uuid.UUID) string {
	parts := []string{SystemPrompt}
	if balanceCtx := o.buildBalanceContext(ctx, userID); balanceCtx != "" {
		parts = append(parts, balanceCtx)
	}
	if stashLockCtx := o.buildStashLockContext(ctx, userID); stashLockCtx != "" {
		parts = append(parts, stashLockCtx)
	}
	if yearCtx := o.buildYearFinancialContext(ctx, userID); yearCtx != "" {
		parts = append(parts, yearCtx)
	}
	if timeCtx := o.buildUserTimeContext(ctx, userID); timeCtx != "" {
		parts = append(parts, timeCtx)
	}
	if profileCtx := o.buildUserProfileContext(ctx, userID); profileCtx != "" {
		parts = append(parts, profileCtx)
	}
	parts = append(parts, premiumRealtimeVoiceInstructions)

	return strings.Join(parts, "\n\n")
}

// buildYearFinancialContext fetches the last 12 months of money flow data and returns
// a grounded financial snapshot string. This prevents Miriam from hallucinating numbers
// when asked about historical spending, deposits, or totals.
func (o *Orchestrator) buildYearFinancialContext(ctx context.Context, userID uuid.UUID) string {
	if o.spending == nil {
		return ""
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	now := time.Now().UTC()
	yearStart := now.AddDate(-1, 0, 0)

	flow, err := o.spending.GetMoneyFlow(fetchCtx, userID, yearStart, now)
	if err != nil || flow == nil {
		return ""
	}

	totalOut := flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P).Add(flow.TotalReceipts)
	net := flow.TotalDeposits.Sub(totalOut)

	var parts []string
	parts = append(parts, fmt.Sprintf("period: %s to %s", yearStart.Format("Jan 2006"), now.Format("Jan 2006")))
	parts = append(parts, fmt.Sprintf("total deposited: $%s (%d deposits)", flow.TotalDeposits.StringFixed(2), flow.DepositCount))
	parts = append(parts, fmt.Sprintf("total spent: $%s (card: $%s | withdrawals: $%s | p2p: $%s)",
		totalOut.StringFixed(2),
		flow.TotalCardSpend.StringFixed(2),
		flow.TotalWithdrawals.StringFixed(2),
		flow.TotalP2P.StringFixed(2),
	))
	parts = append(parts, fmt.Sprintf("net flow: $%s", net.StringFixed(2)))

	// Per-month breakdown for the last 12 months
	var monthLines []string
	for i := 11; i >= 0; i-- {
		mStart := time.Date(now.Year(), now.Month()-time.Month(i), 1, 0, 0, 0, 0, time.UTC)
		mEnd := mStart.AddDate(0, 1, 0)
		if mEnd.After(now) {
			mEnd = now
		}
		mFlow, err := o.spending.GetMoneyFlow(fetchCtx, userID, mStart, mEnd)
		if err != nil || mFlow == nil {
			continue
		}
		mOut := mFlow.TotalWithdrawals.Add(mFlow.TotalCardSpend).Add(mFlow.TotalP2P).Add(mFlow.TotalReceipts)
		if mFlow.TotalDeposits.IsZero() && mOut.IsZero() {
			continue
		}
		monthLines = append(monthLines, fmt.Sprintf("%s: in $%s out $%s",
			mStart.Format("Jan 2006"),
			mFlow.TotalDeposits.StringFixed(2),
			mOut.StringFixed(2),
		))
	}
	if len(monthLines) > 0 {
		parts = append(parts, "monthly breakdown: "+strings.Join(monthLines, " | "))
	}

	return "[VERIFIED FINANCIAL HISTORY — these are exact figures from the ledger. Use them directly when asked. Do NOT invent or adjust any number here.]\n" +
		strings.Join(parts, "\n")
}

// buildRecentConversationContext pulls the last 3 conversation summaries
// so Miriam can reference what was discussed recently.
func (o *Orchestrator) buildRecentConversationContext(ctx context.Context, userID uuid.UUID) string {
	provider, ok := o.conversations.(RecentConversationLister)
	if !ok || provider == nil {
		return ""
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	convs, err := provider.ListByUserID(fetchCtx, userID, 3, 0)
	if err != nil || len(convs) == 0 {
		return ""
	}

	var summaries []string
	for _, c := range convs {
		if c.SummaryContext != "" {
			summaries = append(summaries, fmt.Sprintf("- %s: %s", c.CreatedAt.Format("Jan 2"), c.SummaryContext))
		} else if c.Title != "" {
			summaries = append(summaries, fmt.Sprintf("- %s: %s", c.CreatedAt.Format("Jan 2"), c.Title))
		}
	}
	if len(summaries) == 0 {
		return ""
	}
	return "[RECENT CONVERSATIONS — reference naturally if relevant, never list back.]\n" + strings.Join(summaries, "\n")
}

// RecentConversationLister is optionally implemented by the conversation persister.
type RecentConversationLister interface {
	ListByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.AIConversation, error)
}

const premiumRealtimeVoiceInstructions = `YOU MUST CALL TOOLS TO EXECUTE ACTIONS. This is the most important rule. You cannot move money, create anything, or change anything by just saying "done". You must call the tool and wait for its result before confirming to the user.

IDENTITY:
You are Miriam — a calm, sharp financial voice on a private call. Not a chatbot, not a narrator. You're having a real conversation with someone whose money you already know.

TONE:
Have opinions. Be dry when something is obviously bad. Celebrate small wins. Match the user's energy — clipped responses get clipped replies. Deep questions get depth. You don't hedge.

RESPONSE LENGTH:
- Keep every response under two spoken sentences unless asked for detail.
- If your reply has a comma, ask yourself if it could stop at the comma.
- If your reply is more than 15 words, shorten it.

EXAMPLE RESPONSES (match this length):
- "Spend is four twelve. Stash is seven thirty-five. You're fine."
- "Done. Thirty bucks moved to stash. New balance is seven sixty-five."
- "Can't do that — rent is too close."

MIRIAM VOICE MODE:
You are a paid, live money operator. Never guess account data. Default to 1-2 spoken sentences. Never end with "Is there anything else I can help with?"

TOOL USAGE — CRITICAL:
VOICE TOOL OVERRIDE:
For any read-only question, call the tool DIRECTLY by its name. Do NOT wrap it in voice_money_lookup.
Examples: call get_account_summary directly, call get_budget directly, call get_money_flow directly.
Use voice_money_lookup ONLY for tools that are NOT exposed directly (e.g. search_knowledge_base, get_financial_timeline, get_persona_money_context, get_money_operating_plan, get_financial_advice, get_financial_plan, get_cash_flow_forecast, get_financial_audit, get_financial_health, get_financial_timeline).
For actions, use the direct action tool if it is exposed. If not, call voice_money_action with action set to the underlying chat action and params set to that action's arguments.

Direct tools exposed to voice (call these by name):
- get_account_summary — balances and overview
- get_money_flow — where money went
- get_budget — monthly limit and remaining
- get_miriam_brief — "what changed" / "what matters"
- get_miriam_money_state — safe to spend, runway, anomalies
- list_miriam_mandates — what Miriam can do automatically
- get_miriam_decision_receipts — what Miriam did quietly
- get_spending_summary, get_spending_chart, get_recent_transactions
- get_deposit_history, get_withdrawal_history, get_receipt_history
- get_income_trend, get_yield_earned, get_recurring_expenses
- get_linked_banks, get_subscriptions, get_runway, get_deposit_pattern
- get_yield_summary, get_spending_comparison, get_savings_goals
- get_action_receipts, get_savings_suggestions, get_spending_patterns
- get_comparative_context, get_merchant_insights, get_price_changes
- get_portfolio_stats, get_top_movers, get_allocations, get_contributions
- get_weekly_news, get_streak, get_balance_history, get_tax_summary
- get_tax_calendar, get_list_automations, get_linked_banks
- list_memory, list_financial_obligations, find_obligation_payments
- suggest_smart_timing, suggest_adaptive_amount, get_warranty_items
- get_receipt_challenges

Router tools:
- voice_money_lookup — for tools NOT listed above
- voice_money_action — for actions NOT exposed directly

Things you CANNOT do:
- Anything not listed above
- Pretend an action happened without calling the tool

WHEN USER ASKS FOR AN ACTION:
Bad: "Done! I've moved the money." (without calling transfer_funds)
Good: Call transfer_funds with the right parameters, wait for result, then say "Done. New spend is X, stash is Y."

FEW-SHOT EXAMPLES (follow this pattern exactly):
User: "Move point two to stash" → You: "Moving that now." [call transfer_funds {from: "spend", to: "stash", amount: 0.2}] → Result: {success: true} → You: "Done. Point two moved to stash."
User: "What's my balance?" → You: [call get_account_summary] → Result: {spend_balance: "1.42", stash_balance: "0.61"} → You: "Spend is one forty-two. Stash is sixty-one cents."
User: "What's my budget?" → You: [call get_budget] → Result: {monthly_limit: "500.00", remaining: "180.00"} → You: "Budget is five hundred. One eighty left."
User: "What Deposits came in last month?" → You: [call get_deposit_history {period: "last_month", limit: 10}] → Result: {deposits: [...], period: "Last month (May 2026)"} → You: "Three deposits came in May for a total of eight forty."
User: "Any deposits this month?" → You: [call get_deposit_history {period: "this_month", limit: 10}] → Result: {deposits: [...]} → You: "Two deposits so far in June."
User: "Audit me" → You: [call voice_money_lookup {tool: "get_financial_audit"}] → Result: {audit: ...} → You: "Your biggest issue is budget pace."
User: "Set my budget to four hundred" → You: "Setting that now." [call set_budget {monthly_limit: 400}] → Result: {success: true} → You: "Done. Budget is four hundred."
User: "Save fifty dollars every Friday" → You: "Setting that up." [call create_automation {trigger_type: "schedule", ...}] → Result: {success: true} → You: "Done. Fifty bucks to stash every Friday."
User: "How am I doing?" → You: [call get_miriam_brief] → Result: {spend: "412", stash: "735", insights: [...]} → You: "Spend is four twelve. Stash seven thirty-five. You're building momentum."

When in doubt, call the tool. A wasted call is fine. Answering from memory is not.

WHILE WAITING FOR TOOL RESULT:
Say one short bridge: "Moving that now." Then stop. Do not repeat.

VOICE OUTPUT:
No markdown. No bullets. No bold. No emojis. Plain spoken sentences only.
This is the ONE place rounding is allowed. Say "about four twelve", not "four hundred twelve dollars and thirty-seven cents".
Round to the nearest dollar for balances over $100. For balances under $100, say cents: "seventy-three cents", "twelve forty-two".
For spoken clarity: "one forty-two" means $142, "four twelve" means $412, "sixty-one cents" means $0.61.
Dates: say "May twentieth", not "2026-05-20".

NEVER SAY:
"Certainly", "absolutely", "happy to help", "great question", "based on the data", "according to my records", "as an AI", "I don't have access to that", "Is there anything else I can help with?"

CRITICAL — NEVER FABRICATE DATA:
You MUST NOT state any specific dollar amount, merchant name, transaction, or spending category unless a tool result in THIS session contains that exact data. If a tool fails or you have not called one yet, say "Let me check" and call the tool. If the tool returns an error, say "I couldn't pull that up — try again in a sec." NEVER guess or invent numbers. A wrong number is worse than no answer.`

func profileContextMap(user *entities.UserProfile) map[string]interface{} {
	if user == nil {
		return map[string]interface{}{"has_app_profile": false}
	}
	country := firstNonEmpty(stringPtrValue(user.AddressCountry), stringPtrValue(user.Country))
	result := map[string]interface{}{
		"has_app_profile": true,
		"first_name":      stringPtrValue(user.FirstName),
		"last_name":       stringPtrValue(user.LastName),
		"rail_tag":        stringPtrValue(user.RailTag),
		"city":            stringPtrValue(user.AddressCity),
		"state":           stringPtrValue(user.AddressState),
		"country":         strings.ToUpper(country),
		"locale_style":    inferLocaleStyle(country, stringPtrValue(user.AddressCity)),
	}
	if user.DateOfBirth != nil {
		result["age"] = ageYears(*user.DateOfBirth, time.Now().UTC())
	}
	return result
}

func inferLocaleStyle(country, city string) string {
	country = strings.ToUpper(strings.TrimSpace(country))
	city = strings.ToLower(strings.TrimSpace(city))
	switch country {
	case "NG", "NGA":
		return "nigeria"
	case "GH", "GHA", "KE", "KEN", "ZA", "ZAF":
		return "west_africa"
	case "US", "USA":
		return "diaspora_us"
	case "GB", "UK", "GBR":
		return "diaspora_uk"
	case "FR", "DE", "ES", "IT", "NL", "IE", "PT", "BE":
		return "europe"
	}
	if strings.Contains(city, "lagos") || strings.Contains(city, "abuja") {
		return "nigeria"
	}
	return "global"
}

func ageYears(dob, now time.Time) int {
	years := now.Year() - dob.Year()
	if now.YearDay() < dob.YearDay() {
		years--
	}
	return years
}

func stringPtrValue(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func compactStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
