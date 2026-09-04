package trivia

import (
	"math/rand"
	"strings"
)

// TrivialReply returns a canned response for messages that don't need an LLM call.
// Returns empty string if the message should go through the normal pipeline.
func TrivialReply(message string) string {
	lower := strings.ToLower(strings.TrimSpace(message))

	// Only match very short messages (greetings, acknowledgements)
	if len(lower) > 20 {
		return ""
	}

	switch {
	case lower == "hey" || lower == "hi" || lower == "yo" || lower == "sup" || lower == "hello":
		return pick(greetingReplies)
	case lower == "thanks" || lower == "thank you" || lower == "thx" || lower == "ty":
		return pick(thanksReplies)
	case lower == "ok" || lower == "okay" || lower == "cool" || lower == "got it" || lower == "bet":
		return pick(ackReplies)
	case lower == "lol" || lower == "haha" || lower == "😂" || lower == "lmao":
		return pick(laughReplies)
	default:
		return ""
	}
}

func pick(options []string) string {
	return options[rand.Intn(len(options))]
}

var greetingReplies = []string{
	"What's good? Need anything money-wise?",
	"I'm here. What do you need?",
	"Talk to me. What's on your mind?",
}

var thanksReplies = []string{
	"Anytime.",
	"Got you.",
	"Always.",
}

var ackReplies = []string{
	"Cool. I'm here if you need anything.",
	"Bet.",
	"You know where to find me.",
}

var laughReplies = []string{
	"I try.",
	"The numbers don't lie.",
	"Your money's funnier than you think.",
}
