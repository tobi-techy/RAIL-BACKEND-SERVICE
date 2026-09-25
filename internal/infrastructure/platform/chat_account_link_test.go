package platform

import (
	"strings"
	"testing"
)

func TestPublicAPIOriginStripsConfirmPath(t *testing.T) {
	got := PublicAPIOrigin("https://api.userail.money/confirm")
	if got != "https://api.userail.money" {
		t.Fatalf("origin = %q", got)
	}
}

func TestAccountLinkCopyNamesBothChoices(t *testing.T) {
	text := AccountLinkCopy(ChatLinkOffer{
		AppleURL:  "https://api.userail.money/api/v1/chat-link/start/apple?nonce=a",
		GoogleURL: "https://api.userail.money/api/v1/chat-link/start/google?nonce=a",
	})
	for _, want := range []string{"already use", "different email", "new account", "Apple:", "Google:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("copy missing %q:\n%s", want, text)
		}
	}
}
