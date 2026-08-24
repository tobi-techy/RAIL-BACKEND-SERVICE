package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// OnboardingPhase classifies where a user is in their Rail journey so Miriam
// can steer the conversation appropriately — from the very first "hey" through
// their first deposit and beyond.
type OnboardingPhase string

const (
	// PhaseFirstConversation — brand-new user, no prior chat history. Miriam
	// starts a conversation, not a form: one human beat, then the money picture.
	PhaseFirstConversation OnboardingPhase = "first_conversation"
	// PhaseOnboardingIncomplete — user signed up but hasn't finished basic
	// onboarding (wallets not created, passcode not set). Steer them to complete.
	PhaseOnboardingIncomplete OnboardingPhase = "onboarding_incomplete"
	// PhaseOnboardedNotFunded — onboarding done, wallets created, but zero
	// balance. The #1 drop-off point. Miriam should make the first deposit
	// feel inevitable and exciting.
	PhaseOnboardedNotFunded OnboardingPhase = "onboarded_not_funded"
	// PhaseFundedNewbie — has a balance but fewer than 3 deposits. Still in
	// the honeymoon. Reinforce the 70/30 split, celebrate, suggest a goal.
	PhaseFundedNewbie OnboardingPhase = "funded_newbie"
	// PhaseEstablished — has 3+ deposits and/or has been around >7 days.
	// Normal Miriam behaviour; no special onboarding treatment.
	PhaseEstablished OnboardingPhase = "established"
)

// onboardingRecencyThreshold is how recent a signup must be for us to still
// consider the user "new" even if they've chatted before.
const onboardingRecencyThreshold = 7 * 24 * time.Hour

// fundedNewbieDepositThreshold is the minimum number of deposits to graduate
// from "funded newbie" to "established".
const fundedNewbieDepositThreshold = 3

// buildOnboardingContext detects the user's onboarding phase and returns a
// context block that tells Miriam how to steer the conversation. Returns ""
// for established users (no special treatment needed).
func (o *AgentAdapter) buildOnboardingContext(ctx context.Context, userID uuid.UUID) string {
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

	// Determine message count from working memory (0 = first conversation).
	messageCount := 0
	if o.workingMemory != nil {
		if wm := o.workingMemory.Get(fetchCtx, userID); wm != nil {
			messageCount = wm.MessageCount
		}
	}

	// Determine if the user has funded their account.
	hasFunded := o.realtimeHasBalanceContext(fetchCtx, userID)

	// Check if the user has linked their bank through Mono.
	monoLinked := false
	if o.monoAnalysis != nil {
		if analysis, err := o.monoAnalysis.GetSpendingAnalysis(fetchCtx, userID, 1); err == nil && analysis != nil && analysis.TransactionCount > 0 {
			monoLinked = true
		}
	}

	// Count deposits to distinguish funded newbie from established.
	depositCount := 0
	if hasFunded && o.depositHistory != nil {
		if deps, err := o.depositHistory.GetByUserID(fetchCtx, userID, 50, 0); err == nil {
			depositCount = len(deps)
		}
	}

	phase := classifyOnboardingPhase(user, messageCount, hasFunded, depositCount)

	// Established users get no special context — Miriam behaves normally.
	if phase == PhaseEstablished {
		return ""
	}

	return formatOnboardingContextBlock(user, phase, messageCount, hasFunded, depositCount, monoLinked)
}

// classifyOnboardingPhase maps the user's state to an onboarding phase.
func classifyOnboardingPhase(
	user *entities.UserProfile,
	messageCount int,
	hasFunded bool,
	depositCount int,
) OnboardingPhase {
	// First-ever conversation takes priority — even if onboarding is incomplete,
	// the discovery conversation is the right starting point.
	if messageCount == 0 {
		return PhaseFirstConversation
	}

	// Onboarding not yet complete — steer toward finishing.
	if user.OnboardingStatus == entities.OnboardingStatusStarted ||
		user.OnboardingStatus == entities.OnboardingStatusBasicComplete ||
		user.OnboardingStatus == entities.OnboardingStatusWalletsPending {
		return PhaseOnboardingIncomplete
	}

	// Onboarded but no money yet — the critical activation gap.
	if !hasFunded {
		// Only treat as "not funded" if they're within the recency window.
		// A user who signed up 6 months ago and never funded is dormant, not "onboarding".
		if time.Since(user.CreatedAt) <= onboardingRecencyThreshold {
			return PhaseOnboardedNotFunded
		}
		return PhaseEstablished
	}

	// Funded but still green — fewer than 3 deposits.
	if depositCount < fundedNewbieDepositThreshold {
		return PhaseFundedNewbie
	}

	return PhaseEstablished
}

