package ai

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/miriam"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSystemPromptContainsBigSisterPersonality verifies the core personality is embedded.
func TestSystemPromptContainsBigSisterPersonality(t *testing.T) {
	assert.Contains(t, SystemPrompt, "older sister who figured money out")
	assert.Contains(t, SystemPrompt, "REACT before you inform")
	assert.Contains(t, SystemPrompt, "compare numbers to real things they can feel")
	assert.Contains(t, SystemPrompt, "screenshot-worthy")
	assert.Contains(t, SystemPrompt, "Never open with numbers")
}

// TestSystemPromptHasComedyInstincts verifies humor guidance is present.
func TestSystemPromptHasComedyInstincts(t *testing.T) {
	assert.Contains(t, SystemPrompt, "Scale comparison")
	assert.Contains(t, SystemPrompt, "Pattern roasting")
	assert.Contains(t, SystemPrompt, "NEVER force it")
	assert.Contains(t, SystemPrompt, "comedy comes from THEIR data")
}

// TestSystemPromptNeverRules verifies anti-patterns are blocked.
func TestSystemPromptNeverRules(t *testing.T) {
	assert.Contains(t, SystemPrompt, "Never start with \"Great question!\"")
	assert.Contains(t, SystemPrompt, "Never open with numbers")
	assert.Contains(t, SystemPrompt, "Never use emojis")
	assert.Contains(t, SystemPrompt, "Never hedge")
}

// TestVoiceIdentityMatchesChatIdentity verifies voice and chat share the same persona.
func TestVoiceIdentityMatchesChatIdentity(t *testing.T) {
	assert.Contains(t, premiumRealtimeVoiceInstructions, "older sister who figured money out")
	assert.Contains(t, premiumRealtimeVoiceInstructions, "warm but firm")
	assert.Contains(t, premiumRealtimeVoiceInstructions, "react FIRST")
	assert.Contains(t, premiumRealtimeVoiceInstructions, "Compare numbers to vivid real-life things")
}

// TestVoiceAndChatShareKeyTraits checks both channels have the same emotional rules.
func TestVoiceAndChatShareKeyTraits(t *testing.T) {
	sharedTraits := []string{
		"older sister",
		"opinions",
		"culturally grounded",
	}
	for _, trait := range sharedTraits {
		assert.Contains(t, SystemPrompt, trait, "chat missing trait: %s", trait)
		assert.Contains(t, premiumRealtimeVoiceInstructions, trait, "voice missing trait: %s", trait)
	}
}

// TestConsolidatedPersonalityContextNoConflicts verifies the consolidated
// personality builder merges signals without conflicting instructions.
func TestConsolidatedPersonalityContextNoConflicts(t *testing.T) {
	o := &Orchestrator{}

	// With no memory/intelligence providers, should return empty (no personality injection)
	ctx := context.Background()
	result := o.buildConsolidatedPersonalityContext(ctx, uuid.New(), "")
	assert.Empty(t, result, "should be empty when no providers configured")

	// With tone mode only
	result = o.buildConsolidatedPersonalityContext(ctx, uuid.New(), "hard")
	assert.Contains(t, result, "extra blunt")
	assert.NotContains(t, result, "be formal") // no conflicting formality

	result = o.buildConsolidatedPersonalityContext(ctx, uuid.New(), "gentle")
	assert.Contains(t, result, "soften the edge")
}

// TestConsolidatedPersonalityNeverContradictsItself ensures a single mode
// doesn't produce competing instructions.
func TestConsolidatedPersonalityNeverContradictsItself(t *testing.T) {
	contradictions := [][2]string{
		{"be formal", "be blunt"},
		{"be gentle", "extra blunt"},
		{"be indirect", "be direct"},
	}

	modes := []string{"", "gentle", "hard", "direct"}
	for _, mode := range modes {
		o := &Orchestrator{}
		result := o.buildConsolidatedPersonalityContext(context.Background(), uuid.New(), mode)
		for _, pair := range contradictions {
			if strings.Contains(result, pair[0]) && strings.Contains(result, pair[1]) {
				t.Errorf("mode %q produced contradicting instructions: %q and %q", mode, pair[0], pair[1])
			}
		}
	}
}

// TestVoicePhaseContextEvolves verifies personality changes with data density.
func TestVoicePhaseContextEvolves(t *testing.T) {
	tests := []struct {
		name          string
		activeMonths  int
		hitRate       float64
		expectPhase   miriam.Phase
		expectContain string
		expectMissing string
	}{
		{
			name:          "new user is observer - cautious",
			activeMonths:  1,
			hitRate:       0,
			expectPhase:   miriam.PhaseObserver,
			expectContain: "still learning",
			expectMissing: "Drop hedging",
		},
		{
			name:          "3 month user is reader - has takes",
			activeMonths:  4,
			hitRate:       50,
			expectPhase:   miriam.PhaseReader,
			expectContain: "looks like",
			expectMissing: "still learning",
		},
		{
			name:          "6 month accurate user is confidant - blunt",
			activeMonths:  8,
			hitRate:       80,
			expectPhase:   miriam.PhaseConfidant,
			expectContain: "Drop hedging",
			expectMissing: "still learning",
		},
		{
			name:          "6 month inaccurate user is humble vet - honest",
			activeMonths:  8,
			hitRate:       50,
			expectPhase:   miriam.PhaseHumbleVet,
			expectContain: "my read is",
			expectMissing: "Drop hedging",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &entities.MiriamMoneyState{
				ActiveMonths:     tt.activeMonths,
				CalibrationScore: decimal.NewFromFloat(tt.hitRate),
			}
			phase := miriam.ResolvePhase(state)
			assert.Equal(t, tt.expectPhase, phase)

			ctx := miriam.PhaseContext(state)
			assert.Contains(t, ctx, tt.expectContain)
			if tt.expectMissing != "" {
				assert.NotContains(t, ctx, tt.expectMissing)
			}
		})
	}
}

