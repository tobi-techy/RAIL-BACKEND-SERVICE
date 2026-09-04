package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ai/metrics"
	"go.uber.org/zap"
)

// journeyBeatKey identifies one move in the onboarding arc. The backend owns
// which beats are done; Miriam owns how each beat sounds.
type journeyBeatKey string

const (
	beatStory   journeyBeatKey = "story"
	beatPicture journeyBeatKey = "picture"
	beatAha     journeyBeatKey = "aha"
	beatPath    journeyBeatKey = "path"
	beatDeposit journeyBeatKey = "deposit"
)

// journeyOrder is the canonical beat progression. First unfinished beat wins
// as the active objective; soft objectives over hard steps.
var journeyOrder = []journeyBeatKey{beatStory, beatPicture, beatAha, beatPath, beatDeposit}

// journeySignals bundles everything the resolver needs, gathered once per turn.
type journeySignals struct {
	user         *entities.UserProfile
	name         string
	phase        OnboardingPhase
	messageCount int
	hasFunded    bool
	depositCount int
	monoLinked   bool
	goal         *SavingsGoal
	profileGoal  string
}

// SetJourneyStore wires the cross-session journey state store.
func (o *AgentAdapter) SetJourneyStore(store JourneyStore) {
	o.journey = store
}

// gatherJourneySignals collects live state from existing providers in parallel
// where possible. Returns ok=false when the user can't be resolved.
func (o *AgentAdapter) gatherJourneySignals(ctx context.Context, userID uuid.UUID) (journeySignals, bool) {
	var sigs journeySignals

	provider, ok := o.userProfile.(FullUserProfileProvider)
	if !ok || provider == nil {
		return sigs, false
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	user, err := provider.GetProfile(fetchCtx, userID)
	if err != nil || user == nil {
		return sigs, false
	}
	sigs.user = user
	if user.FirstName != nil {
		sigs.name = strings.TrimSpace(*user.FirstName)
	}

	if o.workingMemory != nil {
		if wm := o.workingMemory.Get(fetchCtx, userID); wm != nil {
			sigs.messageCount = wm.MessageCount
		}
	}

	sigs.hasFunded = o.realtimeHasBalanceContext(fetchCtx, userID)

	if o.monoAnalysis != nil {
		if analysis, err := o.monoAnalysis.GetSpendingAnalysis(fetchCtx, userID, 1); err == nil && analysis != nil && analysis.TransactionCount > 0 {
			sigs.monoLinked = true
		}
	}

	if sigs.hasFunded && o.depositHistory != nil {
		if deps, err := o.depositHistory.GetByUserID(fetchCtx, userID, 50, 0); err == nil {
			sigs.depositCount = len(deps)
		}
	}

	if o.savingsGoalStore != nil {
		if goal, err := o.savingsGoalStore.Get(fetchCtx, userID); err == nil && goal != nil && goal.Name != "" {
			sigs.goal = goal
		}
	}

	if o.financialProfile != nil {
		if fp, err := o.financialProfile.GetByUserID(fetchCtx, userID); err == nil && fp != nil {
			sigs.profileGoal = strings.TrimSpace(fp.FinancialGoal)
		}
	}

	sigs.phase = classifyOnboardingPhase(user, sigs.messageCount, sigs.hasFunded, sigs.depositCount)
	return sigs, true
}

// syncJourneyState merges live signals into durable journey state, firing
// milestones exactly once and persisting the objective transition.
func (o *AgentAdapter) syncJourneyState(ctx context.Context, userID uuid.UUID, sigs journeySignals) *JourneyState {
	state, err := o.journey.Get(ctx, userID)
	if err != nil || state == nil {
		state = &JourneyState{UserID: userID.String()}
	}

	dirty := false

	if sigs.name != "" {
		before := state.Facts[JourneyFactName]
		state.SetFact(JourneyFactName, sigs.name, FactSourceSignup, 0.95)
		if state.Facts[JourneyFactName] != before {
			dirty = true
		}
	}

	motivation := ""
	source := FactSourceChat
	confidence := 0.7
	switch {
	case sigs.goal != nil:
		motivation = journeyMotivationValue(sigs.goal.Name, sigs.goal.Target)
		source = FactSourceGoalTool
		confidence = 0.9
	case sigs.profileGoal != "":
		motivation = sigs.profileGoal
		source = FactSourceGoalTool
		confidence = 0.85
	}
	if motivation != "" {
		before := state.Facts[JourneyFactMotivation]
		state.SetFact(JourneyFactMotivation, motivation, source, confidence)
		if state.Facts[JourneyFactMotivation] != before {
			dirty = true
			if state.ReachMilestone(MilestoneGoalCaptured) {
				metrics.ObserveJourneyMilestone(MilestoneGoalCaptured)
			}
		}
	}

	if sigs.monoLinked && state.ReachMilestone(MilestoneBankLinked) {
		metrics.ObserveJourneyMilestone(MilestoneBankLinked)
		dirty = true
	}
	if sigs.depositCount >= 1 && state.ReachMilestone(MilestoneFirstDeposit) {
		metrics.ObserveJourneyMilestone(MilestoneFirstDeposit)
		dirty = true
	}
	if sigs.depositCount >= 3 && state.ReachMilestone(MilestoneThirdDeposit) {
		metrics.ObserveJourneyMilestone(MilestoneThirdDeposit)
		dirty = true
	}

	if sigs.depositCount != state.LastDepositCount {
		state.LastDepositCount = sigs.depositCount
		dirty = true
	}

	objective := resolveJourneyObjective(state, sigs.phase)
	if objective != "" && objective != state.CurrentObjective {
		state.CurrentObjective = objective
		metrics.ObserveJourneyObjective(objective)
		dirty = true
	}

	if dirty {
		if err := o.journey.Save(ctx, state); err != nil && o.logger != nil {
			o.logger.Warn("journey state save failed", zap.Error(err), zap.String("user_id", userID.String()))
		}
	}
	return state
}

// resolveJourneyObjective picks the active objective: the first unfinished
// beat. Established users return "" (normal behaviour, no journey treatment).
func resolveJourneyObjective(state *JourneyState, phase OnboardingPhase) string {
	if phase == PhaseEstablished {
		return ""
	}
	for _, beat := range journeyOrder {
		if !journeyBeatDone(beat, state) {
			return journeyBeatObjective(beat)
		}
	}
	return JourneyObjectiveHabit
}

func journeyBeatObjective(beat journeyBeatKey) string {
	switch beat {
	case beatStory:
		return JourneyObjectiveStory
	case beatPicture:
		return JourneyObjectivePicture
	case beatAha:
		return JourneyObjectiveAha
	case beatPath:
		return JourneyObjectivePath
	case beatDeposit:
		return JourneyObjectiveDeposit
	default:
		return JourneyObjectiveHabit
	}
}

func journeyBeatDone(beat journeyBeatKey, state *JourneyState) bool {
	switch beat {
	case beatStory:
		return state.HasFact(JourneyFactMotivation)
	case beatPicture:
		return state.HasMilestone(MilestoneBankLinked)
	case beatAha:
		return state.HasMilestone(MilestoneStatementAnalyzed)
	case beatPath:
		return state.HasMilestone("path_named")
	case beatDeposit:
		return state.HasMilestone(MilestoneFirstDeposit)
	default:
		return false
	}
}

func journeyMotivationValue(name, target string) string {
	name = strings.TrimSpace(name)
	if target == "" || target == "0" {
		return name
	}
	return fmt.Sprintf("%s (target %s)", name, target)
}

// noteJourneyToolSuccess records milestones that can only be observed through
// tool execution: the statement-analysis aha moment and the freedom-step
// diagnosis. Called from executeTool after successful runs.
func (o *AgentAdapter) noteJourneyToolSuccess(ctx context.Context, userID uuid.UUID, toolName string) {
	if o.journey == nil {
		return
	}
	var milestone string
	switch toolName {
	case ToolGetBankStatementAnalysis:
		milestone = MilestoneStatementAnalyzed
	case ToolGetBabySteps:
		milestone = "path_named"
	default:
		return
	}

	hookCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	state, err := o.journey.Get(hookCtx, userID)
	if err != nil || state == nil {
		state = &JourneyState{UserID: userID.String()}
	}
	if state.ReachMilestone(milestone) {
		metrics.ObserveJourneyMilestone(milestone)
		if err := o.journey.Save(hookCtx, state); err != nil && o.logger != nil {
			o.logger.Warn("journey milestone save failed", zap.Error(err), zap.String("user_id", userID.String()))
		}
	}
}

// buildJourneyBlock renders the dynamic onboarding guidance. Returns "" when
// the journey engine shouldn't drive this turn, letting the caller fall back
// to static phase guidance.
func (o *AgentAdapter) buildJourneyBlock(ctx context.Context, userID uuid.UUID, sigs journeySignals) string {
	if sigs.phase != PhaseFirstConversation &&
		sigs.phase != PhaseOnboardedNotFunded &&
		sigs.phase != PhaseFundedNewbie {
		return ""
	}

	syncCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	state := o.syncJourneyState(syncCtx, userID, sigs)
	objective := state.CurrentObjective
	if objective == "" {
		objective = resolveJourneyObjective(state, sigs.phase)
	}

	if sigs.user == nil {
		return ""
	}

	header := buildOnboardingHeader(sigs.user, sigs.phase, sigs.messageCount, sigs.hasFunded, sigs.depositCount, sigs.monoLinked)

	var b strings.Builder
	b.WriteString(header + ".\n\n")
	b.WriteString("JOURNEY (backend-tracked state). Trust this over conversation memory.\n")
	b.WriteString("OBJECTIVE: " + journeyObjectiveLabel(objective) + "\n")

	b.WriteString("KNOWN (never ask for anything listed here):\n")
	wroteKnown := false
	if f, ok := state.Facts[JourneyFactName]; ok {
		b.WriteString(fmt.Sprintf("- their name is %s (from signup)\n", f.Value))
		wroteKnown = true
	}
	if f, ok := state.Facts[JourneyFactMotivation]; ok {
		b.WriteString(fmt.Sprintf("- what their money is for: %q (%s)\n", f.Value, journeySourceLabel(f.Source)))
		wroteKnown = true
	}
	if !wroteKnown {
		b.WriteString("- nothing yet; you're starting from zero, and that's fine\n")
	}

	if missing := journeyMissingLine(state); missing != "" {
		b.WriteString("NOT KNOWN YET: " + missing + "\n")
	}

	b.WriteString("PROGRESS:\n")
	for i, beat := range journeyOrder {
		status := "later"
		switch {
		case journeyBeatDone(beat, state):
			status = "DONE: do not repeat or re-frame this"
		case journeyBeatObjective(beat) == objective:
			status = "THIS TURN'S MOVE"
		}
		b.WriteString(fmt.Sprintf("%d. %s: %s\n", i+1, journeyBeatLabel(beat), status))
	}

	b.WriteString("RULES THIS TURN: advance at most one beat. React to what they actually said before steering back. One question per turn. If they change subject, follow them; the objective waits, it doesn't chase.")
	if !sigs.monoLinked && sigs.messageCount > 2 {
		b.WriteString(" If they've already declined the bank link, do NOT re-push it this turn; work with manual discovery instead.")
	}
	if sigs.messageCount == 0 && time.Since(sigs.user.CreatedAt) < 10*time.Minute {
		b.WriteString(" They JUST provisioned the account and already met you. Never re-introduce yourself; pick up from whatever they said.")
	}
	b.WriteString("\n]")

	return b.String()
}

func journeyObjectiveLabel(objective string) string {
	switch objective {
	case JourneyObjectiveStory:
		return "learn what their money is actually for: one human question, mechanics come later"
	case JourneyObjectivePicture:
		return "get eyes on their real cash flow: offer connect_bank as help, never a gate"
	case JourneyObjectiveAha:
		return "turn their data into one genuine insight: get_bank_statement_analysis NOW if linked, one observation, not an audit"
	case JourneyObjectivePath:
		return "name their position: get_baby_steps, one sentence on which Freedom Step, then the path"
	case JourneyObjectiveDeposit:
		return "first deposit: tie THE ASK to their own words, make it feel obvious rather than sold"
	case JourneyObjectiveHabit:
		return "build the habit: reinforce the split, celebrate real wins, propose automation when earned"
	default:
		return objective
	}
}

func journeyBeatLabel(beat journeyBeatKey) string {
	switch beat {
	case beatStory:
		return "the money-for question"
	case beatPicture:
		return "offer the real picture (connect_bank)"
	case beatAha:
		return "first insight (get_bank_statement_analysis)"
	case beatPath:
		return "freedom-step diagnosis (get_baby_steps)"
	case beatDeposit:
		return "THE ASK (first deposit)"
	default:
		return string(beat)
	}
}

func journeyMissingLine(state *JourneyState) string {
	switch {
	case !state.HasFact(JourneyFactMotivation):
		return "what their money is actually for"
	case !state.HasMilestone(MilestoneBankLinked):
		return "what their spending actually looks like (no bank connected)"
	case !state.HasMilestone(MilestoneStatementAnalyzed):
		return "the patterns inside their spending (data landed, not yet read together)"
	case !state.HasMilestone(MilestoneFirstDeposit):
		return "skin in the game (nothing saved yet)"
	default:
		return ""
	}
}

func journeySourceLabel(source string) string {
	switch source {
	case FactSourceGoalTool:
		return "they told you, saved"
	case FactSourceChat:
		return "their words"
	case FactSourceMono, FactSourceStatement:
		return "verified from their data"
	case FactSourceSignup:
		return "from signup"
	default:
		return source
	}
}
