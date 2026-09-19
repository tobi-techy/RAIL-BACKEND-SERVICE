package platform

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// AccountReader is the narrow view of an account the email backfill needs: the
// address on the row, so it can tell a real one from the opaque placeholder.
type AccountReader interface {
	EmailForUser(ctx context.Context, userID uuid.UUID) (string, error)
}

// userEmailGetter is the slice of the user repository this needs; satisfied by
// *repositories.UserRepository.
type userEmailGetter interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
}

type accountReaderAdapter struct{ users userEmailGetter }

// NewAccountReader adapts the user repository to the AccountReader the backfill
// needs. A missing user is reported as an empty address rather than an error, so
// a deleted account simply never gets asked.
func NewAccountReader(users userEmailGetter) AccountReader {
	return &accountReaderAdapter{users: users}
}

func (a *accountReaderAdapter) EmailForUser(ctx context.Context, userID uuid.UUID) (string, error) {
	profile, err := a.users.GetByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if profile == nil {
		return "", nil
	}
	return profile.Email, nil
}

// isBackfillAnswer reports whether a message is part of the backfill exchange --
// the address, the emailed code, or a decline -- rather than a fresh request.
//
// This distinction matters: routing EVERY message to the onboarder while the
// session is open meant "what's my balance?" mid-backfill was answered with
// another "what email should I put on your account?", swallowing the question.
// Only the exchange's own turns are routed; everything else flows normally and
// the session simply keeps waiting.
func isBackfillAnswer(text string) bool {
	if isSkipEmail(text) {
		return true
	}
	if normalizeEmail(text) != "" && strings.Contains(text, "@") {
		return true
	}
	return len(digitsOnly(text)) == 6
}

// emailBackfillTurn routes a message from a linked sender who is mid-way through
// the email backfill into the onboarder, which owns the address and code steps.
//
// Narrow on purpose: it asks the onboarder whether THIS sender has a backfill
// session rather than whether they have any session at all, so a leftover
// onboarding session on a now-linked account cannot hijack their messages.
func (p *Processor) emailBackfillTurn(
	ctx context.Context, msg InboundMessage, resolved *ResolvedUser,
) (bool, error) {
	if p.emailBackfill == nil || p.onboarder == nil || resolved == nil {
		return false, nil
	}
	if p.onboarder.IsEmailBackfillSession(ctx, msg.Platform, msg.UserID) &&
		isBackfillAnswer(msg.Text) {
		return true, p.handleOnboarding(ctx, msg)
	}
	return false, nil
}

// maybeAskForEmail sends the one-time request for a real address, for an account
// that still carries the opaque placeholder (phone+<uuid>@placeholder.invalid)
// and so can receive no receipts and cannot reset anything by email.
//
// Called AFTER the person's own reply has been sent: the ask must never replace
// or delay the answer to what they actually asked.
func (p *Processor) maybeAskForEmail(
	ctx context.Context, msg InboundMessage, resolved *ResolvedUser,
) {
	if p.emailBackfill == nil || p.onboarder == nil || resolved == nil {
		return
	}
	if p.onboarder.IsEmailBackfillSession(ctx, msg.Platform, msg.UserID) {
		return // already in the flow
	}
	if p.onboarder.HasAskedEmailBackfill(ctx, msg.Platform, msg.UserID) {
		return // asked once already
	}

	email, err := p.emailBackfill.EmailForUser(ctx, resolved.UserID)
	if err != nil {
		p.logger.Warn("email backfill: account lookup failed",
			zap.Error(err), zap.String("user_id", resolved.UserID.String()))
		return
	}
	if !IsPlaceholderEmail(email) {
		return // the account has a real address
	}

	// StartEmailBackfill records the ask itself, so a send failure below cannot
	// turn the ask into a nag on every subsequent message.
	reply, err := p.onboarder.StartEmailBackfill(ctx, msg.Platform, msg.UserID, resolved.UserID)
	if err != nil {
		p.logger.Warn("email backfill: could not start the session",
			zap.Error(err), zap.String("user_id", resolved.UserID.String()))
		return
	}
	if reply == nil || strings.TrimSpace(reply.Text) == "" {
		return
	}
	if err := p.sendToSender(ctx, msg, reply.Text); err != nil {
		p.logger.Warn("email backfill: ask failed to send",
			zap.Error(err), zap.String("user_id", resolved.UserID.String()))
	}
}
