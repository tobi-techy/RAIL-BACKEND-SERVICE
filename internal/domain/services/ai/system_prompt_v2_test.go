package ai

import (
	"strings"
	"testing"
)

// TestSystemPromptV2_ConversationalCore guards the personality retune: the
// prompt must teach when to answer vs ask, carry Miriam's own personality,
// judgment, and interaction modes, and must NOT regress to the old rigid rules
// (hard brevity ceiling, unconditional greeting ban, always-ask coaching).
func TestSystemPromptV2_ConversationalCore(t *testing.T) {
	for _, want := range []string{
		"WHO YOU ARE",
		"YOUR JOB",
		"RELATIONSHIP",
		"CONVERSATIONAL INTELLIGENCE",
		"INTERACTION MODES",
		"JUDGMENT",
		"FINANCIAL PHILOSOPHY",
		"ADAPTIVE LENGTH",
		"GREETINGS:",
	} {
		if !strings.Contains(SystemPromptV2, want) {
			t.Errorf("SystemPromptV2 missing %q section from conversational retune", want)
		}
	}

	for _, banned := range []string{
		"NEVER GREET",             // replaced by contextual GREETINGS rule
		"NEVER RUSH TO SOLUTIONS", // contradicted ANSWER THE QUESTION ASKED
		"Hard ceiling ~60 words",  // hard brevity cap replaced by ADAPTIVE LENGTH
		"RAMIT",                   // philosophy is Miriam's own; no named personalities
		"THE RAMIT SETHI METHOD",
	} {
		if strings.Contains(SystemPromptV2, banned) {
			t.Errorf("SystemPromptV2 still contains banned rule %q", banned)
		}
	}
}

// TestSystemPromptV2_NoDashes keeps typographic tells out of the prompt: the
// model imitates the prompt's punctuation, and nobody texts with em dashes.
// The OUTPUT section also carries an explicit ban; this pins the source too.
func TestSystemPromptV2_NoDashes(t *testing.T) {
	if !strings.Contains(SystemPromptV2, "NO EM DASHES") {
		t.Errorf("SystemPromptV2 missing the NO EM DASHES output rule")
	}
	if idx := strings.IndexAny(SystemPromptTools, "\u2013\u2014"); idx >= 0 {
		t.Errorf("SystemPromptTools contains a dash near %q", SystemPromptTools[max(0, idx-30):idx+30])
	}
	for _, prompt := range []string{SystemPromptV2, SystemPromptTools} {
		if idx := strings.IndexAny(prompt, "\u2013\u2014"); idx >= 0 {
			start, end := max(0, idx-30), min(len(prompt), idx+30)
			t.Errorf("prompt contains an em/en dash near %q", prompt[start:end])
		}
	}
}

// TestSystemPromptV2_Tightness keeps the prompt from ballooning: the retune
// restructured hierarchy instead of adding instructions. Bound is generous
// (~10% over post-retune size) but catches wholesale appends.
func TestSystemPromptV2_Tightness(t *testing.T) {
	const maxChars = 13000
	if got := len(SystemPromptV2); got > maxChars {
		t.Errorf("SystemPromptV2 grew to %d chars (max %d) — tighten instead of appending", got, maxChars)
	}
}
