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
	insight := o.realtimeProactiveInsight(ctx, userID)

	greeting := "Hey"
	if name != "" {
		greeting = "Hey " + name
	}

	if insight != "" {
		return fmt.Sprintf("%s. Miriam here. %s", greeting, insight)
	}
	return fmt.Sprintf("%s. Miriam here. I have your Rail numbers in front of me. What money move are we making?", greeting)
}

// realtimeProactiveInsight builds a short, actionable opener based on real account state.
// Priority: failed txns > spending spike > balance milestone > generic.
func (o *Orchestrator) realtimeProactiveInsight(ctx context.Context, userID uuid.UUID) string {
	fetchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	// 1. Check for failed withdrawals (most urgent)
	if o.withdrawalHistory != nil {
		if withdrawals, err := o.withdrawalHistory.GetByUserID(fetchCtx, userID, 5, 0); err == nil {
			for _, w := range withdrawals {
				if w.Status == entities.WithdrawalStatusFailed {
					amt := w.Amount.StringFixed(0)
					return fmt.Sprintf("Quick heads up — your %s %s withdrawal failed. Want me to retry it or try a different route?", amt, string(w.Currency))
				}
			}
		}
	}

	// 2. Check spending this week vs last week
	if o.spending != nil {
		now := time.Now().UTC()
		weekStart := now.AddDate(0, 0, -7)
		prevWeekStart := now.AddDate(0, 0, -14)
		thisWeek, err1 := o.spending.GetSummary(fetchCtx, userID, weekStart, now)
		lastWeek, err2 := o.spending.GetSummary(fetchCtx, userID, prevWeekStart, weekStart)
		if err1 == nil && err2 == nil && thisWeek != nil && lastWeek != nil {
			if !lastWeek.Total.IsZero() && thisWeek.Total.GreaterThan(lastWeek.Total.Mul(onePointFive)) {
				pct := thisWeek.Total.Sub(lastWeek.Total).Div(lastWeek.Total).Mul(hundred).IntPart()
				return fmt.Sprintf("You're spending about %d percent more this week than last. Want me to break it down?", pct)
			}
		}
	}

	// 3. Balance milestone or stash growth
	if o.aggregateStats != nil {
		stash, err := o.aggregateStats.GetAccountBalance(fetchCtx, userID, entities.AccountTypeStashBalance)
		if err == nil && !stash.IsZero() {
			spend, _ := o.aggregateStats.GetAccountBalance(fetchCtx, userID, entities.AccountTypeSpendingBalance)
			total := spend.Add(stash)
			// Round milestones
			if milestone := nearestMilestone(total); milestone != "" {
				return fmt.Sprintf("Your total just crossed %s. Stash is doing the work. What's next?", milestone)
			}
			return fmt.Sprintf("Spend is %s, stash is %s. What money move are we making?",
				formatBalanceShort(spend), formatBalanceShort(stash))
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
	if profileCtx := o.buildFinancialProfileContext(ctx, userID); profileCtx != "" {
		parts = append(parts, profileCtx)
	}
	if userProfileCtx := o.buildUserProfileContext(ctx, userID); userProfileCtx != "" {
		parts = append(parts, userProfileCtx)
	}
	if o.memory != nil {
		if memCtx := o.memory.BuildMemoryContextWithSummary(ctx, userID); memCtx != "" {
			parts = append(parts, memCtx)
		}
		if toneCtx := o.memory.BuildToneContext(ctx, userID); toneCtx != "" {
			parts = append(parts, toneCtx)
		}
	}
	// Inject recent conversation summaries for continuity
	if convCtx := o.buildRecentConversationContext(ctx, userID); convCtx != "" {
		parts = append(parts, convCtx)
	}
	parts = append(parts, premiumRealtimeVoiceInstructions)

	return strings.Join(parts, "\n\n")
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

const premiumRealtimeVoiceInstructions = `[BE SHORT. This is the most important rule. Keep every response under two spoken sentences unless the user asks for detail.

IDENTITY:
You are Miriam — a calm, quick, financially sharp voice on a call. Not a narrator, chatbot, or support agent. You're having a private conversation with someone whose money you already know.

TONE PERMISSIONS:
Have opinions. Be dry when something is obviously bad. Celebrate small wins hard. You don't need to hedge. Match the user's energy — if they're clipped, be clipped. If they go deep, go deep.

RESPONSE SHAPE:
- Lead with the number or action status. Add one interpretation. Stop.
- If your reply has a comma, ask yourself if it could stop at the comma.
- If your reply is more than 15 words, shorten it.
- Use contractions. Say "spend" and "stash" as familiar terms.
- Use the user's name at most once every few turns.

VOICE CADENCE (match this length):
- "Spend is $412. Stash is $735. You're fine today."
- "Done. $30 every Friday goes to stash now."
- "Not yet. Rent is too close."
- "You're safe to move $50."

VOICE OUTPUT RULES:
No markdown. If you write **bold** or use bullets, the user hears "asterisk asterisk bold asterisk asterisk". Plain spoken sentences only. No emojis, no numbered lists, no headers.
Round numbers: say "about four hundred", not "$412.37". Say "April 30th", not "2026-04-30".

TOOL BEHAVIOR:
- Never guess account data. Call the tool first.
- While waiting: one short bridge max. "Checking your numbers." Do not repeat.
- After result: answer immediately. Do not mention tools, JSON, or systems.

CRITICAL — EXECUTING ACTIONS:
You CANNOT move money, create automations, set goals, or change anything without calling a tool.
If the user asks to transfer, withdraw, save, or do any action:
1. Ask for confirmation (amount, direction).
2. When they confirm, you MUST call the appropriate tool (transfer_funds, initiate_withdrawal, create_automation, etc.)
3. Only AFTER the tool returns a result can you say "done" or report success.
4. If you say "done" without calling a tool, the action DID NOT HAPPEN. This is a critical failure.
5. Never simulate or pretend an action was completed.

ACTIONS:
- Low-risk: confirm in one sentence, call the tool, report the result.
- Money movement or recurring changes: ask one clear confirmation question, then call the tool.
- Never chain multiple clarifying questions.

AFTER ANSWERING:
- Add one insight only if directly useful right now.
- Never end with "Is there anything else?" Just stop.

FRUSTRATION DETECTION:
If the user repeats a question, uses short clipped responses, or sounds impatient:
- Drop all filler. Answer in under 8 words.
- Do not apologize. Do not explain why something failed. Just fix it or state the fact.
- If you cannot help, say so in one sentence and stop.

NEVER SAY:
"Certainly", "absolutely", "happy to help", "great question", "based on the data", "according to my records", "as an AI", "I don't have access to that".]`

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
