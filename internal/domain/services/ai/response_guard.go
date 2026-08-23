package ai

import (
	"regexp"
	"strings"

	"github.com/shopspring/decimal"
)

// GuardResponse deterministically repairs a drafted reply before delivery. It
// strips currency figures the model could not have grounded in real data,
// forces a proactive anomaly mention when one was surfaced but ignored, and
// applies mechanical formatting fixes (slop prefix, markdown, mono-bubble). It
// never calls the LLM — every step is a pure string transform so even the
// weakest model lands a clean, non-fabricated answer.
//
// grounding is free text (tool-result JSON, injected balances/FX, assembled
// context) holding every real figure Miriam is allowed to state. anomalies is
// the raw injected anomaly context, if any.
func GuardResponse(content, grounding, anomalies string) string {
	if strings.TrimSpace(content) == "" {
		return content
	}
	out := content
	out = stripFabricatedAmounts(out, grounding)
	out = sanitizeMechanics(out)
	out = surfaceAnomaly(out, anomalies)
	return out
}

// --- grounded-number guard ---

// guardCurrencyRe matches figures carrying explicit currency context: a leading
// $ or ₦, or a trailing currency word.
var guardCurrencyRe = regexp.MustCompile(`(?i)(?:[$₦]\s?[0-9][0-9,]*(?:\.[0-9]{1,2})?)|(?:[0-9][0-9,]*(?:\.[0-9]{1,2})?)\s?(?:dollars|bucks|usd|naira|ngn)`)

// guardNumberRe matches any bare number token in the grounding corpus.
var guardNumberRe = regexp.MustCompile(`[0-9][0-9,]*(?:\.[0-9]+)?`)

// sentenceSplitRe splits text into sentences while preserving their terminators
// and trailing newlines, so rejoining keeps the original formatting.
var sentenceSplitRe = regexp.MustCompile(`[^.!?\n]+[.!?]*\n*`)

var (
	guardTwo   = decimal.NewFromInt(2)
	guardThree = decimal.NewFromInt(3)
	guardFour  = decimal.NewFromInt(4)
)

// stripFabricatedAmounts drops any sentence stating a currency figure that
// cannot be grounded in (or trivially derived from) the grounding corpus. When
// nothing can be grounded (no corpus), it leaves the reply untouched rather than
// gutting it.
func stripFabricatedAmounts(content, grounding string) string {
	grounded := groundedNumberSet(grounding)
	if len(grounded) == 0 {
		return content
	}
	sentences := sentenceSplitRe.FindAllString(content, -1)
	if len(sentences) == 0 {
		return content
	}
	kept := make([]string, 0, len(sentences))
	for _, s := range sentences {
		if sentenceHasUngroundedAmount(s, grounded) {
			continue
		}
		kept = append(kept, s)
	}
	if len(kept) == 0 {
		return "Let me pull the exact figures — give me a sec."
	}
	return strings.Join(kept, "")
}

