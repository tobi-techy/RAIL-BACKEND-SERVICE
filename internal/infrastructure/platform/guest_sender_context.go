package platform

import (
	"context"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// GuestSender identifies the unlinked sender of the current onboarding turn. A
// Python-backed guest completer uses it to derive a stable synthetic identity
// so the agent's per-sender conversation and interview state survive across
// turns even though the sender has no RAIL account yet.
type GuestSender struct {
	Platform entities.Platform
	SenderID string
	ThreadID string
}

type guestSenderCtxKey struct{}

// ContextWithGuestSender attaches the sender identity to the turn's context so
// a Python-backed guest completer can key its agent call, mirroring how the
// linked path threads user identity through the orchestrator adapter.
func ContextWithGuestSender(ctx context.Context, sender GuestSender) context.Context {
	return context.WithValue(ctx, guestSenderCtxKey{}, sender)
}

// GuestSenderFromContext reads the sender attached by ContextWithGuestSender.
func GuestSenderFromContext(ctx context.Context) (GuestSender, bool) {
	s, ok := ctx.Value(guestSenderCtxKey{}).(GuestSender)
	return s, ok
}
