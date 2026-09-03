package channel

import (
	"testing"
	"github.com/google/uuid"
)

func TestNormalizePlatform(t *testing.T) {
	tests := []struct {
		input string
		want  Platform
	}{
		{"imessage", PlatformIMessage},
		{"IMessage", PlatformIMessage},
		{"iMessage", PlatformIMessage},
		{"whatsapp", PlatformWhatsApp},
		{"WhatsApp", PlatformWhatsApp},
		{"whatsapp business", PlatformWhatsApp},
		{"telegram", PlatformTelegram},
		{"Telegram", PlatformTelegram},
		{"sms", PlatformSMS},
		{"SMS", PlatformSMS},
		{"terminal", PlatformTerminal},
		{"Terminal", PlatformTerminal},
		{"unknown", PlatformUnknown},
		{"", PlatformUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizePlatform(tt.input)
			if got != tt.want {
				t.Errorf("NormalizePlatform(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCapabilityRegistry(t *testing.T) {
	registry := NewCapabilityRegistry()

	tests := []struct {
		name     string
		platform Platform
		wantPoll bool
		wantEff  bool
		wantQuick bool
		wantMax  int
	}{
		{"iMessage", PlatformIMessage, true, true, false, 8},
		{"WhatsApp", PlatformWhatsApp, false, false, true, 3},
		{"Telegram", PlatformTelegram, true, false, false, 5},
		{"SMS", PlatformSMS, false, false, false, 1},
		{"Terminal", PlatformTerminal, false, false, false, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := registry.Get(tt.platform)
			if caps.SupportsPolls != tt.wantPoll {
				t.Errorf("SupportsPolls = %v, want %v", caps.SupportsPolls, tt.wantPoll)
			}
			if caps.SupportsEffects != tt.wantEff {
				t.Errorf("SupportsEffects = %v, want %v", caps.SupportsEffects, tt.wantEff)
			}
			if caps.SupportsQuickReplies != tt.wantQuick {
				t.Errorf("SupportsQuickReplies = %v, want %v", caps.SupportsQuickReplies, tt.wantQuick)
			}
			if caps.MaxBubblesPerReply != tt.wantMax {
				t.Errorf("MaxBubblesPerReply = %d, want %d", caps.MaxBubblesPerReply, tt.wantMax)
			}
		})
	}
}

func TestBuildChannelContext(t *testing.T) {
	ctx := BuildChannelContext(PlatformIMessage, uuid.New(), "sender123", "thread-abc", true, "en-NG")

	if ctx.Platform != PlatformIMessage {
		t.Errorf("Platform = %v, want %v", ctx.Platform, PlatformIMessage)
	}
	if ctx.PlatformUserID != "sender123" {
		t.Errorf("PlatformUserID = %q, want %q", ctx.PlatformUserID, "sender123")
	}
	if ctx.ThreadID != "thread-abc" {
		t.Errorf("ThreadID = %q, want %q", ctx.ThreadID, "thread-abc")
	}
	if !ctx.IdentityLinked {
		t.Error("IdentityLinked = false, want true")
	}
	if !ctx.Capabilities.SupportsPolls {
		t.Error("Expected SupportsPolls to be true for iMessage")
	}
	if ctx.Locale != "en-NG" {
		t.Errorf("Locale = %q, want %q", ctx.Locale, "en-NG")
	}
}

func TestChannelContextMediaSupport(t *testing.T) {
	tests := []struct {
		platform  Platform
		wantMedia bool
	}{
		{PlatformIMessage, true},
		{PlatformWhatsApp, true},
		{PlatformTelegram, true},
		{PlatformSMS, false},
		{PlatformTerminal, false},
		{PlatformUnknown, false},
	}

	for _, tt := range tests {
		ctx := BuildChannelContext(tt.platform, uuid.New(), "sender", "thread", true, "en-US")
		if ctx.MediaSupported != tt.wantMedia {
			t.Errorf("Platform %s: MediaSupported = %v, want %v", tt.platform, ctx.MediaSupported, tt.wantMedia)
		}
	}
}

func TestNormalizePlatformCapabilities(t *testing.T) {
	caps := NormalizePlatformCapabilities("imessage")
	if caps == nil {
		t.Error("Expected capabilities for imessage")
	}
	if !caps.SupportsPolls {
		t.Error("Expected SupportsPolls to be true for iMessage")
	}
	if !caps.SupportsEffects {
		t.Error("Expected SupportsEffects to be true for iMessage")
	}
}

func TestNormalizePlatformCapabilitiesSMS(t *testing.T) {
	caps := NormalizePlatformCapabilities("sms")
	if caps == nil {
		t.Error("Expected capabilities for SMS")
	}
	if caps.SupportsPolls {
		t.Error("Did not expect SupportsPolls to be true for SMS")
	}
	if caps.SupportsQuickReplies {
		t.Error("Expected SupportsQuickReplies to be false for SMS")
	}
	if caps.MaxBubblesPerReply != 1 {
		t.Errorf("MaxBubblesPerReply = %d, want 1", caps.MaxBubblesPerReply)
	}
}
