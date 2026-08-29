package execution

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

	// 0. Fabrication: states specific dollar amounts without hedging — likely hallucinated
	if looksFabricated(response) {
		failures = append(failures, "possible_fabrication")
	}

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

	// 5. Mono-bubble: >200 chars with no paragraph break
	if isMonoBubble(response) {
		failures = append(failures, "mono_bubble")
	}

	// 6. A substantial money answer should make the figure useful, either by
	// grounding it in a comparison or giving one concrete next action. It must
	// not force a follow-up question, because direct answers are valid replies.
	if lacksSpecificity(response) {
		failures = append(failures, "lacks_specificity")
	}

	// 7. Currency confusion: mixes $ and ₦ without labeling which is which
	if hasCurrencyConfusion(response) {
		failures = append(failures, "currency_confusion")
	}

	// 8. Contains markdown formatting or numbered lists
	if hasMarkdownOrLists(response) {
		failures = append(failures, "has_markdown")
	}

	// 9. Too verbose — rambles past what a text reply should be
	if isTooVerbose(response) {
		failures = append(failures, "too_verbose")
	}

	return QualityVerdict{
		Pass:     len(failures) == 0,
		Failures: failures,
	}
}

// QualityCorrectionHint returns a short re-prompt instruction for the retry.
// Kept conversational — not adversarial — so the model doesn't overcorrect.
func QualityCorrectionHint(failures []string) string {
	if len(failures) == 0 {
		return ""
	}
	// Only retry for the most impactful issues — don't stack corrections
	for _, f := range failures {
		switch f {
		case "possible_fabrication":
			return "CRITICAL: Your response contains dollar amounts you did not get from a tool call. You are making up numbers. REWRITE: Remove ALL specific dollar figures you invented. Call the relevant tools first to get real data, or say you don't have that information. Never state a number unless a tool gave it to you."
		case "starts_with_slop":
			return "Rewrite. Skip the greeting/filler — just react and answer directly like you're mid-conversation."
		case "contains_banned_phrase":
			return "Rewrite without AI-speak. Talk like a real person texting."
		case "opens_with_raw_data":
			return "Rewrite. React to what the number means before saying the number."
		case "mono_bubble":
			return "Rewrite as short separate texts (blank line between each). One thought per bubble."
		case "currency_confusion":
			return "Rewrite. Label which amounts are USD and which are naira — don't mix $ and ₦ without saying which is which."
		case "emotionally_flat":
			return "Rewrite with more personality — a reaction, a comparison, or a question. Make it feel like a real person said it."
		case "lacks_specificity":
			return "Rewrite so the money figure means something: add one honest comparison, annual view, or concrete next action. Do not add a question unless the answer genuinely depends on it."
		case "has_markdown":
			return "Rewrite in plain text only. No bold, no italics, no numbered lists, no markdown. You're texting, not writing a document."
		case "too_verbose":
			return "Rewrite much shorter. Lead with the direct answer in the first line, cut every sentence that doesn't add information, and stop. No preamble, no wrap-up questions unless the user is clearly chatting."
		}
	}
	return ""
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
	"hey there",
	"hey!",
	"hi there",
	"hello!",
	"hey,",
	"let's break it down",
	"let me break",
	"just thinking about",
	"good to see you",
	"nice to hear from",
	"what's up",
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
	for _, marker := range []string{"done.", "moved", "created", "set to", "automation", "confirmed", "cancelled", "flagged", "dispute", "refund"} {
		if strings.Contains(lower, marker) {
			return false
		}
	}

	// Skip data-heavy responses that cite specific numbers — informational answers don't need personality
	if dollarAmountPattern.MatchString(response) {
		return false
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

// isMonoBubble detects responses >200 chars with no paragraph break (\n\n).
// Long single blocks don't feel like texting.
func isMonoBubble(response string) bool {
	if len(response) <= 200 {
		return false
	}
	// Skip action confirmations
	lower := strings.ToLower(response)
	for _, m := range []string{"done.", "moved", "created", "automation", "confirmed", "flagged", "dispute"} {
		if strings.Contains(lower, m) {
			return false
		}
	}
	// Skip data-heavy responses — informational answers can be longer
	if dollarAmountPattern.MatchString(response) {
		return false
	}
	return !strings.Contains(response, "\n\n")
}

// lacksSpecificity detects substantial monetary replies that report a figure
// without showing why it matters or what to do next. Direct short answers and
// action receipts intentionally pass.
func lacksSpecificity(response string) bool {
	if len(response) <= 150 {
		return false
	}
	lower := strings.ToLower(response)
	for _, m := range []string{"done.", "moved", "created", "automation", "confirmed", "flagged", "dispute", "refund"} {
		if strings.Contains(lower, m) {
			return false
		}
	}
	hasFigure := dollarAmountPattern.MatchString(response) || nairaAmountPattern.MatchString(response)
	if !hasFigure {
		return false
	}
	for _, marker := range []string{
		" per ", " a year", " annual", " month", " week", " day", " compared",
		" than ", " more ", " less ", "enough for", "cover ",
		"move ", "set up", "pay ", "cut ", "cancel ", "build ",
	} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}

// isTooVerbose flags replies that run long for a text conversation. The user
// asked for directness: default replies should be a few short sentences. The
// threshold is generous (only clearly rambling replies trip it) so detailed
// breakdowns the user explicitly requested still pass.
func isTooVerbose(response string) bool {
	const wordCeiling = 120
	words := strings.Fields(response)
	if len(words) <= wordCeiling {
		return false
	}
	// A response the user explicitly asked to expand (a breakdown/audit) reads
	// like a report — but those are driven by tools like get_financial_audit and
	// go through the same gate, so allow long structured answers only when they
	// contain multiple grounded figures (a real breakdown, not waffle).
	figures := dollarAmountPattern.FindAllString(response, -1)
	figures = append(figures, nairaAmountPattern.FindAllString(response, -1)...)
	return len(figures) < 4
}

// hasCurrencyConfusion detects when both $ and ₦ appear without clear labels.
func hasCurrencyConfusion(response string) bool {
	hasDollar := strings.Contains(response, "$")
	hasNaira := strings.Contains(response, "₦")
	if !hasDollar || !hasNaira {
		return false
	}
	// If it has explicit labels, it's fine
	lower := strings.ToLower(response)
	for _, label := range []string{"usd", "ngn", "naira", "dollars", "spend balance", "stash balance"} {
		if strings.Contains(lower, label) {
			return false
		}
	}
	return true
}

// hasMarkdownOrLists detects markdown formatting or numbered lists in the response.
var numberedListPattern = regexp.MustCompile(`(?m)^\s*(?:\d+[\.\)]|[-•‣])\s`)

func hasMarkdownOrLists(response string) bool {
	// Bold/italic markdown
	if strings.Contains(response, "**") || strings.Contains(response, "__") {
		return true
	}
	// Headers
	if strings.Contains(response, "### ") || strings.Contains(response, "## ") {
		return true
	}
	// Numbered lists (1. or 1) at start of line)
	if numberedListPattern.MatchString(response) {
		return true
	}
	return false
}

// dollarAmountPattern matches dollar amounts like $64, $1,234.56, $64.00
var dollarAmountPattern = regexp.MustCompile(`\$[\d,]+(?:\.\d{1,2})?`)

// nairaAmountPattern matches Naira amounts like ₦500, ₦1,200.50
var nairaAmountPattern = regexp.MustCompile(`₦[\d,]+(?:\.\d{1,2})?`)

// looksFabricated flags responses that state specific dollar amounts as facts
// without grounding from tool calls. It is deliberately conservative: the quality
// gate doesn't see the conversation transcript, so it can only flag the clearest
// cases. Multi-amount responses that look like math/comparison/confirmation are
// allowed through (the user often provides numbers that Miriam repeats back).
func looksFabricated(response string) bool {
	matches := dollarAmountPattern.FindAllString(response, -1)
	if len(matches) == 0 {
		return false
	}
	// Deduplicate amounts.
	seen := make(map[string]bool, len(matches))
	for _, m := range matches {
		seen[m] = true
	}
	distinct := len(seen)

	// Single amount: flag only if the response is very short and the number
	// looks invented (not a round number the user likely provided).
	if distinct == 1 && len(response) < 120 {
		for amount := range seen {
			if !isRoundAmount(amount) {
				return true
			}
		}
		return false
	}

	// 3+ distinct amounts in a short response: suspicious, but allow if the
	// response is doing math or confirming a user-provided calculation.
	if distinct >= 3 && len(response) < 300 {
		calcWords := strings.Contains(response, "adding") ||
			strings.Contains(response, "total") ||
			strings.Contains(response, "brings") ||
			strings.Contains(response, "new total") ||
			strings.Contains(response, "that's right")
		return !calcWords
	}

	return false
}

// isRoundAmount returns true for amounts that look user-provided (round numbers
// or small amounts that are unlikely to be hallucinated).
func isRoundAmount(amount string) bool {
	clean := strings.TrimPrefix(amount, "$")
	clean = strings.ReplaceAll(clean, ",", "")
	// Treat amounts ending in .00, .50, or with no decimal as round.
	return strings.HasSuffix(clean, ".00") ||
		strings.HasSuffix(clean, ".50") ||
		!strings.Contains(clean, ".")
}
