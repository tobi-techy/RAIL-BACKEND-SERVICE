package di

import (
	"strings"
	"testing"
	"time"

	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

func TestBuildCrossChannelContinuityNote(t *testing.T) {
	now := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		threads []repositories.ThreadSummary
		want    string
	}{
		{
			name:    "no threads yields empty",
			threads: nil,
			want:    "",
		},
		{
			name: "prefers summary over title",
			threads: []repositories.ThreadSummary{
				{Platform: "whatsapp", Title: "Hey", Summary: "discussed October budget", UpdatedAt: now},
			},
			want: "discussed October budget",
		},
		{
			name: "falls back to title when summary empty",
			threads: []repositories.ThreadSummary{
				{Platform: "imessage", Title: "Subscription audit", Summary: "", UpdatedAt: now},
			},
			want: "Subscription audit",
		},
		{
			name: "skips threads with no topic",
			threads: []repositories.ThreadSummary{
				{Platform: "telegram", Title: "", Summary: "", UpdatedAt: now},
				{Platform: "imessage", Title: "Stash goal", Summary: "saving for rent", UpdatedAt: now},
			},
			want: "saving for rent",
		},
		{
			name: "formats platform display names and date",
			threads: []repositories.ThreadSummary{
				{Platform: "whatsapp", Title: "Rent", Summary: "rent is due Aug 5", UpdatedAt: now},
			},
			want: "WhatsApp (Aug 1): rent is due Aug 5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildCrossChannelContinuityNote(tt.threads)
			if tt.want == "" {
				if got != "" {
					t.Fatalf("expected empty note, got %q", got)
				}
				return
			}
			if !strings.Contains(got, tt.want) {
				t.Fatalf("note %q does not contain %q", got, tt.want)
			}
			if !strings.HasPrefix(got, "[CROSS-CHANNEL CONTINUITY") {
				t.Fatalf("note missing continuity framing: %q", got)
			}
		})
	}
}

func TestFriendlyPlatform(t *testing.T) {
	if got := friendlyPlatform("imessage"); got != "iMessage" {
		t.Fatalf("friendlyPlatform(imessage) = %q, want iMessage", got)
	}
	if got := friendlyPlatform("telegram"); got != "Telegram" {
		t.Fatalf("friendlyPlatform(telegram) = %q, want Telegram", got)
	}
	if got := friendlyPlatform("whatsapp"); got != "WhatsApp" {
		t.Fatalf("friendlyPlatform(whatsapp) = %q, want WhatsApp", got)
	}
	if got := friendlyPlatform("unknown"); got != "unknown" {
		t.Fatalf("friendlyPlatform(unknown) = %q, want pass-through", got)
	}
}
