package core

import "testing"

func TestApplyUXFromTools(t *testing.T) {
	resp := &ChatResponse{Content: "ok"}
	applyUXFromTools(resp, []ToolResult{
		{Name: "send_poll", Data: map[string]interface{}{
			"question": "What's money for?",
			"options":  []interface{}{"a trip", "breathing room"},
		}},
		{Name: "connect_bank", Data: map[string]interface{}{
			"url":   "https://link.mono.co/x",
			"title": "Connect your bank",
		}},
		{Name: "celebrate", Data: map[string]interface{}{"confetti": true}},
	})
	if resp.PollQuestion != "What's money for?" || len(resp.PollOptions) != 2 {
		t.Fatalf("poll = %q %#v", resp.PollQuestion, resp.PollOptions)
	}
	if resp.OpenURL != "https://link.mono.co/x" {
		t.Fatalf("open url = %q", resp.OpenURL)
	}
	if resp.Effect != "celebration" {
		t.Fatalf("effect = %q", resp.Effect)
	}
}
