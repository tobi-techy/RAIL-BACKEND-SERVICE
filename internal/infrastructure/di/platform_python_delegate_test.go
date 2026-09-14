package di

import (
	"testing"

	"github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// TestVoteReplyIsDroppable covers the gate that keeps a declined poll-vote turn
// from being silently swallowed while still surfacing any reply that carries
// real content (even when the Python agent omitted the onboarding marker).
func TestVoteReplyIsDroppable(t *testing.T) {
	// A fully empty response with no onboarding marker: nothing to deliver.
	if !voteReplyIsDroppable(&ai.PythonChatResponse{}) {
		t.Fatal("empty unmarked response must be dropped")
	}
	// Whitespace is not content.
	if !voteReplyIsDroppable(&ai.PythonChatResponse{Response: "   "}) {
		t.Fatal("whitespace-only response must be dropped")
	}
	// Real content is always delivered, even without the onboarding marker.
	for _, resp := range []*ai.PythonChatResponse{
		{Response: "hello"},
		{Messages: []string{"hi"}},
		{Poll: &ai.PythonChatPoll{Title: "q", Options: []string{"a"}}},
		{Share: &ai.PythonChatShare{Kind: "link", Title: "t", URL: "https://x"}},
	} {
		if voteReplyIsDroppable(resp) {
			t.Fatalf("content-bearing unmarked response must not be dropped: %+v", resp)
		}
	}
	// Off-whitelist emoji is stripped by mapPythonChatReply so it is NOT content.
	if !voteReplyIsDroppable(&ai.PythonChatResponse{Reaction: "🍀"}) {
		t.Fatal("off-whitelist emoji must not count as deliverable content")
	}
	// Whitelisted emoji IS content — a tapback survives mapping.
	if voteReplyIsDroppable(&ai.PythonChatResponse{Reaction: "❤️"}) {
		t.Fatal("whitelisted reaction must count as deliverable content")
	}
	// An onboarding-marked turn is never dropped, regardless of content.
	if voteReplyIsDroppable(&ai.PythonChatResponse{Onboarding: &ai.PythonOnboardingStatus{}}) {
		t.Fatal("onboarding-marked turn must never be dropped")
	}
}