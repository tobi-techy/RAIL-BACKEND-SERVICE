package tools

import (
	"strings"
	"testing"
)

// TestSystemPromptTools_NoDashes ensures the tool routing prompt has no
// typographic punctuation that would leak into user-facing replies.
func TestSystemPromptTools_NoDashes(t *testing.T) {
	if idx := strings.IndexAny(SystemPromptTools, "\u2013\u2014"); idx >= 0 {
		start, end := max(0, idx-30), min(len(SystemPromptTools), idx+30)
		t.Errorf("SystemPromptTools contains an em/en dash near %q", SystemPromptTools[start:end])
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
