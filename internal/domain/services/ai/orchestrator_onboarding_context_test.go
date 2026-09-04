package ai

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

func TestClassifyOnboardingPhase(t *testing.T) {
	now := time.Now()
	recentSignup := now.Add(-2 * 24 * time.Hour)
	oldSignup := now.Add(-60 * 24 * time.Hour)

	tests := []struct {
		name          string
		user          *entities.UserProfile
		messageCount  int
		hasFunded     bool
		depositCount  int
		expectedPhase OnboardingPhase
	}{
		{
			name: "first conversation, zero messages",
			user: &entities.UserProfile{
				ID:               uuid.New(),
				OnboardingStatus: entities.OnboardingStatusCompleted,
				KYCStatus:        "non_kyc",
				CreatedAt:        recentSignup,
			},
			messageCount:  0,
			hasFunded:     false,
			expectedPhase: PhaseFirstConversation,
		},
		{
			name: "first conversation even if onboarding incomplete",
			user: &entities.UserProfile{
				ID:               uuid.New(),
				OnboardingStatus: entities.OnboardingStatusStarted,
				KYCStatus:        "pending",
				CreatedAt:        recentSignup,
			},
			messageCount:  0,
			hasFunded:     false,
			expectedPhase: PhaseFirstConversation,
		},
		{
			name: "onboarding incomplete, started status",
			user: &entities.UserProfile{
				ID:               uuid.New(),
				OnboardingStatus: entities.OnboardingStatusStarted,
				KYCStatus:        "pending",
				CreatedAt:        recentSignup,
			},
			messageCount:  3,
			hasFunded:     false,
			expectedPhase: PhaseOnboardingIncomplete,
		},
		{
			name: "onboarding incomplete, basic_complete status",
			user: &entities.UserProfile{
				ID:               uuid.New(),
				OnboardingStatus: entities.OnboardingStatusBasicComplete,
				KYCStatus:        "non_kyc",
				CreatedAt:        recentSignup,
			},
			messageCount:  2,
			hasFunded:     false,
			expectedPhase: PhaseOnboardingIncomplete,
		},
		{
			name: "onboarding incomplete, wallets pending",
			user: &entities.UserProfile{
				ID:               uuid.New(),
				OnboardingStatus: entities.OnboardingStatusWalletsPending,
				KYCStatus:        "non_kyc",
				CreatedAt:        recentSignup,
			},
			messageCount:  1,
			hasFunded:     false,
			expectedPhase: PhaseOnboardingIncomplete,
		},
		{
			name: "onboarded not funded, recent signup",
			user: &entities.UserProfile{
				ID:               uuid.New(),
				OnboardingStatus: entities.OnboardingStatusCompleted,
				KYCStatus:        "approved",
				CreatedAt:        recentSignup,
			},
			messageCount:  5,
			hasFunded:     false,
			expectedPhase: PhaseOnboardedNotFunded,
		},
		{
			name: "onboarded not funded, old signup = established (dormant)",
			user: &entities.UserProfile{
				ID:               uuid.New(),
				OnboardingStatus: entities.OnboardingStatusCompleted,
				KYCStatus:        "approved",
				CreatedAt:        oldSignup,
			},
			messageCount:  10,
			hasFunded:     false,
			expectedPhase: PhaseEstablished,
		},
		{
			name: "funded newbie, 1 deposit",
			user: &entities.UserProfile{
				ID:               uuid.New(),
				OnboardingStatus: entities.OnboardingStatusCompleted,
				KYCStatus:        "approved",
				CreatedAt:        recentSignup,
			},
			messageCount:  4,
			hasFunded:     true,
			depositCount:  1,
			expectedPhase: PhaseFundedNewbie,
		},
		{
			name: "funded newbie, 2 deposits",
			user: &entities.UserProfile{
				ID:               uuid.New(),
				OnboardingStatus: entities.OnboardingStatusCompleted,
				KYCStatus:        "non_kyc",
				CreatedAt:        recentSignup,
			},
			messageCount:  6,
			hasFunded:     true,
			depositCount:  2,
			expectedPhase: PhaseFundedNewbie,
		},
		{
			name: "established, 3+ deposits",
			user: &entities.UserProfile{
				ID:               uuid.New(),
				OnboardingStatus: entities.OnboardingStatusCompleted,
				KYCStatus:        "approved",
				CreatedAt:        oldSignup,
			},
			messageCount:  20,
			hasFunded:     true,
			depositCount:  5,
			expectedPhase: PhaseEstablished,
		},
		{
			name: "established, 3 deposits exactly",
			user: &entities.UserProfile{
				ID:               uuid.New(),
				OnboardingStatus: entities.OnboardingStatusCompleted,
				KYCStatus:        "approved",
				CreatedAt:        recentSignup,
			},
			messageCount:  8,
			hasFunded:     true,
			depositCount:  3,
			expectedPhase: PhaseEstablished,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase := classifyOnboardingPhase(tt.user, tt.messageCount, tt.hasFunded, tt.depositCount)
			if phase != tt.expectedPhase {
				t.Errorf("classifyOnboardingPhase() = %s, want %s", phase, tt.expectedPhase)
			}
		})
	}
}

