package platform

import (
	"context"

	"github.com/google/uuid"
)

// StatementAttachment is a bounded document received from a messaging
// platform. The processor owns transport decoding; implementations own
// scanning and persistence.
type StatementAttachment struct {
	Name     string
	MIMEType string
	Data     []byte
}

// StatementScan is the grounded result made available to the guest brain.
// Summary is derived from parsed transactions, never from raw document bytes.
type StatementScan struct {
	PendingID string
	Summary   string
}

// StatementAttachmentHandler bridges platform messages to the durable
// statement pipeline. It is optional so platform messaging can still boot in
// environments where statement workers are disabled.
type StatementAttachmentHandler interface {
	ScanGuest(ctx context.Context, senderID string, attachment StatementAttachment) (*StatementScan, error)
	EnqueueLinked(ctx context.Context, userID uuid.UUID, attachment StatementAttachment) (*PlatformReply, error)
	CompletePending(ctx context.Context, userID uuid.UUID, pendingID string) error
}
