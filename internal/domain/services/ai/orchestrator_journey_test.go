package ai

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type fakeJourneyStore struct {
	state *JourneyState
	saves int
}

func (f *fakeJourneyStore) Get(_ context.Context, userID uuid.UUID) (*JourneyState, error) {
	if f.state == nil {
		return nil, nil
	}
	return f.state, nil
}

func (f *fakeJourneyStore) Save(_ context.Context, state *JourneyState) error {
	f.state = state
	f.saves++
	return nil
}

func TestResolveJourneyObjective(t *testing.T) {
	completed := func(milestones ...string) *JourneyState {
		s := &JourneyState{UserID: uuid.New().String()}
		for _, m := range milestones {
			s.ReachMilestone(m)
		}
		return s
	}
	withMotivation := func(s *JourneyState) *JourneyState {
		s.SetFact(JourneyFactMotivation, "freedom fund", FactSourceGoalTool, 0.9)
		return s
	}

	tests := []struct {
		name     string
		state    *JourneyState
		phase    OnboardingPhase
		expected string
	}{
		{"fresh user starts at story", &JourneyState{}, PhaseFirstConversation, JourneyObjectiveStory},
		{
			"motivation known moves to picture",
			withMotivation(&JourneyState{}),
			PhaseFirstConversation,
			JourneyObjectivePicture,
		},
		{
			"bank linked moves to aha",
			withMotivation(completed(MilestoneBankLinked)),
			PhaseFirstConversation,
			JourneyObjectiveAha,
		},
		{
			"statement analyzed moves to path",
			withMotivation(completed(MilestoneBankLinked, MilestoneStatementAnalyzed)),
			PhaseFirstConversation,
			JourneyObjectivePath,
		},
		{
			"path named moves to deposit",
			withMotivation(completed(MilestoneBankLinked, MilestoneStatementAnalyzed, "path_named")),
			PhaseOnboardedNotFunded,
			JourneyObjectiveDeposit,
		},
		{
			"deposited moves to habit",
			withMotivation(completed(MilestoneBankLinked, MilestoneStatementAnalyzed, "path_named", MilestoneFirstDeposit)),
			PhaseFundedNewbie,
			JourneyObjectiveHabit,
		},
		{"established users exit the journey", withMotivation(&JourneyState{}), PhaseEstablished, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveJourneyObjective(tt.state, tt.phase)
			if got != tt.expected {
				t.Errorf("resolveJourneyObjective() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestJourneyFactProvenance(t *testing.T) {
	t.Run("stronger source upgrades weaker fact", func(t *testing.T) {
		s := &JourneyState{}
		s.SetFact(JourneyFactIncome, "about 300k", FactSourceChat, 0.7)
		s.SetFact(JourneyFactIncome, "NGN 312000/mo avg", FactSourceStatement, 0.95)

		fact := s.Facts[JourneyFactIncome]
		if fact.Source != FactSourceStatement {
			t.Errorf("expected statement source to win, got %q", fact.Source)
		}
	})

	t.Run("weaker source cannot overwrite stronger fact", func(t *testing.T) {
		s := &JourneyState{}
		s.SetFact(JourneyFactIncome, "NGN 312000/mo avg", FactSourceStatement, 0.95)
		s.SetFact(JourneyFactIncome, "idk maybe 100k", FactSourceChat, 0.9)

		fact := s.Facts[JourneyFactIncome]
		if fact.Value != "NGN 312000/mo avg" {
			t.Errorf("chat mention downgraded verified data: %q", fact.Value)
		}
	})

	t.Run("same source requires higher confidence", func(t *testing.T) {
		s := &JourneyState{}
		s.SetFact(JourneyFactMotivation, "save more", FactSourceChat, 0.8)
		s.SetFact(JourneyFactMotivation, "save more for move-out", FactSourceChat, 0.6)

		if s.Facts == nil || s.Facts[JourneyFactMotivation].Value != "save more" {
			t.Error("lower-confidence repeat overwrote original fact")
		}
	})

	t.Run("empty values are ignored", func(t *testing.T) {
		s := &JourneyState{}
		s.SetFact(JourneyFactName, "", FactSourceSignup, 0.95)
		if len(s.Facts) != 0 {
			t.Error("empty fact was stored")
		}
	})
}

func journeyTestSignals() journeySignals {
	first := "Joseph"
	created := time.Now().Add(-48 * time.Hour)
	return journeySignals{
		user: &entities.UserProfile{
			FirstName:        &first,
			CreatedAt:        created,
			OnboardingStatus: entities.OnboardingStatusCompleted,
			KYCStatus:        "approved",
		},
		name:         first,
		phase:        PhaseFirstConversation,
		messageCount: 3,
		hasFunded:    false,
		depositCount: 0,
		monoLinked:   false,
		goal:         &SavingsGoal{Name: "Freedom fund", Target: "1500000", CreatedAt: time.Now().Format(time.RFC3339)},
		profileGoal:  "",
	}
}

func TestBuildJourneyBlockNeverReasks(t *testing.T) {
	store := &fakeJourneyStore{}
	o := &AgentAdapter{journey: store}
	sigs := journeyTestSignals()

	block := o.buildJourneyBlock(context.Background(), uuid.New(), sigs)
	if block == "" {
		t.Fatal("expected non-empty journey block")
	}

	if !strings.Contains(block, "never ask") {
		t.Error("block must instruct Miriam not to re-ask known facts")
	}
	if !strings.Contains(block, "Joseph") {
		t.Error("block should carry the known name")
	}
	if !strings.Contains(block, "Freedom fund") {
		t.Error("block should surface the captured motivation")
	}
	if !strings.Contains(block, "do not repeat") {
		t.Error("completed beats must be marked done")
	}

	// Story is done (goal captured); the active move must be the bank link.
	storyIdx := strings.Index(block, journeyBeatLabel(beatStory))
	pictureIdx := strings.Index(block, journeyBeatLabel(beatPicture))
	if storyIdx < 0 || pictureIdx < 0 {
		t.Fatalf("progress lines missing: story=%d picture=%d", storyIdx, pictureIdx)
	}
	storySection := block[storyIdx:pictureIdx]
	if strings.Contains(storySection, "THIS TURN'S MOVE") {
		t.Error("story beat marked active although motivation is already known")
	}
	if !strings.Contains(pictureIdxSection(block, pictureIdx), "THIS TURN'S MOVE") {
		t.Error("picture beat should be this turn's move after goal capture")
	}

	if strings.Contains(block, "re-introduce yourself") && sigs.messageCount > 0 {
		t.Error("just-provisioned rule leaked into a returning user's block")
	}
}

func pictureIdxSection(block string, idx int) string {
	end := idx + 220
	if end > len(block) {
		end = len(block)
	}
	return block[idx:end]
}

func TestBuildJourneyBlockJustProvisioned(t *testing.T) {
	store := &fakeJourneyStore{}
	o := &AgentAdapter{journey: store}
	sigs := journeyTestSignals()
	sigs.messageCount = 0
	sigs.user.CreatedAt = time.Now().Add(-2 * time.Minute)
	sigs.goal = nil // nothing captured yet

	block := o.buildJourneyBlock(context.Background(), uuid.New(), sigs)
	if !strings.Contains(strings.ToLower(block), "never re-introduce yourself") {
		t.Error("fresh provisioned user should carry the no-reintroduction rule")
	}
	if !strings.Contains(block, "THIS TURN'S MOVE") || !strings.Contains(strings.ToLower(block), "money-for question") {
		t.Error("fresh user's active beat should be the money-for question")
	}
}

func TestSyncJourneyStateMilestonesIdempotent(t *testing.T) {
	store := &fakeJourneyStore{}
	o := &AgentAdapter{journey: store}
	uid := uuid.New()
	sigs := journeyTestSignals()
	sigs.monoLinked = true
	sigs.depositCount = 1

	state := o.syncJourneyState(context.Background(), uid, sigs)
	if !state.HasMilestone(MilestoneBankLinked) || !state.HasMilestone(MilestoneFirstDeposit) {
		t.Fatal("expected bank_linked and first_deposit milestones on first sync")
	}
	if state.CurrentObjective == "" {
		t.Fatal("objective should be resolved on first sync")
	}
	firstSaves := store.saves
	if firstSaves == 0 {
		t.Fatal("first sync should persist state")
	}

	// Second sync with identical signals must be a no-op.
	state2 := o.syncJourneyState(context.Background(), uid, sigs)
	if store.saves != firstSaves {
		t.Errorf("unchanged signals triggered extra saves: %d -> %d", firstSaves, store.saves)
	}
	if state2.LastDepositCount != 1 {
		t.Errorf("deposit watermark = %d, want 1", state2.LastDepositCount)
	}
}

func TestSyncJourneyStateCapturesMotivation(t *testing.T) {
	store := &fakeJourneyStore{}
	o := &AgentAdapter{journey: store}
	uid := uuid.New()

	state := o.syncJourneyState(context.Background(), uid, journeyTestSignals())

	fact, ok := state.Facts[JourneyFactMotivation]
	if !ok {
		t.Fatal("motivation fact not captured from savings goal")
	}
	if fact.Source != FactSourceGoalTool {
		t.Errorf("motivation source = %q, want %q", fact.Source, FactSourceGoalTool)
	}
	if !state.HasMilestone(MilestoneGoalCaptured) {
		t.Error("goal_captured milestone missing")
	}
	if !state.HasFact(JourneyFactName) {
		t.Error("name fact not captured")
	}
}

func TestNoteJourneyToolSuccess(t *testing.T) {
	store := &fakeJourneyStore{}
	o := &AgentAdapter{journey: store}
	uid := uuid.New()

	o.noteJourneyToolSuccess(context.Background(), uid, ToolGetBankStatementAnalysis)
	o.noteJourneyToolSuccess(context.Background(), uid, ToolGetBabySteps)
	o.noteJourneyToolSuccess(context.Background(), uid, ToolGetAccountSummary)

	state, err := store.Get(context.Background(), uid)
	if err != nil {
		t.Fatalf("store.Get returned error: %v", err)
	}
	if state == nil || !state.HasMilestone(MilestoneStatementAnalyzed) {
		t.Fatal("statement_analyzed milestone not recorded")
	}
	if !state.HasMilestone("path_named") {
		t.Fatal("path_named milestone not recorded")
	}
	if state.HasMilestone(MilestoneBankLinked) {
		t.Error("unrelated tool fired a milestone")
	}

	savesAfterFirstPass := store.saves
	o.noteJourneyToolSuccess(context.Background(), uid, ToolGetBabySteps)
	if store.saves != savesAfterFirstPass {
		t.Error("repeat milestone re-saved state")
	}
}

func TestEstablishedUsersBypassJourney(t *testing.T) {
	store := &fakeJourneyStore{}
	o := &AgentAdapter{journey: store}
	sigs := journeyTestSignals()
	sigs.phase = PhaseEstablished

	if block := o.buildJourneyBlock(context.Background(), uuid.New(), sigs); block != "" {
		t.Errorf("established users should get no journey block, got: %s", block)
	}
}
