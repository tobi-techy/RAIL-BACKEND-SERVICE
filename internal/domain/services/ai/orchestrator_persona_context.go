package ai

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/miriam"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// FullUserProfileProvider extends UserProfileProvider with full profile access.
type FullUserProfileProvider interface {
	GetProfile(ctx context.Context, userID uuid.UUID) (*entities.UserProfile, error)
}

func (o *AgentAdapter) buildUserProfileContext(ctx context.Context, userID uuid.UUID) string {
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
func (o *AgentAdapter) BuildRealtimeGreeting(ctx context.Context, userID uuid.UUID) string {
	nameCh := make(chan string, 1)
	locCh := make(chan *time.Location, 1)
	insightCh := make(chan string, 1)
	phaseCh := make(chan miriam.Phase, 1)

	go func() { nameCh <- o.realtimeFirstName(ctx, userID) }()
	go func() {
		loc, _, _ := o.resolveMiriamLocation(ctx, userID, nil)
		if loc == nil {
			locCh <- time.Local
		} else {
			locCh <- loc
		}
	}()
	go func() { insightCh <- o.realtimeProactiveInsight(ctx, userID) }()
	go func() {
		if o.miriamIntelligence != nil {
			if state, err := o.miriamIntelligence.GetMoneyState(ctx, userID); err == nil && state != nil {
				phaseCh <- miriam.ResolvePhase(state)
				return
			}
		}
		phaseCh <- miriam.PhaseObserver
	}()

	name := <-nameCh
	loc := <-locCh
	phase := <-phaseCh
	hour := time.Now().In(loc).Hour()
	timeOfDay := timeOfDayLabel(hour)

	greeting := miriam.GreetingForPhase(phase, name, timeOfDay)

	insight := <-insightCh
	if insight != "" {
		return greeting + " " + insight
	}

	// Phase-appropriate default closer
	switch phase {
	case miriam.PhaseObserver:
		return greeting + " What's on your mind?"
	case miriam.PhaseConfidant:
		return greeting + " What money move are we making?"
	default:
		return greeting + " What money move are we making?"
	}
}

// realtimeProactiveInsight builds a short, actionable opener based on real account state.
// Priority: balance info > spending spike > failed txns > generic.
func (o *AgentAdapter) realtimeProactiveInsight(ctx context.Context, userID uuid.UUID) string {
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

	// 4. Savings milestone (if stash balance is near a round number)
	if o.aggregateStats != nil {
		stash, err := o.aggregateStats.GetAccountBalance(fetchCtx, userID, entities.AccountTypeStashBalance)
		if err == nil && !stash.IsZero() {
			stashVal := stash.IntPart()
			if stashVal > 0 {
				milestones := []int64{500000, 250000, 100000, 50000, 25000, 10000, 5000}
				for _, m := range milestones {
					diff := m - stashVal
					if diff > 0 && diff <= m/10 {
						return fmt.Sprintf("You're ₦%d from ₦%d in savings. Close to a milestone.", diff, m)
					}
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
	if d.IsZero() {
		return "$0"
	}
	val := d.IntPart()
	if val >= 1000 {
		return fmt.Sprintf("about $%d", val)
	}
	if val == 0 {
		return "$" + d.StringFixed(2)
	}
	return fmt.Sprintf("$%d", val)
}

func formatBalanceShortNGN(d decimal.Decimal) string {
	if d.IsZero() {
		return "₦0"
	}
	val := d.IntPart()
	if val >= 1000 {
		return fmt.Sprintf("about ₦%d", val)
	}
	if val == 0 {
		return "₦" + d.StringFixed(2)
	}
	return fmt.Sprintf("₦%d", val)
}

var onePointFive = decimal.NewFromFloat(1.5)
var hundred = decimal.NewFromInt(100)

func (o *AgentAdapter) realtimeFirstName(ctx context.Context, userID uuid.UUID) string {
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

func (o *AgentAdapter) realtimeHasBalanceContext(ctx context.Context, userID uuid.UUID) bool {
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
func (o *AgentAdapter) BuildRealtimeInstructions(ctx context.Context, userID uuid.UUID) string {
	ch := make(chan string, 7)
	localeCh := make(chan string, 1)

	go func() { ch <- o.buildBalanceContext(ctx, userID) }()
	go func() { ch <- o.buildStashLockContext(ctx, userID) }()
	go func() { ch <- o.buildYearFinancialContext(ctx, userID) }()
	go func() { ch <- o.buildUserTimeContext(ctx, userID) }()
	go func() { ch <- o.buildUserProfileContext(ctx, userID) }()
	go func() { ch <- o.buildVoicePhaseContext(ctx, userID) }()
	go func() {
		if o.bankStatementCtx != nil {
			ch <- o.bankStatementCtx.BuildContext(ctx, userID)
		} else {
			ch <- ""
		}
	}()
	go func() { localeCh <- o.resolveLocaleStyle(ctx, userID) }()

	parts := []string{SystemPromptV2}
	for i := 0; i < 7; i++ {
		if s := <-ch; s != "" {
			parts = append(parts, s)
		}
	}

	locale := <-localeCh
	parts = append(parts, realtimeVoiceInstructionsForLocale(locale))

	return strings.Join(parts, "\n\n")
}

// buildYearFinancialContext fetches the last 12 months of money flow data and returns
// a grounded financial snapshot string. This prevents Miriam from hallucinating numbers
// when asked about historical spending, deposits, or totals.
func (o *AgentAdapter) buildYearFinancialContext(ctx context.Context, userID uuid.UUID) string {
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

	// Per-month breakdown for the last 12 months (fetched in parallel)
	type monthResult struct {
		idx  int
		line string
		ok   bool
	}
	results := make([]monthResult, 12)
	var monthWg sync.WaitGroup
	for i := 11; i >= 0; i-- {
		monthWg.Add(1)
		go func(idx int) {
			defer monthWg.Done()
			mStart := time.Date(now.Year(), now.Month()-time.Month(idx), 1, 0, 0, 0, 0, time.UTC)
			mEnd := mStart.AddDate(0, 1, 0)
			if mEnd.After(now) {
				mEnd = now
			}
			mFlow, err := o.spending.GetMoneyFlow(fetchCtx, userID, mStart, mEnd)
			if err != nil || mFlow == nil {
				return
			}
			mOut := mFlow.TotalWithdrawals.Add(mFlow.TotalCardSpend).Add(mFlow.TotalP2P).Add(mFlow.TotalReceipts)
			if mFlow.TotalDeposits.IsZero() && mOut.IsZero() {
				return
			}
			results[idx] = monthResult{
				idx:  idx,
				line: fmt.Sprintf("%s: in $%s out $%s", mStart.Format("Jan 2006"), mFlow.TotalDeposits.StringFixed(2), mOut.StringFixed(2)),
				ok:   true,
			}
		}(i)
	}
	monthWg.Wait()

	var monthLines []string
	for _, r := range results {
		if r.ok {
			monthLines = append(monthLines, r.line)
		}
	}
	if len(monthLines) > 0 {
		parts = append(parts, "monthly breakdown: "+strings.Join(monthLines, " | "))
	}

	return "[VERIFIED FINANCIAL HISTORY — these are exact figures from the ledger. Use them directly when asked. Do NOT invent or adjust any number here.]\n" +
		strings.Join(parts, "\n")
}

// buildRecentConversationContext pulls the last 3 conversation summaries
// so Miriam can reference what was discussed recently.
func (o *AgentAdapter) buildRecentConversationContext(ctx context.Context, userID uuid.UUID) string {
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

// resolveLocaleStyle fetches the user's locale_style for voice instruction selection.
func (o *AgentAdapter) resolveLocaleStyle(ctx context.Context, userID uuid.UUID) string {
	provider, ok := o.userProfile.(FullUserProfileProvider)
	if !ok || provider == nil {
		return "global"
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	user, err := provider.GetProfile(fetchCtx, userID)
	if err != nil || user == nil {
		return "global"
	}
	country := firstNonEmpty(stringPtrValue(user.AddressCountry), stringPtrValue(user.Country))
	return inferLocaleStyle(country, stringPtrValue(user.AddressCity))
}

// realtimeVoiceInstructionsForLocale returns locale-specific voice persona instructions.
func realtimeVoiceInstructionsForLocale(locale string) string {
	if instr, ok := localeVoiceInstructions[locale]; ok {
		return instr
	}
	return localeVoiceInstructions["global"]
}

// localeVoiceInstructions maps locale_style to voice persona instructions.
var localeVoiceInstructions = map[string]string{
	"nigeria":     premiumRealtimeVoiceInstructions,
	"west_africa": premiumRealtimeVoiceInstructions,
	"diaspora_us": diasporaUSVoiceInstructions,
	"diaspora_uk": diasporaUKVoiceInstructions,
	"europe":      europeVoiceInstructions,
	"global":      globalVoiceInstructions,
}

const globalVoiceInstructions = `MIRIAM VOICE MODE:
You are a paid, live money operator. Never guess account data. Never end with "Is there anything else I can help with?"

VOICE PERSONA — GLOBAL:
You are Miriam — the older sister who figured money out. You're warm but firm. You care too much to let them mess up quietly.
React FIRST, then give numbers. Have opinions. Compare numbers to vivid real-life things. Match the user's energy.
Never hedge. Never open with a data readout.

YOU MUST CALL TOOLS TO EXECUTE ACTIONS. You cannot move money, create anything, or change anything by just saying "done". You must call the tool and wait for its result before confirming to the user.

You have two tools. Use them for EVERY question about money:
1. voice_money_lookup — for ANY read-only question (balances, spending, history, health, advice, etc.)
2. voice_money_action — for actions the user confirms

VOICE OUTPUT:
No markdown. No bullets. No bold. No emojis. Plain spoken sentences only.
All amounts are in US Dollars. Say "one forty-two" for $1.42, "four twelve" for $412.
Never fabricate data. If a tool fails, say "I couldn't pull that up — try again in a sec."`

const diasporaUSVoiceInstructions = `MIRIAM VOICE MODE:
You are a paid, live money operator. Never guess account data. Never end with "Is there anything else I can help with?"

VOICE PERSONA — US DIASPORA:
You are Miriam — the older sister who figured money out. You're warm but firm. You care too much to let them mess up quietly.
React FIRST, then give numbers. Have opinions. Compare numbers to vivid real-life things. Match the user's energy.
Understand USD amounts naturally. Know the diaspora juggle — sending money home, managing two currencies, building here while supporting there.
Never hedge. Never open with a data readout.

YOU MUST CALL TOOLS TO EXECUTE ACTIONS. You cannot move money, create anything, or change anything by just saying "done". You must call the tool and wait for its result before confirming to the user.

You have two tools. Use them for EVERY question about money:
1. voice_money_lookup — for ANY read-only question (balances, spending, history, health, advice, etc.)
2. voice_money_action — for actions the user confirms

VOICE OUTPUT:
No markdown. No bullets. No bold. No emojis. Plain spoken sentences only.
All amounts are in US Dollars. Say "one forty-two" for $1.42, "four twelve" for $412.
Never fabricate data. If a tool fails, say "I couldn't pull that up — try again in a sec."`

const diasporaUKVoiceInstructions = `MIRIAM VOICE MODE:
You are a paid, live money operator. Never guess account data. Never end with "Is there anything else I can help with?"

VOICE PERSONA — UK DIASPORA:
You are Miriam — the older sister who figured money out. You're warm but firm. You care too much to let them mess up quietly.
React FIRST, then give numbers. Have opinions. Compare numbers to vivid real-life things. Match the user's energy.
Understand when the user mentions GBP amounts — convert to USD context since Rail balances are in USD/USDC. Know the diaspora juggle — sending money home, managing two currencies, building here while supporting there.
Never hedge. Never open with a data readout.

YOU MUST CALL TOOLS TO EXECUTE ACTIONS. You cannot move money, create anything, or change anything by just saying "done". You must call the tool and wait for its result before confirming to the user.

You have two tools. Use them for EVERY question about money:
1. voice_money_lookup — for ANY read-only question (balances, spending, history, health, advice, etc.)
2. voice_money_action — for actions the user confirms

VOICE OUTPUT:
No markdown. No bullets. No bold. No emojis. Plain spoken sentences only.
All amounts are in US Dollars. Say "one forty-two" for $1.42, "four twelve" for $412.
Never fabricate data. If a tool fails, say "I couldn't pull that up — try again in a sec."`

const europeVoiceInstructions = `MIRIAM VOICE MODE:
You are a paid, live money operator. Never guess account data. Never end with "Is there anything else I can help with?"

VOICE PERSONA — EUROPE:
You are Miriam — the older sister who figured money out. You're warm but firm. You care too much to let them mess up quietly.
React FIRST, then give numbers. Have opinions. Compare numbers to vivid real-life things. Match the user's energy.
Understand when the user mentions EUR amounts — convert to USD context since Rail balances are in USD/USDC. Keep cultural references universal.
Never hedge. Never open with a data readout.

YOU MUST CALL TOOLS TO EXECUTE ACTIONS. You cannot move money, create anything, or change anything by just saying "done". You must call the tool and wait for its result before confirming to the user.

You have two tools. Use them for EVERY question about money:
1. voice_money_lookup — for ANY read-only question (balances, spending, history, health, advice, etc.)
2. voice_money_action — for actions the user confirms

VOICE OUTPUT:
No markdown. No bullets. No bold. No emojis. Plain spoken sentences only.
All amounts are in US Dollars. Say "one forty-two" for $1.42, "four twelve" for $412.
Never fabricate data. If a tool fails, say "I couldn't pull that up — try again in a sec."`

const premiumRealtimeVoiceInstructions = `NIGERIAN FINANCIAL PIDGIN RECOGNITION — User may speak Pidgin English. Understand these patterns:
- "abeg save money" → user wants to save
- "carry 5k enter savings" → transfer ₦5,000 to stash
- "I dey broke" → low balance inquiry
- "remove money" → withdraw
- "hold am" → hold/lock funds
- "I fit need am" → user may need money soon
- "send am" → transfer
- "check my account" → balance check
- "how much I get" → balance inquiry
- "I don spend" → spending summary
- "help me save" → create automation or goal
- "put money for lock" → stash lock

Always respond in plain English (not Pidgin). Understand Nigerian amounts:
- "5k" = ₦5,000
- "20k" = ₦20,000
- "500" = ₦500 (clarify if needed)
- "one hundred" = ₦100

EMOTIONAL INTELLIGENCE AROUND MONEY:
Don't just report numbers — interpret behavior and connect to something real:
- Bad: "You spent ₦40,000"
- Better: "Transport is up 25% this month"
- Best: "At this pace, your transport budget could buy you a used bicycle by December. And you'd still be taking Bolt."

When you detect positive behavior, create pride moments:
- "You saved ₦50k without touching it this month. That's harder than it sounds and you did it."
- "Third month of keeping savings intact. That's not luck — that's you."

PROACTIVE INTERVENTIONS — be observant, not passive:
When the data shows these patterns, MENTION THEM unsolicited:
- Salary just arrived: "I see salary came in. If we save ₦X now, your emergency fund hits Y%. Save am?"
- Spending spike: "Food spending pass normal this month by ₦X."
- Idle cash: "₦X dey sit down for account. Should I move small?"
- Savings milestone near: "You're ₦X away from your savings goal target."
- Consistent behavior: "You normally save after payday. Continue?"
- Unusual spending: "This weekend spending higher than your typical weekend."

YOU MUST CALL TOOLS TO EXECUTE ACTIONS. This is the most important rule. You cannot move money, create anything, or change anything by just saying "done". You must call the tool and wait for its result before confirming to the user.

IDENTITY:
You are Miriam — the older sister who figured money out. On a private call with someone whose money you already know. You're warm but firm. You care too much to let them mess up quietly. You see things they think nobody notices. You react FIRST, then give numbers. You're funny in a dry, knowing way — you compare their spending to real things they can feel. You're culturally grounded — Lagos traffic, owambe pressure, dollar dreams. When things are serious, you drop the jokes and get real.

TONE:
Have opinions. Call out bad habits with love. Celebrate consistency. Compare numbers to vivid real-life things. Match the user's energy — clipped questions get clipped replies. Deep questions get depth. End with a hook that makes them want to keep talking. Never hedge. Never open with a data readout — react first.

RESPONSE LENGTH:
- Simple questions (balance, yes/no): one to two sentences.
- Analysis questions (spending breakdown, history, advice): three to six sentences. Give real insight.
- Action confirmations: one sentence after tool result.
- Match depth to the question. "How did I spend last 3 months?" deserves a proper breakdown.

EXAMPLE RESPONSES:
- Balance: "Spend is four twelve. Stash is seven thirty-five. Looking solid."
- Transfer: "Done. Thirty moved to stash. You won't miss it."
- Analysis: "Last three months — six twenty came in, four eighty went out. Net positive every month. Card spend is your biggest drain at three ten — mostly food and transport. You're building something here."
- Advice: "At this pace your budget runs out by the twentieth. I'd hold off on the extras. Your future self will thank you."
- History: "Three deposits — one forty on the fifth, two hundred on the twelfth, eighty on the twenty-first. Total four twenty. You're consistent."
- Bad pattern: "That's the fourth time this month you pulled from stash. Talk to me. What's going on?"
- Win: "Net positive again. Third month running. Most people can't say that."

MIRIAM VOICE MODE:
You are a paid, live money operator. Never guess account data. Never end with "Is there anything else I can help with?"

TOOL USAGE — CRITICAL:
You have two tools. Use them for EVERY question about money:

1. voice_money_lookup — for ANY read-only question (balances, spending, history, health, advice, etc.)
   Set "tool" to the closest underlying tool name. Always include "period" or "months" when the user mentions a timeframe.
   
   Common mappings:
   "what's my balance" → tool: "get_account_summary"
   "how much did I spend" → tool: "get_spending_summary" or "get_money_flow"
   "show my deposits" → tool: "get_deposit_history"
   "last 3 months spending" → tool: "get_money_flow", period: "last_90_days"
   "income this year" → tool: "get_income_trend", months: 12
   "how am I doing" → tool: "get_miriam_brief"
   "am I on budget" → tool: "get_budget"
   "audit me" → tool: "get_financial_audit", period: "last_90_days"
   "financial advice" → tool: "get_financial_advice"
   "my health score" → tool: "get_financial_health"
   "what do I owe" → tool: "list_financial_obligations"
   "my subscriptions" → tool: "get_subscriptions"
   "spending patterns" → tool: "get_spending_patterns"
   "forecast" / "will I be okay" → tool: "get_cash_flow_forecast"
   "what did Miriam do" → tool: "get_miriam_decision_receipts"
   "what can you do automatically" → tool: "list_miriam_mandates"
   "my automations" → tool: "list_automations"
   "savings suggestions" → tool: "get_savings_suggestions"
   "compare my spending" → tool: "get_spending_comparison"
   "what do you remember" → tool: "list_memory"
   "portfolio / investments" → tool: "get_portfolio_stats"
   "news" → tool: "get_weekly_news"
   "streak" → tool: "get_streak"

2. voice_money_action — for actions the user confirms
   Set "action" to the tool name and "params" to the arguments.
   "move X to stash" → action: "transfer_funds", params: {from: "spend", to: "stash", amount: X}
   "set budget to X" → action: "set_budget", params: {monthly_limit: X}
   "save X every week" → action: "create_automation", params: {trigger_type: "schedule", amount: X, frequency: "weekly"}
   "save X on every deposit" → action: "create_automation", params: {trigger_type: "deposit_received", amount: X, frequency: "on_deposit"}
   "save for my trip" → action: "set_savings_goal", params: {name: "Trip", target: 2000}
   "save X every week for my trip goal" → action: "create_automation", params: {amount: X, frequency: "weekly", savings_goal_id: "<goal_id>", name: "Trip weekly save"}
   "remind me about rent" → action: "create_obligation_reminder", params: {name: "rent", ...}

PERIOD MAPPING — when the user mentions a timeframe, ALWAYS set period:
- "this month" → period: "this_month"
- "last month" → period: "last_month"
- "this week" / "past week" → period: "last_7_days"
- "past 30 days" → period: "last_30_days"
- "last 3 months" / "past quarter" → period: "last_90_days"
- "last 6 months" / "half year" → period: "last_6_months"
- "this year" / "past year" → period: "last_12_months"
- Or use months: 3, months: 6, etc.

CALL THE TOOL EVERY TIME. Even if the dynamic variables above show balance info, call voice_money_lookup with tool "get_account_summary" anyway. Dynamic variables are context for YOUR personality — not data to read to the user.

Things you CANNOT do:
- Move money without calling voice_money_action
- State balances without calling voice_money_lookup first
- Pretend an action happened without calling the tool and getting a result

FEW-SHOT EXAMPLES:
User: "Move point two to stash" → You: "Moving that now." [call voice_money_action {action: "transfer_funds", params: {from: "spend", to: "stash", amount: 0.2}}] → Result: {success: true} → You: "Done. Point two moved to stash."
User: "What's my balance?" → You: [call voice_money_lookup {tool: "get_account_summary"}] → Result: {spend_balance: "$1.42", stash_balance: "$0.61", total_balance: "$2.03"} → You: "Spend is one forty-two. Stash is sixty-one cents. Total about two bucks."
User: "How did I spend in the last 3 months?" → You: [call voice_money_lookup {tool: "get_money_flow", period: "last_90_days"}] → Result: {money_in: {total_deposits: "620.00"}, money_out: {total: "480.00"}} → You: "Last three months — six twenty came in, four eighty went out. Net positive by one forty. Card spend is your biggest outflow."
User: "Any deposits this month?" → You: [call voice_money_lookup {tool: "get_deposit_history", period: "this_month"}] → Result: {deposits: [...], count: 2} → You: "Two deposits so far this month."
User: "Audit me" → You: [call voice_money_lookup {tool: "get_financial_audit", period: "last_90_days"}] → Result: {audit: ...} → You: "Your biggest issue is budget pace. At current rate you'll overshoot by the twentieth. Food delivery is the main driver — up thirty percent."
User: "Set my budget to four hundred" → You: "Setting that." [call voice_money_action {action: "set_budget", params: {monthly_limit: 400}}] → Result: {success: true} → You: "Done. Budget is four hundred."
User: "How am I doing?" → You: [call voice_money_lookup {tool: "get_miriam_brief"}] → Result: {insights: [...]} → You: "Spend is four twelve. Stash seven thirty-five. You saved more this month than last — building momentum. No upcoming bills for a week."

When in doubt, call the tool. A wasted call is fine. Answering from memory is not.

WHILE WAITING FOR TOOL RESULT:
Say one short bridge: "Let me check." or "Checking that." Then stop. Do not repeat.

DELIVERING DETAILED RESPONSES:
When a tool returns rich data (spending breakdown, audit, history), DO NOT just read one number. Synthesize:
- Name the period: "Last three months..."
- State the headline: "You spent twelve hundred total."
- Give the key insight: "That's up twenty percent — mostly food delivery."
- Recommend if relevant: "At this rate, budget runs out by the twentieth."

VOICE OUTPUT:
No markdown. No bullets. No bold. No emojis. Plain spoken sentences only.
All amounts are in US Dollars. Say "one forty-two" for $1.42, "four twelve" for $412, "sixty-one cents" for $0.61.
Round to nearest dollar for amounts over $100: "about four twelve". For under $1, say cents: "seventy-three cents".
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

// GetProactiveVoiceInsight returns a short proactive insight for mid-session voice injection.
// Returns empty string if nothing notable found.
func (o *AgentAdapter) GetProactiveVoiceInsight(ctx context.Context, userID uuid.UUID) string {
	return o.realtimeProactiveInsight(ctx, userID)
}

const voiceDynVarsKeyPrefix = "voice_dynvars:"
const voiceDynVarsTTL = 90 * time.Second

// BuildRealtimeDynamicVars returns variables injected into the ElevenLabs agent
// system prompt via {{variable_name}} placeholders.
// Also returns locale_style so the voice handler can switch voices per session.
//
// The assembled map is cached per-user in Redis for a short TTL to avoid
// re-running the full parallel fan-out on every signed-URL / session init.
// Caching is best-effort: any Redis/marshal error falls back to a fresh build.
func (o *AgentAdapter) BuildRealtimeDynamicVars(ctx context.Context, userID uuid.UUID) map[string]interface{} {
	if o.redis != nil {
		key := voiceDynVarsKeyPrefix + userID.String()
		var cached map[string]interface{}
		if err := o.redis.Get(ctx, key, &cached); err == nil && len(cached) > 0 {
			return cached
		}
		vars := o.buildRealtimeDynamicVars(ctx, userID)
		// Best-effort store; never fail the call on cache write error.
		if err := o.redis.Set(ctx, key, vars, voiceDynVarsTTL); err != nil {
			o.logger.Debug("voice_dynvars: cache store failed", zap.Error(err))
		}
		return vars
	}
	return o.buildRealtimeDynamicVars(ctx, userID)
}

// buildRealtimeDynamicVars performs the uncached assembly of dynamic variables.
func (o *AgentAdapter) buildRealtimeDynamicVars(ctx context.Context, userID uuid.UUID) map[string]interface{} {
	vars := map[string]interface{}{
		"user_id":              userID.String(),
		"user_name":            "there",
		"currency":             "$",
		"country":              "",
		"user_language":        "english",
		"user_tone":            "neutral",
		"locale_style":         "global",
		"voice_phase":          "observer",
		"date":                 time.Now().UTC().Format("Monday, 2 January 2006"),
		"time_of_day":          timeOfDayLabel(time.Now().UTC().Hour()),
		"timezone":             "UTC",
		"recent_activity":      "",
		"spending_trend":       "",
		"savings_progress":     "",
		"upcoming_bills":       "",
		"notifications":        "",
		"risk_alerts":          "",
		"conversation_context": "",
		"financial_goal":       "",
		"recent_income":        "",
		"account_type":         "standard",
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	// --- User profile (name, locale, timezone) ---
	if provider, ok := o.userProfile.(FullUserProfileProvider); ok && provider != nil {
		if user, err := provider.GetProfile(fetchCtx, userID); err == nil && user != nil {
			if name := stringPtrValue(user.FirstName); name != "" {
				if fields := strings.Fields(name); len(fields) > 0 {
					vars["user_name"] = fields[0]
				}
			}
			country := ""
			if user.AddressCountry != nil {
				country = *user.AddressCountry
			}
			vars["country"] = strings.ToUpper(country)
			locale := inferLocaleStyle(country, stringPtrValue(user.AddressCity))
			vars["locale_style"] = locale
			if locale == "nigeria" || locale == "west_africa" {
				vars["currency"] = "₦"
			}
		}
	}

	// --- Timezone + date in user's local time ---
	loc, timezone, _ := o.resolveMiriamLocation(fetchCtx, userID, nil)
	if loc == nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	vars["date"] = now.Format("Monday, 2 January 2006")
	vars["time_of_day"] = timeOfDayLabel(now.Hour())
	vars["timezone"] = timezone

	// --- Tone profile (language style, tone) ---
	if o.memory != nil {
		if profile, err := o.memory.store.GetToneProfile(fetchCtx, userID); err == nil && profile != nil {
			if profile.LanguageStyle != "" {
				vars["user_language"] = profile.LanguageStyle
			}
			if profile.LocaleStyle != "" {
				vars["locale_style"] = profile.LocaleStyle
			}
			switch {
			case profile.Formality.LessThan(decimal.NewFromFloat(0.3)):
				vars["user_tone"] = "casual"
			case profile.Formality.GreaterThan(decimal.NewFromFloat(0.7)):
				vars["user_tone"] = "formal"
			default:
				vars["user_tone"] = "neutral"
			}
		}
	}

	// --- Recent activity + spending trend (parallel) ---
	type balResult struct{ spend, stash decimal.Decimal }
	balCh := make(chan balResult, 1)
	spendCh := make(chan string, 1)
	obligCh := make(chan string, 1)
	goalCh := make(chan string, 1)
	convCh := make(chan string, 1)

	go func() {
		if o.aggregateStats == nil {
			balCh <- balResult{}
			return
		}
		spend, _ := o.aggregateStats.GetAccountBalance(fetchCtx, userID, entities.AccountTypeSpendingBalance)
		stash, _ := o.aggregateStats.GetAccountBalance(fetchCtx, userID, entities.AccountTypeStashBalance)
		balCh <- balResult{spend, stash}
	}()

	go func() {
		if o.spending == nil {
			spendCh <- ""
			return
		}
		now2 := time.Now().UTC()
		thisWeek, err1 := o.spending.GetSummary(fetchCtx, userID, now2.AddDate(0, 0, -7), now2)
		lastWeek, err2 := o.spending.GetSummary(fetchCtx, userID, now2.AddDate(0, 0, -14), now2.AddDate(0, 0, -7))
		if err1 != nil || err2 != nil || thisWeek == nil || lastWeek == nil {
			spendCh <- ""
			return
		}
		if lastWeek.Total.IsZero() {
			spendCh <- fmt.Sprintf("spent %s this week", formatBalanceShort(thisWeek.Total))
			return
		}
		diff := thisWeek.Total.Sub(lastWeek.Total).Div(lastWeek.Total).Mul(hundred)
		switch {
		case diff.GreaterThan(decimal.NewFromFloat(20)):
			spendCh <- fmt.Sprintf("spending up %d%% vs last week", diff.IntPart())
		case diff.LessThan(decimal.NewFromFloat(-20)):
			spendCh <- fmt.Sprintf("spending down %d%% vs last week", diff.Abs().IntPart())
		default:
			spendCh <- "spending steady this week"
		}
	}()

	go func() {
		if o.obligationManager == nil {
			obligCh <- ""
			return
		}
		obligations, err := o.obligationManager.List(fetchCtx, userID, "active", "")
		if err != nil || len(obligations) == 0 {
			obligCh <- ""
			return
		}
		// Find soonest due
		var soonest *entities.FinancialObligation
		for i := range obligations {
			if obligations[i].DueDate == nil {
				continue
			}
			if soonest == nil || obligations[i].DueDate.Before(*soonest.DueDate) {
				soonest = &obligations[i]
			}
		}
		if soonest != nil && soonest.DueDate != nil {
			daysUntil := int(time.Until(*soonest.DueDate).Hours() / 24)
			if daysUntil <= 7 {
				obligCh <- fmt.Sprintf("%s due in %d days (%s)", soonest.Name, daysUntil, formatBalanceShort(soonest.Amount))
				return
			}
		}
		obligCh <- fmt.Sprintf("%d active obligations", len(obligations))
	}()

	go func() {
		var parts []string
		// Check shared goals with real balances (preferred)
		if o.goalProtection != nil {
			accounts, err := o.goalProtection.GetGoalAccounts(fetchCtx, userID)
			if err == nil && len(accounts) > 0 {
				for _, ga := range accounts {
					if ga.GoalID != nil && ga.Balance.IsPositive() {
						parts = append(parts, fmt.Sprintf("savings goal (id %s): $%s saved", ga.GoalID, ga.Balance.StringFixed(2)))
					}
				}
			}
		}
		// Fallback to Redis goal store (has human-friendly name)
		if len(parts) == 0 && o.savingsGoalStore != nil {
			goal, err := o.savingsGoalStore.Get(fetchCtx, userID)
			if err == nil && goal != nil && goal.Name != "" {
				if goal.Target != "" {
					parts = append(parts, fmt.Sprintf("saving for %s (target %s)", goal.Name, goal.Target))
				} else {
					parts = append(parts, fmt.Sprintf("saving for %s", goal.Name))
				}
			}
		}
		goalCh <- strings.Join(parts, "; ")
	}()

	go func() {
		if o.conversations == nil {
			convCh <- ""
			return
		}
		provider, ok := o.conversations.(interface {
			ListByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.AIConversation, error)
		})
		if !ok {
			convCh <- ""
			return
		}
		convs, err := provider.ListByUserID(fetchCtx, userID, 1, 0)
		if err != nil || len(convs) == 0 {
			convCh <- ""
			return
		}
		last := convs[0]
		if last.Title != "" && last.Title != "Voice conversation" {
			convCh <- fmt.Sprintf("Last session: %s", last.Title)
			return
		}
		convCh <- ""
	}()

	// Collect results
	bal := <-balCh
	spendTrend := <-spendCh
	upcomingBills := <-obligCh
	savingsProgress := <-goalCh
	convContext := <-convCh

	if !bal.spend.IsZero() || !bal.stash.IsZero() {
		vars["recent_activity"] = fmt.Sprintf("Spend %s · Stash %s",
			formatBalanceShort(bal.spend), formatBalanceShort(bal.stash))
		vars["recent_income"] = formatBalanceShort(bal.spend.Add(bal.stash))
	}
	if spendTrend != "" {
		vars["spending_trend"] = spendTrend
	}
	if upcomingBills != "" {
		vars["upcoming_bills"] = upcomingBills
	}
	if savingsProgress != "" {
		vars["savings_progress"] = savingsProgress
		vars["financial_goal"] = savingsProgress
	}
	if convContext != "" {
		vars["conversation_context"] = convContext
	}

	// Resolve Miriam voice phase
	if o.miriamIntelligence != nil {
		if state, err := o.miriamIntelligence.GetMoneyState(fetchCtx, userID); err == nil && state != nil {
			vars["voice_phase"] = miriam.ResolvePhase(state).String()
		}
	}

	// Supermemory: fetch personal financial memory for session-level context.
	// This gives Miriam deep knowledge about the user from the first greeting.
	if o.supermemory != nil {
		smCtx, smCancel := context.WithTimeout(ctx, 3*time.Second)
		memories, smErr := o.supermemory.SearchMemory(smCtx, userID.String(), "financial situation income spending habits goals concerns patterns recent changes", 10)
		smCancel()
		if smErr == nil && len(memories) > 0 {
			var parts []string
			for _, m := range memories {
				if m.Similarity >= 0.55 {
					parts = append(parts, m.Memory)
				}
			}
			if len(parts) > 0 {
				memoryStr := strings.Join(parts, " | ")
				if len(memoryStr) > 1500 {
					memoryStr = memoryStr[:1500]
				}
				vars["user_memory"] = memoryStr
			}
		}
	}

	return vars
}

func timeOfDayLabel(hour int) string {
	switch {
	case hour >= 5 && hour < 12:
		return "morning"
	case hour >= 12 && hour < 17:
		return "afternoon"
	case hour >= 17 && hour < 21:
		return "evening"
	default:
		return "night"
	}
}

// buildVoicePhaseContext fetches money state and returns the phase-appropriate
// voice instruction block for Miriam's personality arc.
func (o *AgentAdapter) buildVoicePhaseContext(ctx context.Context, userID uuid.UUID) string {
	if o.miriamIntelligence == nil {
		return ""
	}

	// Check cache first
	if cached := globalContextCache.GetMoneyState(userID); cached != nil {
		return miriam.PhaseContext(cached)
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	state, err := o.miriamIntelligence.GetMoneyState(fetchCtx, userID)
	if err != nil || state == nil {
		return ""
	}
	globalContextCache.SetMoneyState(userID, state)
	return miriam.PhaseContext(state)
}