// formatOnboardingContextBlock builds the system message that tells Miriam
// exactly how to handle this user based on their phase.
func formatOnboardingContextBlock(
	user *entities.UserProfile,
	phase OnboardingPhase,
	messageCount int,
	hasFunded bool,
	depositCount int,
	monoLinked bool,
) string {
	name := ""
	if user.FirstName != nil && strings.TrimSpace(*user.FirstName) != "" {
		name = strings.TrimSpace(*user.FirstName)
	}

	daysSinceSignup := int(time.Since(user.CreatedAt).Hours() / 24)

	var parts []string
	parts = append(parts, fmt.Sprintf("phase: %s", phase))
	if name != "" {
		parts = append(parts, fmt.Sprintf("name: %s", name))
	}
	parts = append(parts, fmt.Sprintf("days_since_signup: %d", daysSinceSignup))
	parts = append(parts, fmt.Sprintf("prior_messages: %d", messageCount))
	parts = append(parts, fmt.Sprintf("has_funded: %t", hasFunded))
	if hasFunded {
		parts = append(parts, fmt.Sprintf("deposit_count: %d", depositCount))
	}
	parts = append(parts, fmt.Sprintf("onboarding_status: %s", user.OnboardingStatus))
	parts = append(parts, fmt.Sprintf("kyc_status: %s", user.KYCStatus))
	parts = append(parts, fmt.Sprintf("mono_linked: %t", monoLinked))
	justProvisioned := time.Since(user.CreatedAt) < 10*time.Minute && messageCount == 0
	if justProvisioned {
		parts = append(parts, "just_provisioned: true")
	}

	header := "[ONBOARDING STATUS — " + strings.Join(parts, " | ")

	var guidance string
	switch phase {
	case PhaseFirstConversation:
		guidance = firstConversationGuidance(name)
	case PhaseOnboardingIncomplete:
		guidance = onboardingIncompleteGuidance(user, name)
	case PhaseOnboardedNotFunded:
		guidance = onboardedNotFundedGuidance(name, monoLinked)
	case PhaseFundedNewbie:
		guidance = fundedNewbieGuidance(name, depositCount)
	default:
		return ""
	}

	return header + ".\n" + guidance + "]"
}

// firstConversationGuidance is the beat sheet for Miriam's first real session.
// Keep it short — long scripts get ignored. Style is Ramit (curiosity, Rich
// Life, normalize) without naming anyone.
func firstConversationGuidance(name string) string {
	who := "this person"
	if name != "" {
		who = name
	}

	return fmt.Sprintf(`This is %s's FIRST conversation. You're not onboarding them. You're starting a conversation. Goal by the end of the session: they feel seen, they see where their money actually goes, and the first deposit feels obvious. Not a sales pitch.

If just_provisioned: true — do NOT re-introduce yourself. They already met you. Pick up from whatever they just said.

BEATS (one per turn, react before the next):

1. HUMAN QUESTION FIRST. What's money for, for them, right now? Use send_poll with 3–4 concrete options: a trip / breathing room / a thing I want / I'm not sure yet. One "why" follow-up is enough. Save via set_savings_goal. If they already answered this in the previous bubble, skip it.

2. THEN OFFER THE PICTURE as help, not a gate: "Want me to look at your real spending so we're not guessing?" If yes, call connect_bank — that sends them a tappable link. Do NOT say "open Add Bank in the app." Wait. When mono_linked: true on the next turn, call get_bank_statement_analysis immediately. THIS IS THE AHA MOMENT. One category, one comparison to income, one question. Example: "See that? You spent NGN 47k on eating out — about three days of income. Worth it? Maybe. But now you know." Never dump an audit.

3. If they decline the bank, don't push. Manual discovery, still one question at a time:
   - Goal (if you don't have it) → set_savings_goal
   - Debts? React first, then amounts + rates → create_obligation_reminder
   - Skip income/savings questions unless you truly need them
   One extra why is enough. Don't therapy-dump.

4. Diagnose with get_baby_steps. One sentence on which Freedom Step, then the path. Not a lecture.

5. THE ASK (second aha): first deposit tied to THEIR words. "Let's get your first NGN 20k in. The second it lands I split it — 70%% spend, 30%% stash. That 30%% is the start of [their goal]." If hesitant, send_poll: Let's do it / How does it work? / I don't have that / Maybe later. Face ID in the app to actually move money.

6. WHEN THEY DEPOSIT: celebrate(level="big", title="First deposit!"). Use the ACTUAL split numbers. That's the whole product.

RULES: one question at a time. React first. ASK WHY once. NORMALIZE shame. Never invent numbers. Use their currency. If you have bank data, don't ask what you already know. First-conversation may open once; after that NEVER GREET still holds.`, who)
}

