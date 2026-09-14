package platform

import (
	"context"
	"fmt"
	"strings"
)

// GuestVote describes the poll tap that produced the current onboarding turn.
//
// A tap arrives as the bare option title the guest chose ("Savings or
// investments") — indistinguishable from fresh chatter unless it is flagged.
// Without that flag the guest brain can only forward it as ordinary text, and
// the interview brain answers a deliberate selection by re-asking its own
// question with a brand-new poll, which reads to the guest as a dead tap.
//
// It rides the turn's context alongside GuestSender so the shared
// GuestCompleter interface needs no poll-aware variant.
type GuestVote struct {
	IsPollVote bool
	PollTitle  string
}

type guestVoteCtxKey struct{}

// ContextWithGuestVote attaches the poll-tap metadata to the turn's context.
// A normal text turn is a no-op: the zero GuestVote is the default, so callers
// can attach unconditionally.
func ContextWithGuestVote(ctx context.Context, vote GuestVote) context.Context {
	if !vote.IsPollVote {
		return ctx
	}
	return context.WithValue(ctx, guestVoteCtxKey{}, vote)
}

// GuestVoteFromContext reads the poll-tap metadata attached by
// ContextWithGuestVote. The zero value means the turn was ordinary user text.
func GuestVoteFromContext(ctx context.Context) GuestVote {
	vote, _ := ctx.Value(guestVoteCtxKey{}).(GuestVote)
	return vote
}

// guestVoteNote renders the poll-tap context for whichever model answers the
// turn. The guest completer forwards the flag to the Python brain, but the
// fallback provider answers on a Python outage, so the note belongs in the
// prompt too: either way a bare option title must be read as the answer to the
// question Miriam already asked instead of a new topic.
func guestVoteNote(ctx context.Context) string {
	vote := GuestVoteFromContext(ctx)
	if !vote.IsPollVote {
		return ""
	}
	note := "\n\n[The person just tapped one of the options on a poll you sent"
	if title := strings.TrimSpace(vote.PollTitle); title != "" {
		note += fmt.Sprintf(" (that poll asked: %q)", title)
	}
	return note + ". What they sent is their answer to that question: " +
		"acknowledge the choice and move the conversation forward. " +
		"Never re-ask a question they have already answered.]"
}
