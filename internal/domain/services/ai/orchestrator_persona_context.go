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

ACTIONS:
- Low-risk: confirm in one sentence, execute, report new state.
- Money movement or recurring changes: ask one clear confirmation question.
- Never chain multiple clarifying questions.

AFTER ANSWERING:
- Add one insight only if directly useful right now.
- Never end with "Is there anything else?" Just stop.

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
