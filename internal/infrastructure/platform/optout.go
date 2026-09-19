package platform

import (
	"context"
	"strings"

	"go.uber.org/zap"
)

// OptOutStore records a person's request to stop receiving messages, and reports
// whether a handle is currently suppressed.
//
// Durable on purpose (see migrations/307_platform_optouts.up.sql): an opt-out a
// cache flush can lose is not an opt-out. A nil store disables the feature
// entirely, which keeps tests and any deployment without the table working.
type OptOutStore interface {
	IsOptedOut(ctx context.Context, platform, senderID string) (bool, error)
	OptOut(ctx context.Context, platform, senderID, reason string) error
	Resume(ctx context.Context, platform, senderID string) error
}

// optOutWords are the unambiguous stop requests. "cancel" and "no" are
// deliberately absent: they usually answer a question, and a stray "no" must
// never unsubscribe someone from the product.
var optOutWords = map[string]bool{
	"stop":        true,
	"stop all":    true,
	"stopall":     true,
	"unsubscribe": true,
	"opt out":     true,
	"optout":      true,
	"quit":        true,
}

// resumeWords bring messaging back after an opt-out.
var resumeWords = map[string]bool{
	"start":     true,
	"unstop":    true,
	"resume":    true,
	"subscribe": true,
	"opt in":    true,
	"optin":     true,
}

// stagedActionExempt are stop-words that ALSO mean "cancel the staged action"
// when a confirmation is waiting. On a platform without tap buttons a bare
// "stop" is how someone declines a payment, so while an action is pending the
// vote path owns the word and it must not silently unsubscribe them.
var stagedActionExempt = map[string]bool{"stop": true, "quit": true}

// normaliseControlWord reduces a message to a bare control word so "Stop." and
// "  STOP! " are recognised, while "stop the transfer to Ada" is not.
func normaliseControlWord(text string) string {
	cleaned := strings.ToLower(strings.TrimSpace(text))
	cleaned = strings.Trim(cleaned, " .!,;:")
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if strings.Count(cleaned, " ") > 1 {
		// A control word is one or two words; longer text is a real message.
		return ""
	}
	return cleaned
}

// handleOptOut consumes global stop/start messages. It reports whether the
// message was handled, in which case Process must return without running any
// other flow.
func (p *Processor) handleOptOut(
	ctx context.Context, msg InboundMessage, resolved *ResolvedUser,
) (bool, error) {
	if p.optOut == nil {
		return false, nil
	}
	word := normaliseControlWord(msg.Text)
	if word == "" {
		return false, nil
	}
	optingOut, resuming := optOutWords[word], resumeWords[word]
	if !optingOut && !resuming {
		return false, nil
	}

	// "stop" answering a staged confirmation cancels that action instead.
	if optingOut && stagedActionExempt[word] && resolved != nil &&
		p.orchestrator.HasPendingPlatformAction(
			ctx, resolved.UserID.String(), resolved.Identity.ID.String(),
			msg.ThreadID, msg.Platform,
		) {
		return false, nil
	}

	platformName := string(msg.Platform)

	if resuming {
		if err := p.optOut.Resume(ctx, platformName, msg.UserID); err != nil {
			p.logger.Error("failed to record messaging resume",
				zap.Error(err), zap.String("sender", msg.UserID))
			return true, p.sendToSender(ctx, msg,
				"I couldn't update that just now. Text START again in a moment.")
		}
		return true, p.sendToSender(ctx, msg, "You're back on. Text me any time.")
	}

	already, err := p.optOut.IsOptedOut(ctx, platformName, msg.UserID)
	if err != nil {
		// Fail open, matching how the rest of the app treats a degraded
		// dependency: a database blip must not silently silence messaging for
		// every user. The trade-off is that a blip could let one message through
		// to someone who opted out, which is why this is logged at error level.
		p.logger.Error("opt-out lookup failed; treating as not opted out",
			zap.Error(err), zap.String("sender", msg.UserID))
	}
	if already {
		// Already stopped: stay silent rather than acknowledging a repeat
		// request. Messaging someone who asked us to stop is the exact harm this
		// control exists to prevent.
		return true, nil
	}

	// Acknowledge BEFORE recording, so the acknowledgement is not suppressed by
	// the outbound check that recording switches on.
	if err := p.sendToSender(ctx, msg,
		"Done - I won't message you again. Text START whenever you want me back."); err != nil {
		p.logger.Error("failed to send opt-out acknowledgement",
			zap.Error(err), zap.String("sender", msg.UserID))
	}
	if err := p.optOut.OptOut(ctx, platformName, msg.UserID, "user replied "+word); err != nil {
		p.logger.Error("failed to record opt-out",
			zap.Error(err), zap.String("sender", msg.UserID))
	}
	if p.onboarder != nil {
		if err := p.onboarder.ClearSession(ctx, msg.Platform, msg.UserID); err != nil {
			p.logger.Warn("failed to clear session after opt-out",
				zap.Error(err), zap.String("sender", msg.UserID))
		}
	}
	return true, nil
}
