package ai

import "time"

// ProactiveOpener is returned by the proactive opener endpoint.
type ProactiveOpener struct {
	Greeting      string       `json:"greeting"`
	BubbleMessage string       `json:"bubble_message"`
	Subtitle      string       `json:"subtitle,omitempty"`
	Severity      string       `json:"severity"`
	Suggestions   []Suggestion `json:"suggestions"`
	ActionChips   []ActionChip `json:"action_chips,omitempty"`
}

// Suggestion is a prompt suggestion shown to the user.
type Suggestion struct {
	Text     string `json:"text"`
	Category string `json:"category,omitempty"`
}

// ActionChip is a one-tap action button shown below a chat response.
type ActionChip struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

const proactiveCacheTTL = 60 * time.Second
const proactiveCacheMaxSize = 1000