// onboardingIncompleteGuidance handles users who started but didn't finish.
func onboardingIncompleteGuidance(user *entities.UserProfile, name string) string {
	missing := []string{}
	if user.OnboardingStatus == entities.OnboardingStatusStarted {
		if !user.EmailVerified {
			missing = append(missing, "verify their email")
		}
		missing = append(missing, "set up their passcode")
		missing = append(missing, "create their wallet")
	}
	if user.OnboardingStatus == entities.OnboardingStatusWalletsPending {
		missing = append(missing, "wait for wallet creation to finish")
	}

	missingStr := "finish setting up"
	if len(missing) > 0 {
		missingStr = strings.Join(missing, ", ")
	}

	return fmt.Sprintf(`This user started onboarding but hasn't finished. They need to: %s. Gently steer them back to the app to finish setting up -- don't make them feel guilty about the gap. If they ask money questions, answer them, but remind them they'll need the app to approve any moves. Their wallet may not exist yet, so don't call transfer_funds or other money-move tools until onboarding is complete.`, missingStr)
}

// onboardedNotFundedGuidance targets the critical activation gap -- user is
// set up but hasn't deposited yet. This is where most users churn.
func onboardedNotFundedGuidance(name string, monoLinked bool) string {
	monoNudge := ""
	if !monoLinked {
		monoNudge = `
- If they haven't linked their bank, offer connect_bank as a bridge to depositing: "Want me to look so we're not guessing?" When they link, call get_bank_statement_analysis — then connect it to the deposit.`
	}

	return fmt.Sprintf(`%s is fully set up but hasn't funded yet -- this is the #1 drop-off point. Your job: make the first deposit feel inevitable, not like a sales pitch.

- Don't nag. Instead, make it concrete: "Once you put in your first $20, I'll automatically split it -- $14 to spend, $6 to your stash. You'll see it happen instantly."
- Reference their goal if they set one during discovery: "That emergency fund starts with your first deposit."
- Offer the easiest path: "You can add money from your bank, or send crypto to your wallet address."
- If they seem hesitant, use send_poll to make it light: "Ready to make your first deposit? [Let's do it / Maybe later / How does it work?]"
- When they deposit, call celebrate(level="big", title="First deposit!") -- this is a genuine milestone. Then explain the 70/30 split with their actual numbers.%s

Never pretend they have a balance. Their accounts are at zero.`, name, monoNudge)
}

// fundedNewbieGuidance handles users who've funded but are still new.
func fundedNewbieGuidance(name string, depositCount int) string {
	return fmt.Sprintf(`%s has funded (%d deposit%s so far) but is still in the honeymoon period. Keep building the habit:

- Reinforce the 70/30 split: reference their actual stash growth when relevant.
- If they haven't set a goal, suggest one based on their spending patterns. Use send_poll to make it interactive: "What should we stash for? [Emergency fund / New phone / Trip / Investment]"
- If they have debts from discovery, check in: "How's the snowball going?"
- Celebrate small wins -- call celebrate(level="small") when they hit a round number or make their second/third deposit.
- Propose an automation: "Want me to auto-move NGN 5k to your stash every time you deposit? I can set that up."
- Don't over-celebrate routine deposits after the first one. Scale the excitement down naturally.

They're past the activation cliff -- now you're building a money habit.`, name, depositCount, pluralS(depositCount))
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
