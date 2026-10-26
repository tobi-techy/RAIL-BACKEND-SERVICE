package platform

import (
	"context"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// InboundTurn is what the processor knew about the inbound message before it
// rewrote the text for the legacy brain (OCR summaries replace a photo, a
// quoted reply is folded into the text). The /v1/turns path needs the
// original: the channel message id as the idempotency key, the shop's own
// words, and the photo bytes.
type InboundTurn struct {
	Platform   entities.Platform
	MsgID      string
	TurnID     string
	ThreadID   string
	SenderID   string // the channel handle (phone number); audit only
	Text       string // the user's words (the transcript for a voice note)
	IsVoice    bool
	ImageB64   string
	ImageMime  string
	ReceivedAt time.Time
}

type inboundTurnCtxKey struct{}

// ContextWithInboundTurn attaches the original inbound message to the turn's context.
func ContextWithInboundTurn(ctx context.Context, t InboundTurn) context.Context {
	return context.WithValue(ctx, inboundTurnCtxKey{}, t)
}

// InboundTurnFromContext reads what ContextWithInboundTurn attached.
func InboundTurnFromContext(ctx context.Context) (InboundTurn, bool) {
	t, ok := ctx.Value(inboundTurnCtxKey{}).(InboundTurn)
	return t, ok
}
