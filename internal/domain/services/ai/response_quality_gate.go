package ai

import (
	"regexp"
	"strings"
)

// QualityVerdict is the result of a quality gate check.
type QualityVerdict struct {
	Pass     bool     // true if response meets personality standards
	Failures []string // which checks failed (for logging/debugging)
}

// CheckResponseQuality runs fast checks on the LLM output BEFORE delivery.
// Returns a verdict indicating if the response meets Miriam's personality standards.
// This is NOT a rewrite — it catches obvious failures so the caller can decide
// whether to retry with a correction hint.
func CheckResponseQuality(response string) QualityVerdict {
	if strings.TrimSpace(response) == "" {
		return QualityVerdict{Pass: true} // empty responses are handled elsewhere
	}

	var failures []string

	// 1. Starts with banned AI slop phrases
	if startsWithSlop(response) {
		failures = append(failures, "starts_with_slop")
	}

	// 2. Contains banned phrases anywhere
	if containsBannedPhrases(response) {
		failures = append(failures, "contains_banned_phrase")
	}

	// 3. Opens with a raw number without any reaction/framing
	if opensWithRawData(response) {
		failures = append(failures, "opens_with_raw_data")
	}

	// 4. Emotionally flat — has data but no personality signals
	if isEmotionallyFlat(response) {
		failures = append(failures, "emotionally_flat")
	}

	return QualityVerdict{
		Pass:     len(failures) == 0,
		Failures: failures,
	}
}

// QualityCorrectionHint returns a short instruction to prepend to a retry
// based on what failed. Used as an additional system message for the retry call.
func QualityCorrectionHint(failures []string) string {
	var hints []string
	for _, f := range failures {
		switch f {
		case "starts_with_slop":
			hints = append(hints, "Do NOT start with filler phrases. React naturally first — one word or a short observation.")
		case "contains_banned_phrase":
			hints = append(hints, "Remove AI-sounding language. Talk like a real person who knows their money.")
		case "opens_with_raw_data":
			hints = append(hints, "Don't open with the number. React to what the number MEANS first, then drop the data.")
		case "emotionally_flat":
			hints = append(hints, "This response is flat. Add personality: a vivid comparison, a callback, a question hook, or an opinion about what the numbers mean. Make the user FEEL something.")
		}
	}
	if len(hints) == 0 {
		return ""
	}
	return "[PERSONALITY FIX — your last response failed quality check: " + strings.Join(hints, " ") + "]"
}

// --- Check implementations ---

var slopPrefixes = []string{
	"great question",
	"that's a great",
	"i'd be happy to",
	"i'd be glad to",
	"absolutely!",
	"certainly!",
	"sure thing",
	"of course!",
	"let me help you",
	"let me check that",
	"based on the data",
	"based on your data",
	"according to my",
	"according to the",
	"looking at your",
	"i can see that",
	"here's what i found",
	"here is a summary",
	"here's a breakdown",
	"to answer your question",
	"thanks for asking",
	"good question",
}

func startsWithSlop(response string) bool {
	lower := strings.ToLower(strings.TrimSpace(response))
	for _, prefix := range slopPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

var bannedPhrases = []string{
	"is there anything else i can help",
	"is there anything else you'd like",
	"let me know if you need anything",
	"feel free to ask",
	"don't hesitate to",
	"hope this helps",
	"i hope that helps",
	"as an ai",
	"as a language model",
	"i don't have access to",
	"i'm unable to",
	"i cannot access",
}

func containsBannedPhrases(response string) bool {
	lower := strings.ToLower(response)
	for _, phrase := range bannedPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// opensWithRawData detects if the first sentence is just a data readout.
// Pattern: starts with "You" or "Your" followed immediately by a verb + number.
// e.g. "You spent $342" or "Your balance is $735"
var rawDataOpener = regexp.MustCompile(`(?i)^(you\s+(spent|have|earned|saved|deposited|withdrew|received)|your\s+(balance|spend|stash|total|budget|account))\s`)

func opensWithRawData(response string) bool {
	trimmed := strings.TrimSpace(response)
	// Only check first sentence
	first := firstSentence(trimmed)
	if first == "" {
		return false
	}
	return rawDataOpener.MatchString(first)
}

func firstSentence(s string) string {
	for i, r := range s {
		if r == '.' || r == '!' || r == '?' || r == '\n' {
			return s[:i+1]
		}
		// If we're past 100 chars without punctuation, give up
		if i > 100 {
			return s[:i]
		}
	}
	// Short response with no punctuation — check the whole thing
	if len(s) < 80 {
		return s
	}
	return ""
}

// isEmotionallyFlat detects responses that are technically correct but have
// zero personality — no hook, no comparison, no opinion, no reaction.
// Only triggers on medium-length responses (short answers are fine being direct).
func isEmotionallyFlat(response string) bool {
	// Skip short responses — "Done. $50 moved." is fine being flat.
	if len(response) < 80 {
		return false
	}

	lower := strings.ToLower(response)

	// Skip action confirmations — these are transactional, personality isn't expected.
	for _, marker := range []string{"done.", "moved", "created", "set to", "automation", "confirmed", "cancelled"} {
		if strings.Contains(lower, marker) {
			return false
		}
	}

	// If it has personality signals, it's not flat.
	personalitySignals := 0

	// Questions/hooks (engages the user)
	if strings.Contains(lower, "?") {
		personalitySignals++
	}
	// Vivid comparisons (connects to real life)
	for _, marker := range []string{"like ", "basically", "literally", "imagine", "that's a", "could ", "enough to"} {
		if strings.Contains(lower, marker) {
			personalitySignals++
			break
		}
	}
	// Opinions/reactions (Miriam has a take)
	for _, marker := range []string{"honestly", "real talk", "look at", "not bad", "nice", "yo", "okay so", "proud", "love that", "nah", "damn"} {
		if strings.Contains(lower, marker) {
			personalitySignals++
			break
		}
	}
	// Callbacks/time references (narrative framing)
	for _, marker := range []string{"last time", "last month", "ago", "before", "used to", "remember", "again", "still", "streak", "first time"} {
		if strings.Contains(lower, marker) {
			personalitySignals++
			break
		}
	}
	// Contrast/emotion words
	for _, marker := range []string{"but", "though", "actually", "wild", "solid", "tight", "thin", "growing", "building"} {
		if strings.Contains(lower, marker) {
			personalitySignals++
			break
		}
	}

	// A response with 0-1 personality signals out of 5 categories is flat.
	return personalitySignals <= 1
}