func TestFormatOnboardingContextBlock(t *testing.T) {
	now := time.Now()
	firstName := "Tobi"
	user := &entities.UserProfile{
		ID:               uuid.New(),
		FirstName:        &firstName,
		OnboardingStatus: entities.OnboardingStatusCompleted,
		KYCStatus:        "non_kyc",
		CreatedAt:        now.Add(-1 * 24 * time.Hour),
	}

	t.Run("first conversation includes discovery guidance", func(t *testing.T) {
		block := formatOnboardingContextBlock(user, PhaseFirstConversation, 0, false, 0, false)
		if block == "" {
			t.Fatal("expected non-empty context block for first conversation")
		}
		if !strings.Contains(block, "[ONBOARDING STATUS") {
			t.Error("expected [ONBOARDING STATUS] header")
		}
		if !strings.Contains(block, "first_conversation") {
			t.Error("expected first_conversation phase in block")
		}
		if !strings.Contains(block, "Tobi") {
			t.Error("expected user name in block")
		}
		if !strings.Contains(block, "connect_bank") {
			t.Error("expected connect_bank in first conversation block")
		}
		if !strings.Contains(block, "ONE HUMAN QUESTION BEFORE MECHANICS") {
			t.Error("expected human question beat in first conversation block")
		}
		if !strings.Contains(strings.ToLower(block), "one question at a time") {
			t.Error("expected one-question-at-a-time rule in guidance")
		}
	})

	t.Run("onboarded not funded includes activation guidance", func(t *testing.T) {
		block := formatOnboardingContextBlock(user, PhaseOnboardedNotFunded, 5, false, 0, false)
		if block == "" {
			t.Fatal("expected non-empty context block for onboarded not funded")
		}
		if !strings.Contains(block, "onboarded_not_funded") {
			t.Error("expected onboarded_not_funded phase in block")
		}
		if !strings.Contains(block, "#1 drop-off") {
			t.Error("expected drop-off warning in guidance")
		}
		if !strings.Contains(block, "first deposit") {
			t.Error("expected first deposit guidance")
		}
	})

	t.Run("funded newbie includes habit-building guidance", func(t *testing.T) {
		block := formatOnboardingContextBlock(user, PhaseFundedNewbie, 4, true, 1, false)
		if block == "" {
			t.Fatal("expected non-empty context block for funded newbie")
		}
		if !strings.Contains(block, "funded_newbie") {
			t.Error("expected funded_newbie phase in block")
		}
		if !strings.Contains(block, "honeymoon") {
			t.Error("expected honeymoon period reference in guidance")
		}
		if !strings.Contains(block, "70/30") {
			t.Error("expected 70/30 split reference in guidance")
		}
	})

	t.Run("established returns empty", func(t *testing.T) {
		block := formatOnboardingContextBlock(user, PhaseEstablished, 20, true, 5, false)
		if block != "" {
			t.Errorf("expected empty block for established user, got: %s", block)
		}
	})

	t.Run("onboarding incomplete includes setup guidance", func(t *testing.T) {
		incompleteUser := &entities.UserProfile{
			ID:               uuid.New(),
			FirstName:        &firstName,
			OnboardingStatus: entities.OnboardingStatusStarted,
			KYCStatus:        "pending",
			CreatedAt:        now.Add(-1 * 24 * time.Hour),
		}
		block := formatOnboardingContextBlock(incompleteUser, PhaseOnboardingIncomplete, 2, false, 0, false)
		if block == "" {
			t.Fatal("expected non-empty context block for onboarding incomplete")
		}
		if !strings.Contains(block, "onboarding_incomplete") {
			t.Error("expected onboarding_incomplete phase in block")
		}
		if !strings.Contains(block, "finish setting up") {
			t.Error("expected finish setup guidance")
		}
	})
}

func TestFirstConversationGuidance(t *testing.T) {
	t.Run("with name", func(t *testing.T) {
		guidance := firstConversationGuidance("Ada")
		if !strings.Contains(guidance, "Ada") {
			t.Error("expected name in first-conversation guidance")
		}
	})

	t.Run("human question before bank", func(t *testing.T) {
		guidance := firstConversationGuidance("Test")
		human := strings.Index(guidance, "ONE HUMAN QUESTION BEFORE MECHANICS")
		bank := strings.Index(guidance, "connect_bank")
		if human < 0 || bank < 0 || human > bank {
			t.Error("expected human question beat before connect_bank")
		}
	})

	t.Run("contains aha + tools", func(t *testing.T) {
		guidance := firstConversationGuidance("Test")
		for _, s := range []string{
			"connect_bank",
			"get_bank_statement_analysis",
			"AHA MOMENT",
			"repeat-back pattern",
			"money dial",
			"What did money feel like growing up?",
			"send_poll",
			"get_baby_steps",
			"just_provisioned",
			"THE ASK",
			"Never invent numbers",
			"ASK WHY",
			"NORMALIZE",
		} {
			if !strings.Contains(guidance, s) {
				t.Errorf("expected %q in guidance", s)
			}
		}
	})

	t.Run("does not dump a four-question intake", func(t *testing.T) {
		guidance := firstConversationGuidance("Test")
		if strings.Contains(guidance, "What's your income like?") {
			t.Error("first conversation should not interrogate income up front")
		}
	})
}

func TestOnboardingBuildsPersonalCommitmentBeforeCSPCommit(t *testing.T) {
	guidance := firstConversationGuidance("")
	for _, want := range []string{
		"why it matters now",
		"who benefits",
		"build_conscious_spending_plan",
		"biggest mismatch",
		"protected money dial",
		"commit_conscious_spending_plan",
		"moves no money",
	} {
		if !strings.Contains(guidance, want) {
			t.Errorf("first-conversation guidance missing %q", want)
		}
	}
}
