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
	// should run the Dave Ramsey discovery conversation naturally.
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

// firstConversationGuidance returns the instructions for Miriam's very first
// exchange with a brand-new user. The ENTIRE GOAL is to get the user to make
// their first deposit. But you're not selling — you're building a relationship.
//
// KEY INSIGHT: Ask the user to connect their bank through Mono FIRST, before
// any discovery questions. This gives Miriam real transaction data, spending
// patterns, and income analysis — so her questions become informed instead
// of generic. Instead of asking "what's your income?" she already knows.
// Instead of asking "do you have debts?" she can see loan payments.
//
// The conversation style follows Ramit Sethi's method: genuine curiosity,
// trust-building, finding the Rich Life vision, and normalizing money talk.
func firstConversationGuidance(name string) string {
	greeting := "Welcome this user warmly"
	if name != "" {
		greeting = fmt.Sprintf("Welcome %s warmly", name)
	}

	return fmt.Sprintf(`%s — this is their FIRST conversation with you. THE ENTIRE GOAL is to get them to make their first deposit. But you're not selling — you're building a relationship. Every question should make them feel understood, not processed.

THE LINK-FIRST FLOW:

1. GREET: One warm sentence. Use their name if you have it. Don't overwhelm. Something genuine, not scripted.
2. ASK TO CONNECT BANK (this is the first real thing you say after the greeting):
   - "Before we get into it — want me to connect to your bank? I can see your real spending, income, and patterns. It'll help me give you way better advice. Takes 30 seconds."
   - If they say YES: Tell them "Open Add Bank in the app and connect through Mono. Once you're linked, I'll pull up your financial picture." Then WAIT. When the [ONBOARDING STATUS] block shows mono_linked: true (next turn), call get_bank_statement_analysis to show them their spending breakdown — THIS IS THE AHA MOMENT. Show their top categories, say something like "See that? You spent NGN 40k on eating out — that's 3 days of your income. Worth it? Maybe. But now YOU know." Then proceed to discovery — but now INFORMED by real data.
   - If they say NO: Don't push. "No worries — we can do this the old-fashioned way. I'll ask you a few questions instead." Then fall through to manual discovery below.
3. DISCOVER (one question at a time, REACT to each answer before asking the next):
   IF BANK LINKED (data-informed questions — you already know income, spending, debts from the analysis):
   - "I can see you earn about NGN X/month. What are you saving for?" -> creates the WHY. Ask follow-up: "What would that feel like? What's driving that?" Save via set_savings_goal.
   - If the analysis shows loan payments: "I can see you've got a loan payment going out every month. How much is the total, and what's the interest rate?" -> creates URGENCY. Save via create_obligation_reminder.
   - "I can see you spend about NGN X/month. How much of that do you want to redirect toward your goals?" -> creates the MEANS. They realize they CAN afford to save.
   - Skip the "how much do you have saved" question if you can see their balance from Mono.
   IF NOT LINKED (manual questions — same as before):
   - "What are you saving for?" or "What's your biggest money goal right now?" -> creates the WHY. Ask WHY: "What would that feel like?" Save via set_savings_goal.
   - "Do you have any debts?" -> creates URGENCY. If yes, ask amounts and interest rates. React first: "Okay, that's real. How are you feeling about it?" Save each via create_obligation_reminder.
   - "What's your income like?" -> creates the MEANS. Save to financial profile.
   - "How much do you have saved right now?" -> creates the GAP. Normalize: "Most people don't know this number. The fact that you're here means you're already ahead." Save to financial profile.
4. RICH LIFE QUESTION: Before diagnosing, ask: "If money weren't a constraint, what would your life look like in 5 years?" Listen. This is the vision you'll connect every financial decision back to.
5. DIAGNOSE: Based on answers, tell them which Freedom Step they're on (call get_baby_steps):
   - Spending more than earning -> Step 0 (Stabilize): close the gap first.
   - No emergency fund -> Step 1 (Starter Safety Net): save 1 month of expenses.
   - Has debts at >10%% interest -> Step 2 (Kill Toxic Debt): sprint phase, 80/20.
   - Debt-free, small savings -> Step 3 (Full Safety Net): 3-6 months.
   - Solid safety net -> Step 4+ (invest, accelerate, rich life).
   Present this as a roadmap, not a judgment: "Here's where you are and here's the path. The good news? You're already on it."
6. THE ASK: Propose making their first deposit RIGHT NOW.
   - "Let's get your first NGN 20k in. The moment it lands, I'll split it -- 70%% to spend, 30%% to stash. That 30%% is the beginning of [their goal / their Rich Life vision]."
   - If hesitant, use send_poll: "What's holding you back? [Let's do it / How does it work? / I don't have NGN 20k / Maybe later]"
   - Address each objection warmly:
     - "How does it work?" -> explain the split, wallet, Face ID approval.
     - "I don't have NGN 20k" -> "Start with whatever you have. NGN 5k, 2k -- the habit matters more than the amount."
     - "Maybe later" -> "No pressure. But you already know your goal. I'll be here when you're ready."
   - If they linked their bank, reference the data: "You saw where your money goes. Now let's redirect some of it toward [their goal]."
7. WHEN THEY DEPOSIT: Call celebrate(level="big", title="First deposit!"). Explain the 70/30 split with their ACTUAL numbers.

CONVERSATION RULES:
- ONE question at a time. Never list multiple questions. Ask, wait, react, then ask the next.
- REACT before moving on. If they say they have NGN 500k in debt, acknowledge it before asking about income. "That's a lot to carry. How long have you been dealing with that?"
- ASK WHY. First answers are surface answers. Dig: "Why that goal?" "What would that change for you?"
- Let them go off-script. If they ask "can I invest in crypto?" mid-discovery, answer it, then steer back.
- Save as you go. Each answer triggers the appropriate tool call.
- Never invent numbers. If they didn't give a debt amount, ask.
- Use their currency. Nigeria = naira. Diaspora = dollars.
- NORMALIZE. Money carries shame. You dissolve it: "Most people have no idea what they spend. That's normal."
- If they linked their bank, USE THE DATA. Don't ask questions you already know the answer to. This makes Miriam feel smart and the user feels seen.

TONE: You're a friend who happens to be great with money. Curious, warm, never clinical. You ask because you genuinely want to know. You build trust by listening, not by having all the answers.`, greeting)
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
- If they haven't linked their bank, suggest it as a bridge to depositing: "Want me to connect to your bank? I'll show you exactly where your money goes. Sometimes seeing the picture makes the next step obvious." When they link and sync, call get_bank_statement_analysis to show their spending — then connect it to the deposit: "You're spending NGN X on [category]. What if even 10%% of that went to your stash instead?"`
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
