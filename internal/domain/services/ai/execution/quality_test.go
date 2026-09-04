package execution

import (
	"strings"
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

func TestQualityGateCatchesVerbose(t *testing.T) {
	// Rambling, low-information reply well past the ceiling with no figures.
	verbose := "Well, I took a look at everything going on with your money right now, and honestly there is a lot to unpack here, so let me walk you through it step by step so nothing gets missed. First, I want to say that you are doing a genuinely good job staying on top of things, and that is not something I say lightly, because most people never check at all. Second, your spending this month looks broadly normal, though of course normal is a relative term and depends on what your baseline is, which I am still learning about you. Third, I think we should keep an eye on a few things together over the next couple of weeks, and I will be here watching along with you the whole time."
	v := CheckResponseQuality(verbose)
	assert.False(t, v.Pass, "rambling reply should fail")
	assert.Contains(t, v.Failures, "too_verbose")
}

func TestQualityGateAllowsLongGroundedBreakdown(t *testing.T) {
	// A long reply with several grounded figures (a real breakdown) passes.
	var b strings.Builder
	b.WriteString("Here's the full breakdown you asked for. ")
	for i, line := range []string{
		"Groceries took $420 this month, up a bit. ",
		"Transport was $180, holding steady. ",
		"Eating out hit $260, which is the one to watch. ",
		"Subscriptions are $95 across five services. ",
		"Rent is $1,200 as always. ",
		"That leaves about $645 of breathing room before the 31st. ",
	} {
		b.WriteString(line)
		_ = i
	}
	v := CheckResponseQuality(b.String())
	assert.NotContains(t, v.Failures, "too_verbose", "grounded breakdown should not be flagged verbose")
}

func TestQualityGateSpecificity(t *testing.T) {
	flat := "okay. your ₦12,500 food spending is what the record shows right now, and it is worth keeping in mind because these numbers matter when you are trying to make a decision about where your money should go next."
	v := CheckResponseQuality(flat)
	assert.Contains(t, v.Failures, "lacks_specificity")

	specific := "okay, ₦12,500 went on food. that's 25% above your normal month, so pause takeout this week and set a food cap before the next shop."
	v = CheckResponseQuality(specific)
	assert.NotContains(t, v.Failures, "lacks_specificity")
}

func TestQualityGateCatchesBullets(t *testing.T) {
	v := CheckResponseQuality("• food: ₦12,500\n- transport: ₦4,000")
	assert.Contains(t, v.Failures, "has_markdown")
}
