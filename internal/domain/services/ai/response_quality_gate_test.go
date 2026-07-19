package ai

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQualityGatePassesGoodResponses(t *testing.T) {
	good := []string{
		"Yo. $735 in stash. Three months ago that was zero.",
		"Net positive again. More in than out. That's the game.",
		"Okay. Let's look at this. Spend is thin — 9 days to payday.",
		"₦80k on food. You're funding someone's restaurant expansion.",
		"Done. $50 moved to stash.",
		"Third month of not touching stash. That's discipline.",
	}
	for _, r := range good {
		v := CheckResponseQuality(r)
		assert.True(t, v.Pass, "should pass: %q", r)
	}
}

func TestQualityGateCatchesSlop(t *testing.T) {
	slop := []string{
		"Great question! Let me look at your spending.",
		"I'd be happy to help you with that.",
		"Absolutely! Here's your balance breakdown.",
		"Based on the data, you spent $342 this month.",
		"Certainly! Your stash is at $735.",
		"Here's what I found in your account.",
		"Thanks for asking! Your income is $3200.",
	}
	for _, r := range slop {
		v := CheckResponseQuality(r)
		assert.False(t, v.Pass, "should fail: %q", r)
		assert.Contains(t, v.Failures, "starts_with_slop")
	}
}

func TestQualityGateCatchesBannedPhrases(t *testing.T) {
	banned := []string{
		"Your balance is $400. Is there anything else I can help you with?",
		"I hope this helps! Your spending is normal.",
		"As an AI, I don't have real-time data but...",
		"I don't have access to your bank account.",
		"Feel free to ask me anything else about your money.",
	}
	for _, r := range banned {
		v := CheckResponseQuality(r)
		assert.False(t, v.Pass, "should fail: %q", r)
		assert.Contains(t, v.Failures, "contains_banned_phrase")
	}
}

func TestQualityGateCatchesRawDataOpener(t *testing.T) {
	raw := []string{
		"You spent $342.50 this month across 23 transactions.",
		"Your balance is $735 in stash and $412 in spend.",
		"You have $1,147 total across both wallets.",
		"Your stash balance is currently $735.00.",
		"You deposited $200 on the 15th.",
	}
	for _, r := range raw {
		v := CheckResponseQuality(r)
		assert.False(t, v.Pass, "should fail: %q", r)
		assert.Contains(t, v.Failures, "opens_with_raw_data")
	}
}

func TestQualityGateAcceptsDataAfterReaction(t *testing.T) {
	// Data is fine as long as there's a reaction FIRST
	ok := []string{
		"Look at you. $735 in stash — three months of growth.",
		"Nice. You deposited $200 on the 15th.",
		"Okay so. Your balance is $412 in spend.",
		"Honestly? You spent $342 this month and most of it was food.",
	}
	for _, r := range ok {
		v := CheckResponseQuality(r)
		assert.True(t, v.Pass, "should pass (data after reaction): %q", r)
	}
}

func TestQualityCorrectionHint(t *testing.T) {
	hint := QualityCorrectionHint([]string{"starts_with_slop", "opens_with_raw_data"})
	assert.NotEmpty(t, hint)
	assert.Contains(t, hint, "greeting")

	empty := QualityCorrectionHint(nil)
	assert.Empty(t, empty)
}

func TestQualityGateEmptyResponse(t *testing.T) {
	v := CheckResponseQuality("")
	assert.True(t, v.Pass, "empty response should pass (handled elsewhere)")
}
