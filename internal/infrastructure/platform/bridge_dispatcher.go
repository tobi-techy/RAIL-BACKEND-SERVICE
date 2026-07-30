package platform

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// ThreadResolver finds the active messaging thread for a user on a platform.
type ThreadResolver interface {
	GetLastPlatformThread(ctx context.Context, userID uuid.UUID, platform string) (string, error)
}

// BridgeDispatcher is the single delivery path for all of Miriam's proactive
// output. Every nudge, mandate receipt, briefing, and alert goes to the user's
// iMessage thread — there is no push fallback. It satisfies the notifier
// interfaces used across the Miriam subsystem (chat sender, generic notifier,
// and the worker push-sender shape) so those callers all land in iMessage.
type BridgeDispatcher struct {
	send     SendMessageFunc
	threads  ThreadResolver
	platform entities.Platform
	guard    *ProactiveGuard
	logger   *zap.Logger
}

// SendMessageFunc is the signature used by the platform consumer to publish outbound
// messages. Same closure wired in container.go.
type SendMessageFunc func(ctx context.Context, msg *OutboundMessage) error

// NewBridgeDispatcher creates a dispatcher that routes proactive messages to users
// on the given messaging platform.
func NewBridgeDispatcher(send SendMessageFunc, threads ThreadResolver, platform entities.Platform, logger *zap.Logger) *BridgeDispatcher {
	return &BridgeDispatcher{
		send:     send,
		threads:  threads,
		platform: platform,
		logger:   logger,
	}
}

// SetGuard attaches a quiet-hours + daily-frequency guard. Optional — without it
// every proactive message is delivered.
func (d *BridgeDispatcher) SetGuard(g *ProactiveGuard) {
	d.guard = g
}

// SendChatMessage delivers a proactive nudge to the user's iMessage thread.
// Subject to the frequency/quiet-hours guard.
func (d *BridgeDispatcher) SendChatMessage(ctx context.Context, userID uuid.UUID, message string) error {
	return d.deliver(ctx, userID, message, ProactiveCategoryNudge, false)
}

// SendGenericNotification satisfies the Miriam Notifier interface. Title and body
// are folded into a single iMessage; action receipts (money moved) are treated as
// important and bypass the daily cap so users always hear about their own money.
func (d *BridgeDispatcher) SendGenericNotification(ctx context.Context, userID uuid.UUID, title, message string) error {
	// Money-move / mandate receipts use the receipt category (critical bypass).
	category := ProactiveCategoryReceipt
	critical := true
	lower := strings.ToLower(title + " " + message)
	if strings.Contains(lower, "mandate") || strings.Contains(lower, "suggest") {
		category = ProactiveCategoryNudge
		critical = false
	}
	return d.deliver(ctx, userID, composeMessage(title, message), category, critical)
}

// SendToUser satisfies the worker push-sender shape (autopilot, daily pulse). The
// briefing/summary is delivered to iMessage. data is intentionally ignored — an
// iMessage has no notification payload. Non-critical, so it respects quiet hours.
func (d *BridgeDispatcher) SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error {
	category := ProactiveCategoryBriefing
	critical := false
	if data != nil {
		if t, _ := data["type"].(string); t == "autopilot_morning" || t == "anomaly" {
			category = ProactiveCategoryRisk
			critical = true
		}
	}
	return d.deliver(ctx, userID, composeMessage(title, body), category, critical)
}

func (d *BridgeDispatcher) deliver(ctx context.Context, userID uuid.UUID, message, category string, critical bool) error {
	if strings.TrimSpace(message) == "" {
		return nil
	}
	if d.guard != nil && !d.guard.AllowCategory(ctx, userID, category, critical) {
		return nil
	}

	threadID, err := d.threads.GetLastPlatformThread(ctx, userID, string(d.platform))
	if err != nil {
		return fmt.Errorf("resolve thread for %s: %w", d.platform, err)
	}
	if threadID == "" {
		d.logger.Debug("no active platform thread for user, skipping proactive message",
			zap.Stringer("user_id", userID),
			zap.String("platform", string(d.platform)),
		)
		return nil
	}

	msg := &OutboundMessage{
		Platform: d.platform,
		UserID:   userID.String(),
		ThreadID: threadID,
		Text:     message,
	}
	if err := d.send(ctx, msg); err != nil {
		return fmt.Errorf("send proactive message: %w", err)
	}
	return nil
}

// composeMessage folds a notification title and body into one iMessage. A bare
// "Miriam"-style label is dropped (she's always mid-conversation, never announces
// herself); a meaningful title becomes its own opening bubble.
func composeMessage(title, body string) string {
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if title == "" || strings.EqualFold(title, "miriam") {
		return body
	}
	if body == "" {
		return title
	}
	return title + "\n\n" + body
}
