package entities

import "context"

type inboundMessageIDKey struct{}

// WithInboundMessageID tags ctx with the provider's id for the inbound message
// being handled, scoped by platform ("whatsapp:wamid.…"). The Python client
// forwards it so the agent can refuse to run the same message twice. An empty
// id is a no-op.
func WithInboundMessageID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, inboundMessageIDKey{}, id)
}

// InboundMessageIDFromContext returns the id set by WithInboundMessageID, or "".
func InboundMessageIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(inboundMessageIDKey{}).(string)
	return id
}
