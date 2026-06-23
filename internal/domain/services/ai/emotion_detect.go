package ai

import "strings"

// detectEmotion returns a one-line LLM tone hint based on keyword matching.
// Returns empty string for neutral messages.
func detectEmotion(message string) string {
	lower := strings.ToLower(message)

	// Frustrated
	for _, kw := range []string{"wtf", "annoyed", "frustrated", "angry", "pissed", "fed up", "sick of", "hate this", "useless", "broken"} {
		if strings.Contains(lower, kw) {
			return "[Emotion: user sounds frustrated. Acknowledge it briefly, stay calm and helpful, no jokes.]"
		}
	}

	// Anxious
	for _, kw := range []string{"worried", "anxious", "scared", "nervous", "panic", "stress", "can't sleep", "freaking out", "what if", "afraid"} {
		if strings.Contains(lower, kw) {
			return "[Emotion: user sounds anxious. Be reassuring and concrete — give them a next step, not platitudes.]"
		}
	}

	// Sad
	for _, kw := range []string{"sad", "depressed", "hopeless", "giving up", "can't do this", "failed", "lost everything", "broke af", "i'm done"} {
		if strings.Contains(lower, kw) {
			return "[Emotion: user sounds down. Be warm and real — no toxic positivity, just presence and a small actionable step.]"
		}
	}

	// Excited
	for _, kw := range []string{"let's go", "yesss", "finally", "omg", "amazing", "hyped", "excited", "can't believe", "i did it", "woohoo"} {
		if strings.Contains(lower, kw) {
			return "[Emotion: user is excited. Match their energy — celebrate with them, then build on the momentum.]"
		}
	}

	return ""
}

// detectEnergy returns a hint about how long/short Miriam's response should be,
// based on the user's message length and style.
func detectEnergy(message string) string {
	length := len(strings.TrimSpace(message))

	switch {
	case length <= 10:
		return "[Energy: user sent a very short message. Reply in 1 line. Don't greet back — just respond to what they need or ask what's up with their money.]"
	case length <= 40:
		return "[Energy: quick question. Keep it tight — answer + one follow-up at most.]"
	case length > 150:
		return "[Energy: user wrote a lot. They want depth. Still use bubbles, but go deeper.]"
	default:
		return ""
	}
}