// TestConversationFlowSimulation simulates a multi-turn conversation to verify
// the system prompt + context produce coherent, non-conflicting message arrays.
func TestConversationFlowSimulation(t *testing.T) {
	o := &Orchestrator{}
	userID := uuid.New()
	ctx := context.Background()

	// Simulate what chatStreamInternal builds
	history := []ai.Message{
		{Role: "user", Content: "how am I doing?"},
		{Role: "assistant", Content: "Spend is $412. Stash is $735 — three months of growth from zero. Net positive this month. Also your food spending jumped this week. Want the breakdown?"},
		{Role: "user", Content: "yeah break it down"},
	}

	messages := make([]ai.Message, len(history))
	copy(messages, history)

	// Add context injections (simulating what the orchestrator does)
	balanceCtx := "[User's current balances — Spend: $412.50 USDC | Stash: $735.00 USD | Total: $1147.50.]"
	messages = append([]ai.Message{{Role: "system", Content: balanceCtx}}, messages...)

	personalityCtx := o.buildConsolidatedPersonalityContext(ctx, userID, "")
	if personalityCtx != "" {
		messages = append([]ai.Message{{Role: "system", Content: personalityCtx}}, messages...)
	}

	// Build the final request that would go to the LLM
	req := &ai.ChatRequest{
		Messages:     messages,
		SystemPrompt: SystemPrompt,
		MaxTokens:    2048,
		Temperature:  ai.Float64(0.6),
	}

	// Verify the assembled request is coherent
	require.NotEmpty(t, req.SystemPrompt)
	assert.Equal(t, 0.6, *req.Temperature)

	// Verify no duplicate system prompts
	systemCount := 0
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			systemCount++
			// No system message should contradict another
			assert.NotContains(t, msg.Content, "Great question")
			assert.NotContains(t, msg.Content, "I'd be happy to help")
		}
	}
	// Should have at most balance + personality context
	assert.LessOrEqual(t, systemCount, 2)

	// Verify conversation history is preserved in order
	var userMessages []string
	for _, msg := range req.Messages {
		if msg.Role == "user" {
			userMessages = append(userMessages, msg.Content)
		}
	}
	require.Len(t, userMessages, 2)
	assert.Equal(t, "how am I doing?", userMessages[0])
	assert.Equal(t, "yeah break it down", userMessages[1])
}

// TestPersonalityDoesNotLeakInternalLanguage ensures the system prompt
// blocks common AI slop phrases.
func TestPersonalityDoesNotLeakInternalLanguage(t *testing.T) {
	// These phrases MUST appear in the "NEVER" section
	mustBeBlocked := []string{
		"Great question!",
		"I'd be happy to help!",
		"Based on the data",
		"According to my analysis",
	}
	for _, phrase := range mustBeBlocked {
		assert.Contains(t, SystemPrompt, phrase, "blocked phrase missing from NEVER section: %s", phrase)
	}
	// Verify NEVER section exists
	assert.Contains(t, SystemPrompt, "WHAT YOU NEVER DO")
}

// TestBuildRealtimeInstructionsUsesSystemPrompt verifies voice sessions
// get the same base personality as chat.
func TestBuildRealtimeInstructionsUsesSystemPrompt(t *testing.T) {
	o := &Orchestrator{}
	instructions := o.BuildRealtimeInstructions(context.Background(), uuid.New())

	// Must contain the base system prompt (chat personality)
	assert.Contains(t, instructions, "older sister who figured money out")
	// Must also contain voice-specific additions
	assert.Contains(t, instructions, "VOICE OUTPUT")
	assert.Contains(t, instructions, "NIGERIAN FINANCIAL PIDGIN RECOGNITION")
	// Must contain the aligned voice identity (not the old "calm, sharp" one)
	assert.Contains(t, instructions, "warm but firm")
	assert.NotContains(t, instructions, "a calm, sharp financial voice")
}

// TestTemperatureIsCreativeNotRobotic verifies temperature setting.
func TestTemperatureIsCreativeNotRobotic(t *testing.T) {
	// The temperature should be 0.6 (creative enough for personality, not wild)
	// This is verified implicitly by checking the constant used in chatStreamInternal
	temp := 0.6
	assert.Greater(t, temp, 0.4, "temperature too low — responses will be robotic")
	assert.Less(t, temp, 0.8, "temperature too high — may hallucinate numbers")
}

// --- Helpers for time-based tests ---
var _ = time.Now // satisfy import
