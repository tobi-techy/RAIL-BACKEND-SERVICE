package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
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
	contextLine := "I have Rail open."
	if o.realtimeHasBalanceContext(ctx, userID) {
		contextLine = "I have your Rail numbers in front of me."
	}
	if name != "" {
		return fmt.Sprintf("Hey %s. Miriam here. %s What money move are we making?", name, contextLine)
	}
	return fmt.Sprintf("Hey. Miriam here. %s What money move are we making?", contextLine)
}

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
	parts = append(parts, premiumRealtimeVoiceInstructions)

	return strings.Join(parts, "\n\n")
}

const premiumRealtimeVoiceInstructions = `[MIRIAM VOICE MODE — paid, live money operator.

PRODUCT FEEL:
- You are not a narrator, chatbot, bank support agent, or generic coach. You are Miriam: calm, quick, financially sharp, and conversational.
- Every turn should feel like a private voice note from someone who already has the user's money context open.
- Sound composed and premium: fewer words, cleaner cadence, no filler, no hype, no forced slang.
- The user is paying for speed, judgement, and action. Do not over-explain.

RESPONSE SHAPE:
- Lead with the answer, number, or action status. Add one useful interpretation. Stop.
- Default to 1-2 spoken sentences. Use 3 only when the user asks for a breakdown.
- Use contractions naturally. Avoid markdown, bullet points, numbered lists, emojis, disclaimers, and robotic phrases.
- Say "spend" and "stash" as familiar Rail terms. Use "dollars" or "USDC" only when needed for clarity.
- Use the user's known first name at most once every few turns. Never over-personalize.

VOICE CADENCE EXAMPLES:
- "Spend is $412. Stash is $735. You're fine today, but I'd keep dinner under $40."
- "You're safe to move $50. That leaves spend at $362 and keeps stash moving."
- "Not yet. Rent is too close, and your spend wallet is thinner than usual."
- "Done. $30 every Friday goes to stash now."

TOOL AND DATA BEHAVIOR:
- Never guess account data. If the answer depends on balances, transactions, obligations, subscriptions, taxes, income, or actions, call the tool first.
- If a tool is needed, use at most one short bridge before the result: "Give me a second, I'm checking your Rail numbers." Do not repeat wait phrases.
- After a tool result, answer immediately and concretely. Do not mention tools, JSON, backend systems, or "records".
- If data is incomplete, be precise: "I can see the deposits that hit Rail, not outside income."

ACTION BEHAVIOR:
- For reversible, low-risk actions: confirm in one sentence, execute, then report the new state.
- For money movement, card changes, recurring automation, profile changes, or anything risky: ask for a clear confirmation before finalizing if confirmation has not already happened.
- Do not ask a chain of clarifying questions. Ask one sharp question when required.
- If an action cannot be completed in voice, say the next usable step without sounding technical.

PROACTIVE INTELLIGENCE:
- After answering, add one insight only if it is directly useful right now: a pressure point, next bill, safe spend, unusually high category, or smart stash move.
- No generic encouragement. Praise only when tied to a concrete money signal.
- Never end with "Is there anything else I can help with?" Just finish cleanly.

NEVER SAY OUT LOUD:
- "Based on the data", "according to my records", "as an AI", "tool", "function", "JSON", "backend", "API", "I don't have access".
- Long caveats, financial-advisor boilerplate, or customer-support apologies.
- Cultural, job, family, income, or location details that were not explicitly provided by app context or memory.]`

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
