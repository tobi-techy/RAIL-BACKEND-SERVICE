package di

import (
	"testing"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// TestPythonRole_GrantsMoneyToolsToEmailVerifiedChatAccounts pins the parity
// rule between the two onboarding paths.
//
// pythonRole used to key on KYC approval alone. A chat-onboarded account is
// email-verified but sits at kyc_status non_kyc, so it was handed the read-only
// "user" role and could never move money through Miriam — which meant a chat
// signup could never be the equal of an app signup. A proven address on an
// active account is now the identity floor; KYC approval remains an additional
// grant so existing approved users are unaffected.
func TestPythonRole_GrantsMoneyToolsToEmailVerifiedChatAccounts(t *testing.T) {
	nonKYC := string(entities.KYCStatusNonKYC)
	approved := string(entities.KYCStatusApproved)

	cases := []struct {
		name          string
		kycStatus     string
		emailVerified bool
		isActive      bool
		want          string
	}{
		{"chat signup: email proven at tier 1", nonKYC, true, true, "verified"},
		{"app signup: kyc approved", approved, true, true, "verified"},
		{"kyc approved with an unproven address keeps access", approved, false, true, "verified"},
		{"no proof of identity is read-only", nonKYC, false, true, "user"},
		{"an inactive account never gets money tools", nonKYC, true, false, "user"},
		{"kyc approved but inactive still gets nothing", approved, true, false, "user"},
	}

	for _, tc := range cases {
		got := pythonRole(tc.kycStatus, tc.emailVerified, tc.isActive)
		if got != tc.want {
			t.Errorf("%s: pythonRole(%q, emailVerified=%v, active=%v) = %q, want %q",
				tc.name, tc.kycStatus, tc.emailVerified, tc.isActive, got, tc.want)
		}
	}
}

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