// UngroundedAmountSentences reports sentences stating currency figures that
// cannot be traced to (or trivially derived from) the grounding corpus. This is
// the read-only counterpart of stripFabricatedAmounts — used on content that
// was already streamed to the client where repair is impossible and detection
// exists purely for observability.
func UngroundedAmountSentences(content, grounding string) []string {
	grounded := groundedNumberSet(grounding)
	if len(grounded) == 0 {
		return nil
	}
	var out []string
	for _, s := range sentenceSplitRe.FindAllString(content, -1) {
		if sentenceHasUngroundedAmount(s, grounded) {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func sentenceHasUngroundedAmount(s string, grounded []decimal.Decimal) bool {
	for _, m := range guardCurrencyRe.FindAllString(s, -1) {
		val, ok := parseCurrencyAmount(m)
		if !ok {
			continue
		}
		if !amountGrounded(val, grounded) {
			return true
		}
	}
	return false
}

func parseCurrencyAmount(tok string) (decimal.Decimal, bool) {
	cleaned := strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || r == '.' {
			return r
		}
		return -1
	}, tok)
	if cleaned == "" {
		return decimal.Zero, false
	}
	v, err := decimal.NewFromString(cleaned)
	if err != nil {
		return decimal.Zero, false
	}
	return v, true
}

// groundedNumberSet extracts every number from the grounding corpus and adds the
// common derivations a truthful reply legitimately states: pairwise sums and
// half/third/quarter of each value. Bounded so a large corpus can't blow up.
func groundedNumberSet(grounding string) []decimal.Decimal {
	raw := guardNumberRe.FindAllString(grounding, -1)
	base := make([]decimal.Decimal, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, r := range raw {
		clean := strings.ReplaceAll(r, ",", "")
		if seen[clean] {
			continue
		}
		seen[clean] = true
		if v, err := decimal.NewFromString(clean); err == nil {
			base = append(base, v)
		}
	}
	if len(base) == 0 {
		return nil
	}
	out := append([]decimal.Decimal(nil), base...)
	limit := len(base)
	if limit > 40 {
		limit = 40
	}
	for i := 0; i < limit; i++ {
		out = append(out,
			base[i].Div(guardTwo).Round(2),
			base[i].Div(guardThree).Round(2),
			base[i].Div(guardFour).Round(2),
		)
		for j := i + 1; j < limit; j++ {
			out = append(out, base[i].Add(base[j]))
		}
	}
	return out
}

func amountGrounded(val decimal.Decimal, grounded []decimal.Decimal) bool {
	if val.IsZero() {
		return true
	}
	tol := val.Abs().Mul(decimal.NewFromFloat(0.05))
	for _, g := range grounded {
		if g.Sub(val).Abs().LessThanOrEqual(tol) {
			return true
		}
	}
	return false
}

// --- mechanical sanitizer ---

func sanitizeMechanics(content string) string {
	out := stripSlopPrefix(content)
	out = stripMarkdown(out)
	out = splitMonoBubble(out)
	return strings.TrimSpace(out)
}

func stripSlopPrefix(s string) string {
	trimmed := strings.TrimSpace(s)
	lower := strings.ToLower(trimmed)
	for _, p := range slopPrefixes {
		if strings.HasPrefix(lower, p) {
			if idx := strings.IndexAny(trimmed, ".!?\n"); idx != -1 && idx+1 < len(trimmed) {
				return strings.TrimSpace(trimmed[idx+1:])
			}
			return ""
		}
	}
	return s
}

func stripMarkdown(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "__", "")
	s = strings.ReplaceAll(s, "### ", "")
	s = strings.ReplaceAll(s, "## ", "")
	s = strings.ReplaceAll(s, "# ", "")
	s = numberedListPattern.ReplaceAllString(s, "")
	return s
}

func splitMonoBubble(s string) string {
	if len(s) <= 200 || strings.Contains(s, "\n\n") {
		return s
	}
	sentences := sentenceSplitRe.FindAllString(s, -1)
	if len(sentences) < 2 {
		return s
	}
	var b strings.Builder
	for i, sent := range sentences {
		t := strings.TrimSpace(sent)
		if t == "" {
			continue
		}
		b.WriteString(t)
		if i < len(sentences)-1 {
			b.WriteString("\n\n")
		}
	}
	return b.String()
}

// --- anomaly-surface guard ---

var guardAnomalyLineRe = regexp.MustCompile(`(?m)^\[[A-Z]+\]\s+(.+)$`)

func surfaceAnomaly(content, anomalies string) string {
	if strings.TrimSpace(anomalies) == "" {
		return content
	}
	if mentionsAnomaly(strings.ToLower(content)) {
		return content
	}
	summary := anomalySummaryLine(anomalies)
	if summary == "" {
		return content
	}
	return strings.TrimRight(content, "\n ") + "\n\n" + summary
}

func anomalySummaryLine(anomalies string) string {
	m := guardAnomalyLineRe.FindStringSubmatch(anomalies)
	if len(m) < 2 {
		return ""
	}
	body := strings.TrimSpace(m[1])
	if body == "" {
		return ""
	}
	return "Also — I noticed something worth flagging: " + body + ". Recognize that one?"
}

func mentionsAnomaly(lower string) bool {
	for _, kw := range []string{"charge", "duplicate", "noticed", "unusual", "flag", "spike", "recognize"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
