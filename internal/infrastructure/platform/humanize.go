package platform

import "strings"

// humanizeText strips the typographic tells that make Miriam read like a
// document instead of a person texting. Em and en dashes are the biggest one:
// LLMs overuse them and nobody texts with them. Plain hyphens are untouched
// (phone numbers, compound words). Applied at the outbound serialization choke
// point so every bridge-bound message benefits: chat replies, onboarding copy,
// proactive nudges, and briefings.
func humanizeText(text string) string {
	if !strings.ContainsAny(text, "\u2013\u2014") { // – —
		return text
	}
	out := strings.NewReplacer(
		" \u2014 ", ", ", // "Hey — I'm" → "Hey, I'm"
		" \u2013 ", ", ",
		"\u2014", ", ", // unspaced fallback so words never fuse
		"\u2013", ", ",
	).Replace(text)

	// Clean up punctuation left behind by the replacements.
	for _, pair := range [][2]string{
		{",,", ","},
		{", .", "."},
		{", ?", "?"},
		{", !", "!"},
		{": ,", ":"},
		{", ,", ","},
	} {
		out = strings.ReplaceAll(out, pair[0], pair[1])
	}
	return strings.TrimRight(out, " ")
}

// humanizeOutbound sanitizes every user-visible string on an OutboundMessage.
func humanizeOutbound(msg *OutboundMessage) {
	if msg == nil {
		return
	}
	msg.Text = humanizeText(msg.Text)
	msg.PollTitle = humanizeText(msg.PollTitle)
	msg.CardTitle = humanizeText(msg.CardTitle)
	for i, opt := range msg.PollOptions {
		msg.PollOptions[i] = humanizeText(opt)
	}
}
