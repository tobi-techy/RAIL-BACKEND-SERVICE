package platform

import (
	"testing"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

func TestHumanizeText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"no dashes passthrough", "Hey, I'm Miriam. What's up?", "Hey, I'm Miriam. What's up?"},
		{"spaced em dash", "Hey \u2014 I'm Miriam", "Hey, I'm Miriam"},
		{"spaced en dash", "Rent is due \u2013 Friday", "Rent is due, Friday"},
		{"unspaced em dash", "No\u2014build the net first", "No, build the net first"},
		{"dash before period", "You're at 720k\u2014.", "You're at 720k."},
		{"plain hyphen preserved", "call +234-801-234-5678 or e-mail me", "call +234-801-234-5678 or e-mail me"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanizeText(tt.in); got != tt.want {
				t.Errorf("humanizeText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestResponseBuilderJSONSanitizesDashes(t *testing.T) {
	id := &entities.PlatformIdentity{Platform: entities.PlatformIMessage, PlatformUserID: "test-user"}
	b := NewResponseBuilder()
	msg := b.TextResponse(id, "Got it \u2014 moving \u20a620k now")
	data, err := b.JSON(msg)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if got := msg.Text; got != "Got it, moving \u20a620k now" {
		t.Errorf("text not sanitized: %q", got)
	}
	if string(data) == "" {
		t.Errorf("empty payload")
	}

	poll := b.PollResponse(id, "Confirm \u2014 send \u20a65k?", "thread", []string{"Confirm \u2014 yes", "Cancel"})
	if _, err := b.JSON(poll); err != nil {
		t.Fatalf("JSON poll: %v", err)
	}
	if poll.PollTitle != "Confirm, send \u20a65k?" || poll.PollOptions[0] != "Confirm, yes" {
		t.Errorf("poll fields not sanitized: %q / %q", poll.PollTitle, poll.PollOptions[0])
	}
}
